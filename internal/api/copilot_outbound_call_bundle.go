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
	// v0.10.1455 (R2) — iş kodları (ham, sınırlı) + host-yalnız MV özeti.
	OutboundCodes(ctx context.Context, q chstore.OutboundQuery) ([]chstore.OutboundCode, error)
	OutboundHostCallers(ctx context.Context, host string, from, to time.Time) ([]chstore.ExternalCaller, error)
	OutboundHostTrend(ctx context.Context, host, caller string, from, to time.Time) ([]chstore.ExternalTrendPoint, error)
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
	default: // v0.10.1455 — ≥2 ortam: ortam başına kırılım
		hit.Envs = append([]string(nil), msgEnvs...)
	}
	if src == "mv" { // v0.10.1455 — sondanın MV penceresi (ölçüm boşsa genişleme)
		hit.ProbeS = rangeS
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
	} else {
		route.OutboundProbeS = 0 // açık pencere kazanır: genişleme yok
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
	Codes   []chstore.OutboundCode  `json:"codes,omitempty"` // v0.10.1455
	WeekErr string                  `json:"weekErr,omitempty"`
	DurMs   int64                   `json:"durMs"`
}

// outboundCacheKey — SAF: tüm girdiler (sıralı çağıranlar dahil) FNV.
// v2 (v0.10.1455): ortam kırılımı bayrağı + iş kodu gövdesi.
func outboundCacheKey(q chstore.OutboundQuery, step int64, perCaller bool) string {
	svcs := append([]string(nil), q.Services...)
	sort.Strings(svcs)
	envs := append([]string(nil), q.Envs...)
	sort.Strings(envs)
	return "cosre-outbound:v2:" + fnvStr(q.PathLike, strings.Join(svcs, "\x01"), q.PeerHost, strings.Join(envs, "\x01"),
		fmt.Sprint(q.From.Unix()/60), fmt.Sprint(q.To.Unix()/60), fmt.Sprint(step), fmt.Sprint(perCaller), fmt.Sprint(q.ByEnv))
}

// outboundQuerySummary — SAF: kanıt çipinin sorgu özeti (gerçek SQL uzun:
// normalize regex zinciri; çip okunur kalsın).
func outboundQuerySummary(q chstore.OutboundQuery) string {
	var b strings.Builder
	fmt.Fprintf(&b, "spans PREWHERE kind='client' AND db_system='' AND msg_system='' WHERE service_name IN (%s)", strings.Join(q.Services, ", "))
	if len(q.Envs) > 0 {
		fmt.Fprintf(&b, " AND deploy_env IN (%s)", strings.Join(q.Envs, ", "))
	}
	if q.PathLike != chstore.OutboundAllPaths {
		fmt.Fprintf(&b, " AND path ILIKE '%s'", q.PathLike)
	}
	if q.PeerHost != "" {
		fmt.Fprintf(&b, " AND peer = '%s'", q.PeerHost)
	}
	b.WriteString(" → count · avg · quantileTDigest(0.95) · hata")
	if q.ByEnv {
		b.WriteString(" GROUP BY deploy_env")
	}
	return b.String()
}

// guidedOutboundAnswer — v0.10.1454: ölçüm demeti + LLM'siz cevap.
// v0.10.1455: yolsuz rota (R2) host özetine gider (guidedOutboundHostAnswer).
func (s *Server) guidedOutboundAnswer(ctx context.Context, emit func(string, any), route guidedRoute, asked []string, from, to time.Time) (bool, bool) {
	if route.OutboundPath == "" {
		return s.guidedOutboundHostAnswer(ctx, emit, route, asked, from, to)
	}
	rd := outboundReadFn(s)
	if rd == nil || len(route.OutboundCallers) == 0 {
		return false, false
	}
	return s.outboundRawAnswer(ctx, emit, rd, route, asked, from, to, chstore.OutboundPathPattern(route.OutboundPath), route.OutboundPath, nil)
}

