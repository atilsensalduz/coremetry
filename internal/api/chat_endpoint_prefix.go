package api

// chat_endpoint_prefix.go — v0.10.1482: `/x/**` ÖNEK KİPİ.
//
// Sondaki `/**` (ya da `/*`) taşıyan ve BAŞTA Markdown sarmalayıcısı OLMAYAN
// yol anması "bu önekin altındaki her şey" demektir (v0.10.1373 joker kuralı;
// v0.10.1447'den beri `**/x/**` sarmalayıcıdır, joker değil — yol "/x" olur ve
// tam-yol akışı aynen koşar). Önek kipinde endpoint sorusu önekin altındaki
// TÜM http_route'ları toplar (chstore.RoutePrefixRED, spanmetrics_1m):
// toplam çağrı / hata % / çağrı ağırlıklı ortalama + önceki eşit pencere,
// rota başına satır (ilk epPrefixListMax, çağrı sırası) ve p95 YALNIZ satır
// başına (yüzdelikler toplanmaz). Cevap deterministik (model yok) ve önek
// toplamı olduğunu ilk cümlede söyler.
//
// Önekin altında hiç rota yoksa (ya da okuma düştüyse — çipte görünür) akış
// bayt bayt eskiye döner: tam-yol katalog eşleşmesi → yol-yalnız hedef →
// dış-yol sondası (giden çağrı; outbound_call önek notunu kendisi yazar).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	epPrefixTool    = "endpoint_prefix"
	epPrefixListMax = 10
	epPrefixSource  = "ClickHouse spanmetrics_1m (örneklenmiş span)"
)

// routePrefixSeam — test dikişi (nil = store).
var routePrefixSeam func(ctx context.Context, service, prefix string, from, to time.Time, limit int) (chstore.RoutePrefixREDResult, error)

// pathPrefixOf — SAF (v0.10.1482): "/x/**" ya da "/x/*" → "/x". Önek geçerli
// bir yol olmalı ve başka joker/elips taşımamalı (aksi bugünkü akış).
func pathPrefixOf(path string) (string, bool) {
	var p string
	switch {
	case strings.HasSuffix(path, "/**"):
		p = strings.TrimSuffix(path, "/**")
	case strings.HasSuffix(path, "/*"):
		p = strings.TrimSuffix(path, "/*")
	default:
		return "", false
	}
	if !validEndpointPath(p) || strings.Contains(p, "*") || strings.Contains(p, "...") {
		return "", false
	}
	return p, true
}

// endpointPrefixOf — SAF (v0.10.1482): mesajın ilk yol anması sarmalayıcısız
// bir önek mi. Baştaki `*`/`_` sarmalayıcıdır (v0.10.1447) → önek DEĞİL.
func endpointPrefixOf(raw string) (method, prefix string, ok bool) {
	raw = strings.ReplaceAll(raw, "…", "...")
	m, path, wrapped, found := endpointMentionWrap(raw)
	if !found || wrapped {
		return "", "", false
	}
	p, pok := pathPrefixOf(path)
	if !pok {
		return "", "", false
	}
	return m, p, true
}

// endpointPrefixRender — metin girdisi.
type endpointPrefixRender struct {
	Prefix, Method, Service string
	RangeS                  int64
	Res                     chstore.RoutePrefixREDResult
}

// prefixStat — SAF: önek toplamı → hüküm girdisi (p95 kesin DEĞİL).
func prefixStat(t chstore.RoutePrefixTotal) outboundStat {
	return outboundStat{Calls: t.Calls, Errors: t.Errors, AvgMs: t.AvgMs, Groups: int(t.Routes)}
}

