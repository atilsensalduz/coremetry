package api

// copilot_outbound_call_bundle.go — v0.10.1454: outbound_call (R1) sunucu
// yarısı. Sonda (ön-getirme kademesinde, ≤5 sn) + ölçüm demeti (guided, ≤10
// sn): cur+prev tek geçiş, geçen hafta ve zaman serisi paralel; cevap LLM'siz
// (operatör kararı S1 — sayılar sunucudan, model yeniden saymaz). Salt-okur:
// yazma, denetim, model çağrısı yok.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// outboundReader — okuyucu dikişi (*chstore.Store karşılar; testler sahte takar).
type outboundReader interface {
	OutboundProbeMV(ctx context.Context, needle, service string, from, to time.Time) ([]chstore.OutboundCaller, error)
	OutboundExternalCallers(ctx context.Context, from, to time.Time) ([]string, error)
	OutboundProbeRaw(ctx context.Context, q chstore.OutboundQuery) ([]chstore.OutboundCaller, error)
	OutboundAggs(ctx context.Context, q chstore.OutboundQuery, prevFrom time.Time) ([]chstore.OutboundAgg, error)
	OutboundSeries(ctx context.Context, q chstore.OutboundQuery, stepS int64, perCaller bool) ([]chstore.OutboundPoint, error)
}

// outboundReadFn — paket değişkeni (fakeTools.install emsali); nil = okuyucu yok.
var outboundReadFn = func(s *Server) outboundReader {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store
}

const (
	outboundProbeTimeout  = 5 * time.Second
	outboundAnswerTimeout = 10 * time.Second
	outboundProbeTool     = "outbound_probe"
	outboundCallsTool     = "outbound_calls"
	outboundSeriesTool    = "outbound_series"
	outboundVerdictTool   = "outbound_verdict"
	outboundEvSource      = "ClickHouse · spans (client) · trace (örneklenmiş)"
)