// outboundRawRead — ham okuma demeti: cur+prev tek geçiş; geçen hafta, seri
// ve iş kodları paralel. Önbellek 60 sn (yalnız başarılı ana okuma).
func (s *Server) outboundRawRead(ctx context.Context, rd outboundReader, q chstore.OutboundQuery, step int64, perCaller bool) (c outboundCached, readErr error, cached bool) {
	key := outboundCacheKey(q, step, perCaller)
	if s.metricChartCacheGet(ctx, key, &c) {
		return c, nil, true
	}
	span := q.To.Sub(q.From)
	wq := q
	wq.From, wq.To = q.From.Add(-outboundWeek), q.To.Add(-outboundWeek)
	start := time.Now()
	var wg sync.WaitGroup
	var weekErr error
	wg.Add(4)
	go func() { defer wg.Done(); c.Rows, readErr = rd.OutboundAggs(ctx, q, q.From.Add(-span)) }()
	go func() { defer wg.Done(); c.Week, weekErr = rd.OutboundAggs(ctx, wq, time.Time{}) }()
	go func() { defer wg.Done(); c.Points, _ = rd.OutboundSeries(ctx, q, step, perCaller) }()
	go func() { defer wg.Done(); c.Codes, _ = rd.OutboundCodes(ctx, q) }() // hata → bölüm yok (iş kodu ek kırılım)
	wg.Wait()
	c.DurMs = time.Since(start).Milliseconds()
	if weekErr != nil {
		c.WeekErr = weekErr.Error()
	}
	if readErr == nil {
		s.metricChartCacheSet(ctx, key, c, outboundCacheTTL)
	}
	return c, readErr, false
}

// outboundRawAnswer — ham client-span ölçümü (R1 yol; R2 ortam süzgeçli
// host özeti pathLike = OutboundAllPaths). ≥2 ortam → ortam başına kırılım.
func (s *Server) outboundRawAnswer(ctx context.Context, emit func(string, any), rd outboundReader, route guidedRoute, asked []string, from, to time.Time, pathLike, display string, notes []string) (bool, bool) {
	actx, cancel := context.WithTimeout(ctx, outboundAnswerTimeout)
	defer cancel()
	qFrom, qTo, clamp := outboundWindowFor(from, to, len(route.OutboundCallers))
	byEnv := len(route.EnvSet) >= 2
	q := chstore.OutboundQuery{PathLike: pathLike, Services: route.OutboundCallers, PeerHost: route.OutboundPeer, From: qFrom, To: qTo, ByEnv: byEnv}
	switch {
	case byEnv:
		q.Envs = append([]string(nil), route.EnvSet...)
	case route.Env != "":
		q.Envs = []string{route.Env}
	}
	step := outboundStepS(qFrom, qTo)
	perCaller := !byEnv && len(q.Services) > 1 && len(q.Services) <= outboundPerCallerMax

	am := map[string]any{"path": display, "path_like": q.PathLike, "services": q.Services, "range_s": int64(qTo.Sub(qFrom) / time.Second)}
	if q.PeerHost != "" {
		am["peer"] = q.PeerHost
	}
	if byEnv {
		am["envs"], am["by_env"] = q.Envs, true
	}
	if clamp != "" {
		am["window_clamped"] = true
	}
	args, _ := json.Marshal(am)
	n := emitGuidedStep(emit, outboundCallsTool, withEnvArg(string(args), route.Env))

	c, readErr, cached := s.outboundRawRead(actx, rd, q, step, perCaller)
	if cached {
		emitGuidedContextStep(emit, "önbellek: aynı giden çağrı okuması (≤60 sn)")
	}
	// v0.10.1455 — R1 açık notu: sonda yolu ölçümden geniş MV penceresinde (≤24
	// sa) bulduysa ve açık pencere yazılmamış varsayılan 1 sa boşsa, ölçüm
	// sondanın penceresine (≤6 sa tavanıyla) bir kez genişler — beyanlı.
	if readErr == nil && route.OutboundProbeS > 0 && outboundTotal(c.Rows, "cur").Calls == 0 {
		wFrom, _, wClamp := outboundWindowFor(qTo.Add(-time.Duration(route.OutboundProbeS)*time.Second), qTo, len(route.OutboundCallers))
		if old := qTo.Sub(qFrom); qTo.Sub(wFrom) > old {
			wq := q
			wq.From = wFrom
			wStep := outboundStepS(wFrom, qTo)
			if c2, err2, _ := s.outboundRawRead(actx, rd, wq, wStep, perCaller); err2 == nil {
				notes = append(notes, fmt.Sprintf("%s içinde bu yola giden çağrı yok; sonda yolu %s içinde bulmuştu — ölçüm penceresi %s'e genişletildi",
					fmtRangeTR(int64(old/time.Second)), fmtRangeTR(route.OutboundProbeS), strings.TrimPrefix(fmtRangeTR(int64(qTo.Sub(wFrom)/time.Second)), "son ")))
				c, q, qFrom, step, clamp = c2, wq, wFrom, wStep, wClamp
			}
		}
	}
	return s.emitOutboundOutcome(ctx, emit, n, outboundOutcome{route: route, display: display, qFrom: qFrom, qTo: qTo, c: c, readErr: readErr,
		byEnv: byEnv, pct: "p95", traces: true, evSource: outboundEvSource, query: outboundQuerySummary(q), step: step, perCaller: perCaller,
		clamp: clamp, notes: notes}, asked)
}