// renderEndpointPrefixTR — SAF (v0.10.1482): deterministik Türkçe cevap.
func renderEndpointPrefixTR(in endpointPrefixRender) string {
	var b strings.Builder
	scope := in.Prefix + " altındaki tüm HTTP rotaları"
	if in.Service != "" {
		scope += ", servis " + in.Service
	}
	cur, prev := prefixStat(in.Res.Cur), prefixStat(in.Res.Prev)
	fmt.Fprintf(&b, "%s/** önek toplamı (%s; %s, kaynak: %s): %d rota, %s çağrı", in.Prefix, scope, fmtRangeTR(in.RangeS), epPrefixSource,
		in.Res.Cur.Routes, fmtCountTR(cur.Calls))
	if prev.Calls > 0 {
		fmt.Fprintf(&b, " (önceki eşit pencere %s)", fmtCountTR(prev.Calls))
	}
	fmt.Fprintf(&b, ", ort %s", fmtMsTR(cur.AvgMs))
	if prev.measured() {
		fmt.Fprintf(&b, " (önceki %s", fmtMsTR(prev.AvgMs))
		if prev.AvgMs > 0 {
			b.WriteString(" → ×" + trNum(cur.AvgMs/prev.AvgMs))
		}
		b.WriteString(")")
	}
	fmt.Fprintf(&b, ", hata %%%s", trNum(cur.errPct()))
	if prev.measured() {
		fmt.Fprintf(&b, " (önceki %%%s)", trNum(prev.errPct()))
	}
	b.WriteString(". ")
	switch kind, _ := outboundVerdict(cur, prev, outboundStat{}); kind {
	case obVerdictInsufficient:
		fmt.Fprintf(&b, "Yetersiz veri (%d çağrı < %d) — hüküm yok.", cur.Calls, outboundMinCalls)
	case obVerdictNoRef:
		b.WriteString("Kıyas yok: önceki pencere yeterli çağrı taşımıyor — hüküm yok.")
	case obVerdictBoth:
		b.WriteString("**ARTIŞ VAR** (ortalama gecikme ve hata oranı).")
	case obVerdictRise:
		b.WriteString("**ARTIŞ VAR** (ortalama gecikme).")
	case obVerdictErrRise:
		b.WriteString("**HATA ARTIŞI VAR**.")
	default:
		b.WriteString("Belirgin fark yok (±%20 / +2 puan eşiği altında).")
	}
	var top *chstore.RoutePrefixRow
	for i := range in.Res.Rows {
		if r := &in.Res.Rows[i]; top == nil || r.P95Ms > top.P95Ms {
			top = r
		}
	}
	b.WriteString("\np95 yalnız rota başına (yüzdelikler toplanmaz)")
	if top != nil && top.P95Ms > 0 {
		fmt.Fprintf(&b, "; en yüksek p95: %s %s (%s)", top.Route, fmtMsTR(top.P95Ms), top.Service)
	}
	b.WriteString(".")
	listed := min(len(in.Res.Rows), epPrefixListMax)
	fmt.Fprintf(&b, "\nİlk %d rota tabloda (çağrı sırası)", listed)
	if rest := int(in.Res.Cur.Routes) - listed; rest > 0 {
		fmt.Fprintf(&b, "; %d rota listelenmedi (toplamlara dahil)", rest)
	}
	b.WriteString(".")
	if in.Method != "" {
		fmt.Fprintf(&b, "\nNot: %s fiil süzgeci uygulanmadı (http_route fiil taşımaz) — tüm fiiller.", in.Method)
	}
	return b.String()
}

// endpointPrefixRows — SAF: tablo satırları (≤epPrefixListMax).
func endpointPrefixRows(rows []chstore.RoutePrefixRow) [][]any {
	out := [][]any{}
	for i, r := range rows {
		if i >= epPrefixListMax {
			break
		}
		errPct := 0.0
		if r.Calls > 0 {
			errPct = float64(r.Errors) * 100 / float64(r.Calls)
		}
		out = append(out, []any{r.Service, r.Route, r.Calls, round1(errPct), round1(r.AvgMs), round1(r.P95Ms)})
	}
	return out
}

var endpointPrefixColumns = []mcptools.EvTableColumn{
	{Key: "service", Label: "Servis", Kind: "text"},
	{Key: "route", Label: "Rota", Kind: "text"},
	{Key: "calls", Label: "Çağrı", Kind: "num"},
	{Key: "err", Label: "Hata %", Kind: "num"},
	{Key: "avg", Label: "Ort ms", Kind: "num"},
	{Key: "p95", Label: "p95 ms", Kind: "num"},
}