func firstString(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// outboundProbe — v0.10.1454: pathOnly kapanışının sondası. MV ön süzgeci
// (top_labels) → boşsa ham sonda (son 1 sa; çağıran = servis, yoksa ya da
// ekran servisi çağırmıyorsa ≤100 dış çağıran — operatör kararı S2: tara +
// beyan). ok=false → bugünkü yol-yalnız hedef bayt bayt.
func (s *Server) outboundProbe(ctx context.Context, emit func(string, any), slot *outboundSlot, path, msgService string, msgEnvs []string) (outboundHit, bool) {
	rd := outboundReadFn(s)
	if rd == nil || slot == nil || path == "" {
		return outboundHit{}, false
	}
	svc, fromScreen := msgService, false
	if svc == "" && slot.service != "" {
		svc, fromScreen = slot.service, true
	}
	to := slot.anchor
	if to.IsZero() {
		to = time.Now()
	}
	rangeS := slot.rangeS
	if rangeS < 3600 {
		rangeS = 3600
	}
	if rangeS > 86400 {
		rangeS = 86400
	}
	from := to.Add(-time.Duration(rangeS) * time.Second)
	pctx, cancel := context.WithTimeout(ctx, outboundProbeTimeout)
	defer cancel()
	am := map[string]any{"path": path}
	if svc != "" {
		am["service"] = svc
	}
	args, _ := json.Marshal(am)
	n := emitGuidedStep(emit, outboundProbeTool, string(args))

	var rows []chstore.OutboundCaller
	var err error
	src, scan := "mv", false
	if needle := chstore.OutboundPathNeedle(path); needle != "" {
		rows, err = rd.OutboundProbeMV(pctx, needle, svc, from, to)
		if err == nil && len(rows) == 0 && svc != "" {
			rows, err = rd.OutboundProbeMV(pctx, needle, "", from, to)
		}
	}
	if len(rows) == 0 { // MV boş ≠ yok (yol kenarın en sık 5 etiketinde olmayabilir) → ham sonda
		src, err = "raw", nil
		rTo := to
		rFrom := rTo.Add(-outboundScanWindow)
		q := chstore.OutboundQuery{PathLike: chstore.OutboundPathPattern(path), From: rFrom, To: rTo}
		if svc != "" {
			q.Services = []string{svc}
			rows, err = rd.OutboundProbeRaw(pctx, q)
		}
		if err == nil && len(rows) == 0 && (svc == "" || fromScreen) {
			scan = true
			if q.Services, err = rd.OutboundExternalCallers(pctx, rFrom, rTo); err == nil && len(q.Services) > 0 {
				rows, err = rd.OutboundProbeRaw(pctx, q)
			}
		}
	}
	if err != nil || len(rows) == 0 {
		text := "bu yola giden client span bulunamadı — yol-yalnız trace aramasına dönülüyor"
		if err != nil {
			text = "dış-yol sondası okunamadı — yol-yalnız trace aramasına dönülüyor"
		}
		emitGuidedStepResult(emit, n, outboundProbeTool, text, err)
		return outboundHit{}, false
	}
	callers, peer := outboundCallersFrom(rows, svc)
	hit := outboundHit{Path: path, Callers: callers, Peer: peer, Scan: scan, Src: src}
	if len(callers) == 1 {
		hit.Service = callers[0]
	} else if slot.service != "" {
		hit.Service = slot.service
	}
	switch {
	case len(msgEnvs) == 1:
		hit.Env = msgEnvs[0]
	case len(msgEnvs) == 0:
		hit.Env = slot.env
	}
	emitGuidedStepResult(emit, n, outboundProbeTool, outboundProbeTextTR(rows, src, scan), nil)
	return hit, true
}

// outboundProbeTextTR — SAF: sonda çipinin kanıt metni (≤5 satır).
func outboundProbeTextTR(rows []chstore.OutboundCaller, src string, scan bool) string {
	var b strings.Builder
	how := "MV ön süzgeci (topology_edges_5m)"
	if src == "raw" {
		how = "ham sonda (spans, son 1 sa)"
	}
	fmt.Fprintf(&b, "%d çağıran·host — %s", len(rows), how)
	if scan {
		fmt.Fprintf(&b, "; çağıran belirsiz: ≤%d dış çağıran tarandı", chstore.OutboundCallerScanMax)
	}
	b.WriteString("\n")
	for i, r := range rows {
		if i == 5 {
			fmt.Fprintf(&b, "… +%d\n", len(rows)-5)
			break
		}
		fmt.Fprintf(&b, "%s → %s: %s çağrı\n", r.Service, r.Host, fmtCountTR(r.Calls))
	}
	return b.String()
}

// guidedOutboundRoute — copilotChatGuided'in yuva dalı: pencere (açık >
// son 1 sa) → runGuidedRoute (kapsam + /ai rota notu) → bağlam notu.
func (s *Server) guidedOutboundRoute(ctx context.Context, emit func(string, any), hit outboundHit, question, norm string, msgs []copilot.ChatMessage, explain, ctxService, ctxOperation string, anchorTo time.Time, loc *time.Location) (bool, bool) {
	route := hit.route()
	rangeS, explicit := guidedRangeSExplicit(norm)
	if !explicit {
		rangeS = outboundDefaultRangeS
	}
	handled, ok := s.runGuidedRoute(ctx, emit, route, rangeS, question, msgs, explain, ctxService, ctxOperation, "", anchorTo, loc)
	if handled {
		s.noteChatContextRoute(ctx, route, rangeS, explicit)
	}
	return handled, ok
}

// outboundCached — 60 sn önbellek gövdesi (yalnız okuma sonuçları).
type outboundCached struct {
	Rows    []chstore.OutboundAgg   `json:"rows"`
	Week    []chstore.OutboundAgg   `json:"week"`
	Points  []chstore.OutboundPoint `json:"points"`
	WeekErr string                  `json:"weekErr,omitempty"`
	DurMs   int64                   `json:"durMs"`
}

// outboundCacheKey — SAF: tüm girdiler (sıralı çağıranlar dahil) FNV.
func outboundCacheKey(q chstore.OutboundQuery, step int64, perCaller bool) string {
	svcs := append([]string(nil), q.Services...)
	sort.Strings(svcs)
	envs := append([]string(nil), q.Envs...)
	sort.Strings(envs)
	return "cosre-outbound:v1:" + fnvStr(q.PathLike, strings.Join(svcs, "\x01"), q.PeerHost, strings.Join(envs, "\x01"),
		fmt.Sprint(q.From.Unix()/60), fmt.Sprint(q.To.Unix()/60), fmt.Sprint(step), fmt.Sprint(perCaller))
}

// outboundQuerySummary — SAF: kanıt çipinin sorgu özeti (gerçek SQL uzun:
// normalize regex zinciri; çip okunur kalsın).
func outboundQuerySummary(q chstore.OutboundQuery) string {
	var b strings.Builder
	fmt.Fprintf(&b, "spans PREWHERE kind='client' AND db_system='' AND msg_system='' WHERE service_name IN (%s)", strings.Join(q.Services, ", "))
	if len(q.Envs) > 0 {
		fmt.Fprintf(&b, " AND deploy_env IN (%s)", strings.Join(q.Envs, ", "))
	}
	fmt.Fprintf(&b, " AND path ILIKE '%s'", q.PathLike)
	if q.PeerHost != "" {
		fmt.Fprintf(&b, " AND peer = '%s'", q.PeerHost)
	}
	b.WriteString(" → count · avg · quantileTDigest(0.95) · hata")
	return b.String()
}

// guidedOutboundAnswer — v0.10.1454: ölçüm demeti + LLM'siz cevap.
func (s *Server) guidedOutboundAnswer(ctx context.Context, emit func(string, any), route guidedRoute, asked []string, from, to time.Time) (bool, bool) {
	rd := outboundReadFn(s)
	if rd == nil || route.OutboundPath == "" || len(route.OutboundCallers) == 0 {
		return false, false
	}
	actx, cancel := context.WithTimeout(ctx, outboundAnswerTimeout)
	defer cancel()
	qFrom, qTo, clamp := outboundWindowFor(from, to, len(route.OutboundCallers))
	span := qTo.Sub(qFrom)
	rangeS := int64(span / time.Second)
	q := chstore.OutboundQuery{PathLike: chstore.OutboundPathPattern(route.OutboundPath), Services: route.OutboundCallers,
		PeerHost: route.OutboundPeer, From: qFrom, To: qTo}
	if route.Env != "" {
		q.Envs = []string{route.Env}
	}
	wq := q
	wq.From, wq.To = q.From.Add(-outboundWeek), q.To.Add(-outboundWeek)
	step := outboundStepS(qFrom, qTo)
	perCaller := len(q.Services) > 1 && len(q.Services) <= outboundPerCallerMax

	am := map[string]any{"path": route.OutboundPath, "path_like": q.PathLike, "services": q.Services, "range_s": rangeS}
	if q.PeerHost != "" {
		am["peer"] = q.PeerHost
	}
	if clamp != "" {
		am["window_clamped"] = true
	}
	args, _ := json.Marshal(am)
	n := emitGuidedStep(emit, outboundCallsTool, withEnvArg(string(args), route.Env))

	key := outboundCacheKey(q, step, perCaller)
	var c outboundCached
	var readErr error
	if s.metricChartCacheGet(actx, key, &c) {
		emitGuidedContextStep(emit, "önbellek: aynı giden çağrı okuması (≤60 sn)")
	} else {
		start := time.Now()
		var wg sync.WaitGroup
		var weekErr error
		wg.Add(3)
		go func() { defer wg.Done(); c.Rows, readErr = rd.OutboundAggs(actx, q, q.From.Add(-span)) }()
		go func() { defer wg.Done(); c.Week, weekErr = rd.OutboundAggs(actx, wq, time.Time{}) }()
		go func() { defer wg.Done(); c.Points, _ = rd.OutboundSeries(actx, q, step, perCaller) }()
		wg.Wait()
		c.DurMs = time.Since(start).Milliseconds()
		if weekErr != nil {
			c.WeekErr = weekErr.Error()
		}
		if readErr == nil {
			s.metricChartCacheSet(ctx, key, c, outboundCacheTTL)
		}
	}
	in := outboundRender{Path: route.OutboundPath, Peer: route.OutboundPeer, Callers: route.OutboundCallers, RangeS: rangeS,
		Env: route.Env, Scan: route.OutboundScan, ReadErr: readErr, Clamped: clamp}
	if readErr != nil {
		text := renderOutboundTR(in)
		emitGuidedStepResult(emit, n, outboundCallsTool, text, readErr)
		ans := map[string]any{"text": text, "suggestions": guidedSuggestions(route),
			"links": dedupLinksByHref(guidedAnswerLinks(route, linkWindowBetween(qFrom, qTo)))}
		emit("answer", ans)
		return true, true
	}
	in.Cur, in.Prev, in.Week = outboundTotal(c.Rows, "cur"), outboundTotal(c.Rows, "prev"), outboundTotal(c.Week, "cur")
	if c.WeekErr != "" {
		in.WeekErr = fmt.Errorf("%s", c.WeekErr)
	}
	paths := map[string]bool{}
	for _, r := range c.Rows {
		if r.Win == "cur" {
			paths[r.Path] = true
		}
	}
	in.PathGroups = len(paths)
	text := renderOutboundTR(in)

	win := linkWindowBetween(qFrom, qTo)
	tracesHref := win.apply(outboundTracesHref(route, false))
	evWin := mcptools.EvidenceWindow{FromMs: qFrom.UnixMilli(), ToMs: qTo.UnixMilli()}
	ev := mcptools.ChatEvidence{Tool: outboundCallsTool, Source: outboundEvSource, Query: outboundQuerySummary(q), QueryKind: "sql",
		Window: evWin, Env: route.Env, DurationMs: c.DurMs, Href: tracesHref}
	tableRows, truncated := outboundTableRows(c.Rows, c.Week)
	nRows := len(tableRows)
	ev.Rows = &nRows
	ev = emitGuidedEvidenceResult(emit, n, outboundCallsTool, text, ev, nil)
	if ev.Ref != "" && nRows > 0 {
		emit("ev_block", mcptools.EvidenceBlock{Type: blocks.TypeTable, Payload: mcptools.EvTablePayload{
			Title: outboundSubjectTR(route.OutboundCallers, route.OutboundPeer, route.OutboundPath) + " · " + outboundSourceTR, EvidenceRef: ev.Ref,
			DeepLink: tracesHref, Columns: outboundTableColumns, Rows: tableRows, Truncated: truncated}})
	}

	// Grafik — ayrı kanıt (FE ref başına tek blok: tablo grafiği ezmesin).
	sn := emitGuidedStep(emit, outboundSeriesTool, fmt.Sprintf(`{"step_s":%d,"per_caller":%t}`, step, perCaller))
	chart := outboundChartPayload(c.Points, outboundChartTitle(route.OutboundCallers, route.OutboundPeer, route.OutboundPath), tracesHref, qFrom, qTo)
	if chart != nil {
		ns := len(chart.Series)
		sev := mcptools.ChatEvidence{Tool: outboundSeriesTool, Source: outboundEvSource, Query: outboundQuerySummary(q) + fmt.Sprintf(" GROUP BY toStartOfInterval(time, %d s)", step),
			QueryKind: "sql", Window: evWin, Env: route.Env, Series: &ns, DurationMs: c.DurMs, Href: tracesHref}
		emitGuidedEvidenceResult(emit, sn, outboundSeriesTool, fmt.Sprintf("%d seri, adım %d sn, %d kova; kaynak: %s. Grafik kanıt çipinde çizildi.", ns, step, len(c.Points), outboundSourceTR), sev, chart)
	} else {
		emitGuidedStepResult(emit, sn, outboundSeriesTool, "grafik yok: bu pencerede kova yok", nil)
	}
	vn := emitGuidedStep(emit, outboundVerdictTool, "")
	kind, _ := outboundVerdict(in.Cur, in.Prev, in.Week)
	emitGuidedStepResult(emit, vn, outboundVerdictTool, outboundVerdictLabelTR(kind), nil)

	route.OutboundTraces = outboundSampleTraces(c.Rows)
	route.OutboundRangeS, route.OutboundFromMs, route.OutboundToMs = rangeS, qFrom.UnixMilli(), qTo.UnixMilli()
	ans := map[string]any{"text": text, "suggestions": guidedSuggestions(route),
		"links": dedupLinksByHref(guidedAnswerLinks(route, win))}
	emit("answer", withGuidedFollowups(ans, guidedFollowups(route, asked)))
	return true, true
}

// outboundVerdictLabelTR — SAF: hüküm çipinin kısa metni.
func outboundVerdictLabelTR(kind string) string {
	switch kind {
	case obVerdictInsufficient:
		return "yetersiz veri — hüküm yok"
	case obVerdictNoRef:
		return "kıyas penceresi yok — hüküm yok"
	case obVerdictBoth:
		return "ARTIŞ VAR (gecikme + hata)"
	case obVerdictRise:
		return "ARTIŞ VAR (gecikme)"
	case obVerdictErrRise:
		return "HATA ARTIŞI VAR"
	}
	return "belirgin fark yok"
}