// outboundOutcome — ölçümün cevap girdisi (ham ve MV yolları ortak).
type outboundOutcome struct {
	route            guidedRoute
	display          string // anılan yol ya da "(tüm yollar)"
	qFrom, qTo       time.Time
	c                outboundCached
	readErr          error
	byEnv            bool
	pct              string // "p95" (ham, kesin) | "p99" (MV, kova maksimumu)
	traces           bool   // tablo örnek trace sütunlarını taşır
	mv               bool   // MV özeti: hüküm ortalama + hata üzerinden
	source, evSource string
	query            string
	step             int64
	perCaller        bool
	clamp            string
	notes            []string
}

// emitOutboundOutcome — kanıt + tablo + grafik + iş kodları + hüküm + cevap.
func (s *Server) emitOutboundOutcome(ctx context.Context, emit func(string, any), n int, o outboundOutcome, asked []string) (bool, bool) {
	route, c := o.route, o.c
	rangeS := int64(o.qTo.Sub(o.qFrom) / time.Second)
	in := outboundRender{Path: o.display, Peer: route.OutboundPeer, Callers: route.OutboundCallers, RangeS: rangeS,
		Env: route.Env, Scan: route.OutboundScan, ReadErr: o.readErr, Clamped: o.clamp, Notes: o.notes, Source: o.source}
	if o.byEnv {
		in.Envs = route.EnvSet
	}
	win := linkWindowBetween(o.qFrom, o.qTo)
	if o.readErr != nil {
		text := renderOutboundTR(in)
		emitGuidedStepResult(emit, n, outboundCallsTool, text, o.readErr)
		emit("answer", map[string]any{"text": text, "suggestions": guidedSuggestions(route),
			"links": dedupLinksByHref(guidedAnswerLinks(route, win))})
		return true, true
	}
	in.Cur, in.Prev, in.Week = outboundTotal(c.Rows, "cur"), outboundTotal(c.Rows, "prev"), outboundTotal(c.Week, "cur")
	if o.mv { // kova maksimumu p99 kesin yüzdelik değil: hüküm ortalama + hata
		var p99 float64
		for _, r := range c.Rows {
			if r.Win == "cur" {
				p99 = maxF(p99, r.P95Ms)
			}
		}
		in.Cur.P95Exact, in.Prev.P95Exact, in.Week.P95Exact = false, false, false
		if p99 > 0 {
			in.Notes = append(in.Notes, "p99 (5 dk kova maksimumu, MV) "+fmtMsTR(p99))
		}
	}
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
	if o.byEnv {
		in.EnvLines = outboundEnvLinesTR(c.Rows, c.Week)
	}
	codeRows := outboundCodeRows(c.Codes)
	if len(codeRows) > 0 {
		in.Notes = append(in.Notes, "iş kodu kırılımı (function_code / channel_code, ilk 5) ayrı tabloda")
	}
	text := renderOutboundTR(in)

	tracesHref := win.apply(outboundTracesHref(route, false))
	evWin := mcptools.EvidenceWindow{FromMs: o.qFrom.UnixMilli(), ToMs: o.qTo.UnixMilli()}
	ev := mcptools.ChatEvidence{Tool: outboundCallsTool, Source: o.evSource, Query: o.query, QueryKind: "sql",
		Window: evWin, Env: route.Env, DurationMs: c.DurMs, Href: tracesHref}
	tableRows, truncated := outboundTableRowsX(c.Rows, c.Week, o.byEnv, o.traces)
	nRows := len(tableRows)
	ev.Rows = &nRows
	ev = emitGuidedEvidenceResult(emit, n, outboundCallsTool, text, ev, nil)
	subject := outboundSubjectTR(route.OutboundCallers, route.OutboundPeer, o.display)
	if ev.Ref != "" && nRows > 0 {
		emit("ev_block", mcptools.EvidenceBlock{Type: blocks.TypeTable, Payload: mcptools.EvTablePayload{
			Title: subject + " · " + outboundSourceTR, EvidenceRef: ev.Ref, DeepLink: tracesHref,
			Columns: outboundTableColumnsFor(o.byEnv, o.pct, o.traces), Rows: tableRows, Truncated: truncated}})
	}

	// Grafik — ayrı kanıt (FE ref başına tek blok: tablo grafiği ezmesin).
	sn := emitGuidedStep(emit, outboundSeriesTool, fmt.Sprintf(`{"step_s":%d,"per_caller":%t,"by_env":%t}`, o.step, o.perCaller, o.byEnv))
	chart := outboundChartPayloadP(c.Points, outboundChartTitle(route.OutboundCallers, route.OutboundPeer, o.display), tracesHref, o.pct+" ms", o.qFrom, o.qTo)
	if chart != nil {
		ns := len(chart.Series)
		sev := mcptools.ChatEvidence{Tool: outboundSeriesTool, Source: o.evSource, Query: o.query + fmt.Sprintf(" GROUP BY toStartOfInterval(time, %d s)", o.step),
			QueryKind: "sql", Window: evWin, Env: route.Env, Series: &ns, DurationMs: c.DurMs, Href: tracesHref}
		emitGuidedEvidenceResult(emit, sn, outboundSeriesTool, fmt.Sprintf("%d seri, adım %d sn, %d kova; kaynak: %s. Grafik kanıt çipinde çizildi.", ns, o.step, len(c.Points), outboundSourceTR), sev, chart)
	} else {
		emitGuidedStepResult(emit, sn, outboundSeriesTool, "grafik yok: bu pencerede kova yok", nil)
	}
	// İş kodları — ayrı kanıt + tablo (yalnız ≥1 boş-olmayan kod varsa).
	if len(codeRows) > 0 {
		cn := emitGuidedStep(emit, outboundCodesTool, "")
		nc := len(codeRows)
		cev := mcptools.ChatEvidence{Tool: outboundCodesTool, Source: outboundEvSource, Query: outboundCodesQuerySummary(route), QueryKind: "sql",
			Window: evWin, Env: route.Env, Rows: &nc, DurationMs: c.DurMs, Href: tracesHref}
		cev = emitGuidedEvidenceResult(emit, cn, outboundCodesTool, fmt.Sprintf("%d iş kodu (function_code / channel_code, tür başına ilk %d)", nc, 5), cev, nil)
		if cev.Ref != "" {
			emit("ev_block", mcptools.EvidenceBlock{Type: blocks.TypeTable, Payload: mcptools.EvTablePayload{
				Title: subject + " · iş kodları · " + outboundSourceTR, EvidenceRef: cev.Ref, DeepLink: tracesHref,
				Columns: outboundCodeColumns, Rows: codeRows}})
		}
	}
	vn := emitGuidedStep(emit, outboundVerdictTool, "")
	kind, _ := outboundVerdict(in.Cur, in.Prev, in.Week)
	emitGuidedStepResult(emit, vn, outboundVerdictTool, outboundVerdictLabelTR(kind), nil)

	route.OutboundTraces = outboundSampleTraces(c.Rows)
	route.OutboundRangeS, route.OutboundFromMs, route.OutboundToMs = rangeS, o.qFrom.UnixMilli(), o.qTo.UnixMilli()
	ans := map[string]any{"text": text, "suggestions": guidedSuggestions(route),
		"links": dedupLinksByHref(guidedAnswerLinks(route, win))}
	emit("answer", withGuidedFollowups(ans, guidedFollowups(route, asked)))
	return true, true
}