// readRoutePrefix — dikiş ya da store.
func (s *Server) readRoutePrefix(ctx context.Context, service, prefix string, from, to time.Time) (chstore.RoutePrefixREDResult, error) {
	if routePrefixSeam != nil {
		return routePrefixSeam(ctx, service, prefix, from, to, epPrefixListMax+1)
	}
	return s.store.RoutePrefixRED(ctx, service, prefix, from, to, epPrefixListMax+1)
}

// endpointPrefixPrefetch — v0.10.1482: önek kipinde deterministik cevap.
// true → cevap yayınlandı (tur biter); false → bugünkü akış (önek değil,
// vetolu, log sorusu, okuma hatası ya da önekin altında rota yok).
func (s *Server) endpointPrefixPrefetch(pctx, ctx context.Context, emit func(string, any), raw string, rangeS int64, anchor time.Time) bool {
	if s.store == nil && routePrefixSeam == nil {
		return false
	}
	method, prefix, ok := endpointPrefixOf(raw)
	if !ok {
		return false
	}
	envs := s.guidedEnvNames(pctx)
	if endpointMentionVetoed(raw, envs) || !endpointPathOnlyAllowed(raw) {
		return false
	}
	svc := firstString(endpointPathOnlyTarget(raw, method, prefix+"/**", s.guidedServiceNames(pctx), envs).Services)
	if rangeS <= 0 {
		rangeS = opDefaultRangeS
	}
	to := anchor
	if to.IsZero() {
		to = time.Now()
	}
	from := to.Add(-time.Duration(rangeS) * time.Second)
	argm := map[string]any{"prefix": prefix + "/**", "range_s": rangeS}
	if svc != "" {
		argm["service"] = svc
	}
	args, _ := json.Marshal(argm)
	n := emitGuidedStep(emit, epPrefixTool, string(args))
	t0 := time.Now()
	res, err := s.readRoutePrefix(pctx, svc, prefix, from, to)
	if err != nil {
		emitGuidedStepResult(emit, n, epPrefixTool, "", fmt.Errorf("önek okuması düştü (okunamadı ≠ yok): %w", err))
		return false
	}
	if len(res.Rows) == 0 || res.Cur.Calls == 0 {
		emitGuidedStepResult(emit, n, epPrefixTool, "önekin altında http_route yok — tam yol / giden çağrı akışı", nil)
		return false
	}
	text := renderEndpointPrefixTR(endpointPrefixRender{Prefix: prefix, Method: method, Service: svc, RangeS: rangeS, Res: res})
	rows := endpointPrefixRows(res.Rows)
	nRows := len(rows)
	ev := mcptools.ChatEvidence{Tool: epPrefixTool, Source: epPrefixSource, QueryKind: "sql",
		Query:  fmt.Sprintf("spanmetrics_1m WHERE http_route = %q OR startsWith(http_route, %q) AND kind NOT IN (client, producer) GROUP BY service_name, http_route", prefix, prefix+"/"),
		Window: mcptools.EvidenceWindow{FromMs: from.UnixMilli(), ToMs: to.UnixMilli()}, Rows: &nRows, DurationMs: time.Since(t0).Milliseconds()}
	ev = emitGuidedEvidenceResult(emit, n, epPrefixTool, text, ev, nil)
	if ev.Ref != "" {
		emit("ev_block", mcptools.EvidenceBlock{Type: blocks.TypeTable, Payload: mcptools.EvTablePayload{
			Title: prefix + "/** · önek toplamı · " + epPrefixSource, EvidenceRef: ev.Ref,
			Columns: endpointPrefixColumns, Rows: rows, Truncated: int(res.Cur.Routes) > nRows}})
	}
	emit("answer", map[string]any{"text": text, "exchangeId": copilot.MetaFromContext(ctx).ExchangeID})
	return true
}