// outboundCodesQuerySummary — SAF: iş kodu çipinin sorgu özeti.
func outboundCodesQuerySummary(route guidedRoute) string {
	return fmt.Sprintf("spans PREWHERE kind='client' WHERE service_name IN (%s) AND peer = '%s' → topK function_code / channel_code (LIMIT 5 BY kind)",
		strings.Join(route.OutboundCallers, ", "), route.OutboundPeer)
}

// ─── v0.10.1455 (R2) — yolsuz host özeti, MV-first ────────────────────

const (
	outboundHostMaxWindow = 24 * time.Hour // MV trendi (5 dk kova) tavanı
	outboundCodesTool     = "outbound_codes"
	outboundHostEvSource  = "ClickHouse · topology_edges_5m (MV) · trace (örneklenmiş)"
	outboundHostSourceTR  = "topology_edges_5m (MV) · trace (örneklenmiş)"
	outboundMVStepS       = 300
)

// outboundHostCallerScope — SAF: MV çağıranları mesaj / ekran servisine
// daraltır; servis bu hostu çağırmıyorsa tüm çağıranlar + beyan.
func outboundHostCallerScope(cur []chstore.ExternalCaller, svc, host string) ([]chstore.ExternalCaller, []string) {
	if svc == "" {
		return cur, nil
	}
	for _, c := range cur {
		if c.Service == svc {
			return []chstore.ExternalCaller{c}, nil
		}
	}
	if len(cur) == 0 {
		return cur, nil
	}
	return cur, []string{svc + " bu pencerede " + host + " hostunu çağırmıyor (MV); tüm çağıranlar gösteriliyor"}
}

// outboundHostAggs — SAF: MV çağıran satırları → ölçüm satırları (yüzdelik
// alanı p99 kova maksimumunu taşır; yol "(tüm yollar)"), keep süzgeciyle.
func outboundHostAggs(rows []chstore.ExternalCaller, host, win string, keep map[string]bool) []chstore.OutboundAgg {
	var out []chstore.OutboundAgg
	for _, r := range rows {
		if keep != nil && !keep[r.Service] {
			continue
		}
		out = append(out, chstore.OutboundAgg{Service: r.Service, Peer: host, Path: "(tüm yollar)", Win: win,
			Calls: r.Calls, Errors: r.Errors, AvgMs: r.AvgMs, P95Ms: r.P99Ms})
	}
	return out
}

// outboundTrendPoints — SAF: MV trendi → grafik kovaları (P95Ms = p99).
func outboundTrendPoints(tr []chstore.ExternalTrendPoint) []chstore.OutboundPoint {
	out := make([]chstore.OutboundPoint, 0, len(tr))
	for _, p := range tr {
		out = append(out, chstore.OutboundPoint{Bucket: p.Bucket, Calls: p.Calls, Errors: p.Errors, AvgMs: p.AvgMs, P95Ms: p.P99Ms})
	}
	return out
}

// outboundHostCallersTextTR — SAF: MV çağıran çipinin metni (≤5 satır).
func outboundHostCallersTextTR(rows []chstore.ExternalCaller, host string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d çağıran (topology_edges_5m)\n", host, len(rows))
	for i, r := range rows {
		if i == 5 {
			fmt.Fprintf(&b, "… +%d\n", len(rows)-5)
			break
		}
		fmt.Fprintf(&b, "%s: %s çağrı, ort %s\n", r.Service, fmtCountTR(r.Calls), fmtMsTR(r.AvgMs))
	}
	return b.String()
}

// guidedOutboundHostAnswer — v0.10.1455: yolsuz hedef (R2). Host tek adaya
// inmediyse "hangi dış host?" çipleri (tahmin yok). Aksi MV-first: çağıranlar
// (externalCallersSQL) cur / önceki / geçen hafta + trend (externalTrendSQL);
// ortam süzgeci istendiyse (MV ortam taşımaz) ham client-span yoluna geçer.
// İş kodları en yoğun ≤5 çağıranda sınırlı ham okumayla.
func (s *Server) guidedOutboundHostAnswer(ctx context.Context, emit func(string, any), route guidedRoute, asked []string, from, to time.Time) (bool, bool) {
	if route.OutboundPeer == "" {
		emit("answer", withGuidedFollowups(map[string]any{"text": outboundAskHostTR(route), "suggestions": guidedSuggestions(route),
			"links": dedupLinksByHref(guidedAnswerLinks(route, linkWindowBetween(from, to)))}, guidedFollowups(route, asked)))
		return true, true
	}
	rd := outboundReadFn(s)
	if rd == nil {
		return false, false
	}
	actx, cancel := context.WithTimeout(ctx, outboundAnswerTimeout)
	defer cancel()
	host, clamp := route.OutboundPeer, ""
	if to.Sub(from) > outboundHostMaxWindow {
		from, clamp = to.Add(-outboundHostMaxWindow), "pencere 24 saate kırpıldı (MV trendi)"
	}
	am := map[string]any{"host": host, "source": "mv"}
	if route.Service != "" {
		am["service"] = route.Service
	}
	args, _ := json.Marshal(am)
	pn := emitGuidedStep(emit, outboundProbeTool, string(args))
	start := time.Now()
	cur, err := rd.OutboundHostCallers(actx, host, from, to)
	if err != nil {
		emitGuidedStepResult(emit, pn, outboundProbeTool, "dış host çağıranları okunamadı", err)
		return s.emitOutboundOutcome(ctx, emit, emitGuidedStep(emit, outboundCallsTool, string(args)), outboundOutcome{route: route,
			display: "(tüm yollar)", qFrom: from, qTo: to, readErr: err, source: outboundHostSourceTR}, asked)
	}
	emitGuidedStepResult(emit, pn, outboundProbeTool, outboundHostCallersTextTR(cur, host), nil)
	sel, notes := outboundHostCallerScope(cur, route.Service, host)
	names := make([]string, 0, len(sel))
	keep := map[string]bool{}
	for _, c := range sel {
		if len(names) < chstore.OutboundCallerScanMax {
			names = append(names, c.Service)
			keep[c.Service] = true
		}
	}
	route.OutboundCallers = names
	if len(names) > 0 && (route.Env != "" || len(route.EnvSet) >= 2) {
		notes = append(notes, "ortam süzgeci istendiği için ham client span okuması (MV ortam taşımaz; yalnız URL taşıyan span'ler)")
		return s.outboundRawAnswer(ctx, emit, rd, route, asked, from, to, chstore.OutboundAllPaths, "(tüm yollar)", notes)
	}

	span := to.Sub(from)
	am["range_s"] = int64(span / time.Second)
	am["callers"] = names
	args, _ = json.Marshal(am)
	n := emitGuidedStep(emit, outboundCallsTool, string(args))
	c := outboundCached{Rows: outboundHostAggs(sel, host, "cur", nil)}
	if len(names) > 0 {
		caller := ""
		if len(names) == 1 {
			caller = names[0]
		}
		top := names
		if len(top) > outboundManyCallers {
			top = top[:outboundManyCallers]
		}
		cFrom, cTo, _ := outboundWindowFor(from, to, len(top))
		var prev, week []chstore.ExternalCaller
		var trend []chstore.ExternalTrendPoint
		var weekErr error
		var wg sync.WaitGroup
		wg.Add(4)
		go func() { defer wg.Done(); prev, _ = rd.OutboundHostCallers(actx, host, from.Add(-span), from) }()
		go func() {
			defer wg.Done()
			week, weekErr = rd.OutboundHostCallers(actx, host, from.Add(-outboundWeek), to.Add(-outboundWeek))
		}()
		go func() { defer wg.Done(); trend, _ = rd.OutboundHostTrend(actx, host, caller, from, to) }()
		go func() {
			defer wg.Done()
			c.Codes, _ = rd.OutboundCodes(actx, chstore.OutboundQuery{PathLike: chstore.OutboundAllPaths, Services: top, PeerHost: host, From: cFrom, To: cTo})
		}()
		wg.Wait()
		c.Rows = append(c.Rows, outboundHostAggs(prev, host, "prev", keep)...)
		c.Week = outboundHostAggs(week, host, "cur", keep)
		c.Points = outboundTrendPoints(trend)
		if weekErr != nil {
			c.WeekErr = weekErr.Error()
		}
		if len(top) < len(names) && len(c.Codes) > 0 {
			notes = append(notes, fmt.Sprintf("iş kodları en yoğun %d çağıranda okundu", len(top)))
		}
	}
	c.DurMs = time.Since(start).Milliseconds()
	query := fmt.Sprintf("topology_edges_5m FINAL WHERE node_kind='external' AND child_node='ext:%s'", host)
	if len(names) == 1 {
		query += fmt.Sprintf(" AND parent_service='%s'", names[0])
	}
	query += " → sum(calls) · sum(errors) · sum_duration/calls · max(p99_ms) (cur · önceki · geçen hafta)"
	return s.emitOutboundOutcome(ctx, emit, n, outboundOutcome{route: route, display: "(tüm yollar)", qFrom: from, qTo: to, c: c,
		pct: "p99", mv: true, source: outboundHostSourceTR, evSource: outboundHostEvSource, query: query, step: outboundMVStepS,
		clamp: clamp, notes: notes}, asked)
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
