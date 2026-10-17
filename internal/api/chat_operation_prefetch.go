package api

// chat_operation_prefetch.go — CoSRE operasyon tespiti Faz 2. Kullanıcı
// sohbete bir operasyon adı yazınca, model ÇAĞRILMADAN önce:
//   1. mesaj belirteçleri cosre_operation_catalog'la eşlenir (birebir →
//      harf duyarsız → önek/içerir; <4 karakter belirteç yok sayılır;
//      bulanık aşama yalnız tanımlayıcı-biçimli belirteçte — "servis" gibi
//      sıradan kelime operasyon adının içinde geçip tetiklemesin);
//   2. tek operasyon → sohbet penceresi (varsayılan 1 sa) için: ≤20 trace
//      (önce hatalılar), hata oranı / p99 / çağrı + önceki eşit pencereye
//      fark, trace'lere TRACEID ile bağlı Oracle teknik hataları;
//   3. özet modele HAM trace değil, kompakt metin olarak gider: serbest
//      döngünün konuşmasına sentetik bir tool çağrısı + sonucu eklenir
//      (model getirildiğini bilir, gerekirse başka tool çağırır);
//   4. birden çok operasyon → seçim çipleri, tahmin yok; eşleşme yok →
//      normal akış (bayt bayt eski).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	opTokenMin        = 4
	opTokenMax        = 12
	opCandidateLimit  = 60
	opChoiceMax       = 5
	opTraceLimit      = 20
	opDefaultRangeS   = 3600
	opPrefetchToolID  = "cosre-operation-prefetch"
	opPrefetchTool    = "operation_prefetch"
	opPrefetchTimeout = 15 * time.Second
)

// opTokens — SAF: mesajdan aday belirteçler (tekil, sırayı korur). Ayraç:
// boşluk ve noktalama; '_' '-' '.' '/' ':' tanımlayıcının parçası sayılır.
func opTokens(msg string) []string {
	parts := strings.FieldsFunc(msg, func(r rune) bool {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
		switch r {
		case '_', '-', '.', '/', ':':
			return false
		}
		return true
	})
	out := []string{}
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.Trim(p, "-.:/")
		if len([]rune(p)) < opTokenMin || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) == opTokenMax {
			break
		}
	}
	return out
}

// identifierLike — SAF: bulanık eşleşmeye aday mı (alt çizgi / rakam /
// nokta / eğik çizgi ya da iç büyük harf taşıyan belirteç).
func identifierLike(t string) bool {
	if strings.ContainsAny(t, "_./:0123456789") {
		return true
	}
	upper := 0
	for i, r := range t {
		if i > 0 && unicode.IsUpper(r) {
			upper++
		}
	}
	return upper > 0
}

// opMatch — eşleyici sonucu.
type opMatch struct {
	Kind string   // exact | case_insensitive | fuzzy | ""
	Ops  []string // tekil operasyon adları, çağrı sayısına göre
}

// matchOperations — SAF: belirteçler × katalog adayları. İlk dolu aşama kazanır.
func matchOperations(tokens []string, cands []chstore.CatalogOperation) opMatch {
	calls := map[string]uint64{}
	for _, c := range cands {
		calls[c.Operation] += c.CallCount
	}
	stage := func(pred func(tok, op string) bool, fuzzyOnly bool) []string {
		hit := map[string]bool{}
		for _, t := range tokens {
			if fuzzyOnly && !identifierLike(t) {
				continue
			}
			for op := range calls {
				if pred(t, op) {
					hit[op] = true
				}
			}
		}
		out := make([]string, 0, len(hit))
		for op := range hit {
			out = append(out, op)
		}
		sort.Slice(out, func(i, j int) bool {
			if calls[out[i]] != calls[out[j]] {
				return calls[out[i]] > calls[out[j]]
			}
			return out[i] < out[j]
		})
		return out
	}
	if ops := stage(func(t, op string) bool { return t == op }, false); len(ops) > 0 {
		return opMatch{Kind: "exact", Ops: ops}
	}
	if ops := stage(strings.EqualFold, false); len(ops) > 0 {
		return opMatch{Kind: "case_insensitive", Ops: ops}
	}
	if ops := stage(func(t, op string) bool {
		lt, lo := strings.ToLower(t), strings.ToLower(op)
		return strings.HasPrefix(lo, lt) || strings.Contains(lo, lt)
	}, true); len(ops) > 0 {
		return opMatch{Kind: "fuzzy", Ops: ops}
	}
	return opMatch{}
}

// opTarget — tek eşleşen operasyonun okuma hedefi (katalog satırlarından).
type opTarget struct {
	Name     string
	Services []string // span kaynağı servisleri
	Codes    []string // Oracle fonksiyon kodları
	HasSpan  bool
	HasOra   bool
}

// opTargetFor — SAF.
func opTargetFor(name string, cands []chstore.CatalogOperation) opTarget {
	t := opTarget{Name: name}
	seenS, seenC := map[string]bool{}, map[string]bool{}
	for _, c := range cands {
		if c.Operation != name {
			continue
		}
		switch c.Source {
		case "span":
			t.HasSpan = true
			if c.Service != "" && !seenS[c.Service] {
				seenS[c.Service] = true
				t.Services = append(t.Services, c.Service)
			}
		case "oracle":
			t.HasOra = true
			for _, fc := range c.FunctionCodes {
				if fc != "" && !seenC[fc] && len(t.Codes) < 5 {
					seenC[fc] = true
					t.Codes = append(t.Codes, fc)
				}
			}
		}
	}
	return t
}

// opPrefetch — getirilen paket (modele özet + Faz 3 kartı).
type opPrefetch struct {
	Operation  string                     `json:"operation"`
	MatchKind  string                     `json:"matchKind"`
	Services   []string                   `json:"services,omitempty"`
	Codes      []string                   `json:"functionCodes,omitempty"`
	RangeS     int64                      `json:"rangeS"`
	From       time.Time                  `json:"from"`
	To         time.Time                  `json:"to"`
	Current    chstore.OpRED              `json:"current"`
	Previous   chstore.OpRED              `json:"previous"`
	Traces     []opTraceRow               `json:"traces"`
	Oracle     []chstore.OracleErrorCount `json:"oracleErrors"`
	TracesHref string                     `json:"tracesHref"`
	argsJSON   string
	// codeKey — v0.10.1297: operasyon span'lerde yok, köprü fonksiyon kodu
	// (Oracle katalog satırı) → span anahtarının yazımı; "" = span operasyonu
	// ya da kod yok (döngü linkleri yeniden yazılmaz).
	codeKey string
}

type opTraceRow struct {
	TraceID    string  `json:"traceId"`
	Service    string  `json:"service"`
	DurationMs float64 `json:"durationMs"`
	Error      bool    `json:"error"`
}

// seed — serbest döngünün konuşmasına sentetik tool çağrısı + sonucu
// (nil → mesajlar aynen).
func (p *opPrefetch) seed(msgs []copilot.ChatMessage) []copilot.ChatMessage {
	if p == nil {
		return msgs
	}
	out := append([]copilot.ChatMessage{}, msgs...)
	out = append(out,
		copilot.ChatMessage{Role: "assistant", ToolCalls: []copilot.ToolCall{{ID: opPrefetchToolID, Name: opPrefetchTool, Input: json.RawMessage(p.argsJSON)}}},
		copilot.ChatMessage{Role: "user", ToolResults: []copilot.ToolResult{{CallID: opPrefetchToolID, Name: opPrefetchTool, Content: p.summary()}}},
	)
	return out
}

func opPctDelta(cur, prev float64) string {
	if prev == 0 {
		if cur == 0 {
			return "değişim yok"
		}
		return "önceki pencerede yoktu"
	}
	return fmt.Sprintf("%+.0f%%", (cur-prev)/prev*100)
}

// summary — SAF: modele giden kompakt özet (ham trace değil).
func (p *opPrefetch) summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Operasyon %s otomatik getirildi (eşleşme: %s; pencere son %s, %s – %s UTC).\n",
		p.Operation, p.MatchKind, fmtAgoTR(p.RangeS), p.From.UTC().Format("15:04"), p.To.UTC().Format("15:04"))
	if len(p.Services) > 0 {
		fmt.Fprintf(&b, "Servis: %s\n", strings.Join(p.Services, ", "))
	}
	if len(p.Codes) > 0 {
		fmt.Fprintf(&b, "Oracle fonksiyon kodu: %s\n", strings.Join(p.Codes, ", "))
	}
	if p.Current.Measured {
		c, pr := p.Current, p.Previous
		fmt.Fprintf(&b, "Çağrı %d (önceki eşit pencere %d, %s) · hata oranı %%%.2f (önce %%%.2f) · p99 %.0f ms (önce %.0f ms, %s)\n",
			c.Calls, pr.Calls, opPctDelta(float64(c.Calls), float64(pr.Calls)), c.ErrorRate()*100, pr.ErrorRate()*100,
			c.P99Ms, pr.P99Ms, opPctDelta(c.P99Ms, pr.P99Ms))
	} else {
		b.WriteString("RED ölçülemedi (bu kaynak için ön-özet yok).\n")
	}
	errN := 0
	for _, t := range p.Traces {
		if t.Error {
			errN++
		}
	}
	fmt.Fprintf(&b, "Trace: %d (%d hatalı). İlk hatalılar:\n", len(p.Traces), errN)
	shown := 0
	for _, t := range p.Traces {
		if !t.Error || shown == 5 {
			continue
		}
		shown++
		fmt.Fprintf(&b, "- %s · %s · %.0f ms\n", t.TraceID, t.Service, t.DurationMs)
	}
	if len(p.Oracle) > 0 {
		b.WriteString("Bu trace'lere bağlı Oracle teknik hataları (TRACEID):\n")
		for _, o := range p.Oracle {
			fmt.Fprintf(&b, "- %s ×%d: %s\n", o.Code, o.Count, clipBytes(o.Sample, 160))
		}
	} else {
		b.WriteString("Bu trace'lere bağlı Oracle hatası yok.\n")
	}
	return b.String()
}

// opTracesHref — SAF: filtreli Traces sayfası; Traces'in URL sözleşmesi
// (frontend lib/urlState.ts): range=custom:<fromMs>-<toMs>, op=<ad>,
// service=, filters=<JSON FilterExpr dizisi>, hasError.
func opTracesHref(t opTarget, spanKey string, from, to time.Time) string {
	q := url.Values{}
	q.Set("range", fmt.Sprintf("custom:%d-%d", from.UnixMilli(), to.UnixMilli()))
	switch {
	case t.HasSpan:
		q.Set("op", t.Name)
		if len(t.Services) == 1 {
			q.Set("service", t.Services[0])
		}
	case len(t.Codes) > 0 && spanKey != "":
		// v0.10.1297 — TÜM kodlar (tek kod "=", çoğu "IN"); eskiden yalnız ilki.
		f, _ := json.Marshal([]chstore.FilterExpr{mcptools.OracleCodeFilter(spanKey, t.Codes)}) // {"k","op","v"} — FE FilterExpr
		q.Set("filters", string(f))
	}
	return "/traces?" + q.Encode()
}

// rewriteSearchHref — v0.10.1297: ön-getirme operasyonu fonksiyon koduyla
// çözdüyse, döngünün bu operasyonun ADIYLA kurduğu /traces?search=<ad>
// linkleri (build_link çipi, search_traces deep_link'i → answer.open) kod
// süzgecine çevrilir. nil / span operasyonu → href aynen.
func (p *opPrefetch) rewriteSearchHref(href string) string {
	if p == nil || p.codeKey == "" || len(p.Codes) == 0 {
		return href
	}
	return opSearchHrefToCodeFilter(href, p.Operation, p.codeKey, p.Codes)
}

// opSearchHrefToCodeFilter — SAF (tablo testli): /traces?…search=<op>… →
// search düşer, filters'a kod süzgeci eklenir (aynı anahtarda süzgeç zaten
// varsa eklenmez); diğer paramlar (range, service, sort…) korunur. search
// operasyon adı değilse, href /traces değilse ya da filters çözülemiyorsa
// href aynen döner.
func opSearchHrefToCodeFilter(href, op, key string, codes []string) string {
	if !strings.HasPrefix(href, "/traces?") || strings.TrimSpace(op) == "" || key == "" || len(codes) == 0 {
		return href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	q := u.Query()
	if !strings.EqualFold(strings.TrimSpace(q.Get("search")), strings.TrimSpace(op)) {
		return href
	}
	var fs []chstore.FilterExpr
	if raw := q.Get("filters"); raw != "" {
		if json.Unmarshal([]byte(raw), &fs) != nil {
			return href
		}
	}
	has := false
	for _, f := range fs {
		if strings.EqualFold(f.Key, key) {
			has = true
		}
	}
	if !has {
		fs = append(fs, mcptools.OracleCodeFilter(key, codes))
	}
	b, _ := json.Marshal(fs)
	q.Del("search")
	q.Set("filters", string(b))
	return "/traces?" + q.Encode()
}

// mergeErrorsFirst — SAF: tekilleştirilmiş birleşim; önce hatalılar, sonra süre.
func mergeErrorsFirst(sets [][]chstore.TraceRow, limit int) []chstore.TraceRow {
	seen := map[string]bool{}
	var out []chstore.TraceRow
	for _, rows := range sets {
		for _, r := range rows {
			if !seen[r.TraceID] {
				seen[r.TraceID] = true
				out = append(out, r)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HasError != out[j].HasError {
			return out[i].HasError
		}
		return out[i].DurationMs > out[j].DurationMs
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// chatOperationPrefetch — copilotChat'in guided'dan önceki adımı.
// handled=true → seçim çipleri yayınlandı, tur bitti.
func (s *Server) chatOperationPrefetch(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, rangeS int64, anchor time.Time) (*opPrefetch, bool) {
	if s == nil || s.store == nil {
		return nil, false
	}
	toks := opTokens(lastUserText(msgs))
	if len(toks) == 0 {
		return nil, false
	}
	pctx, cancel := context.WithTimeout(ctx, opPrefetchTimeout)
	defer cancel()
	cands, err := s.store.CatalogCandidates(pctx, toks, opCandidateLimit)
	if err != nil || len(cands) == 0 {
		return nil, false
	}
	m := matchOperations(toks, cands)
	switch {
	case len(m.Ops) == 0:
		return nil, false
	case len(m.Ops) > 1:
		ops := m.Ops
		if len(ops) > opChoiceMax {
			ops = ops[:opChoiceMax]
		}
		sugg := make([]string, len(ops))
		for i, op := range ops {
			sugg[i] = op + " operasyonuna bak"
		}
		emit("answer", map[string]any{
			"text":        fmt.Sprintf("Mesajınız %d operasyonla eşleşti; hangisine bakayım?", len(m.Ops)),
			"exchangeId":  copilot.MetaFromContext(ctx).ExchangeID,
			"suggestions": sugg,
		})
		return nil, true
	}
	if rangeS <= 0 {
		rangeS = opDefaultRangeS
	}
	to := anchor
	if to.IsZero() {
		to = time.Now()
	}
	from := to.Add(-time.Duration(rangeS) * time.Second)
	prevFrom := from.Add(-time.Duration(rangeS) * time.Second)
	t := opTargetFor(m.Ops[0], cands)
	sc := s.oracleOpScope()
	p := &opPrefetch{Operation: t.Name, MatchKind: m.Kind, Services: t.Services, Codes: t.Codes, RangeS: rangeS, From: from, To: to}
	args, _ := json.Marshal(map[string]any{"operation": t.Name, "match": m.Kind, "range_s": rangeS})
	p.argsJSON = string(args)
	n := emitGuidedStep(emit, opPrefetchTool, p.argsJSON)

	svc := ""
	if len(t.Services) == 1 {
		svc = t.Services[0]
	}
	switch {
	case t.HasSpan:
		p.Current, _ = s.store.SpanOperationRED(pctx, svc, t.Name, from, to)
		p.Previous, _ = s.store.SpanOperationRED(pctx, svc, t.Name, prevFrom, from)
	case len(t.Codes) > 0:
		p.Current, _ = s.store.FunctionCodeRED(pctx, t.Codes, from, to)
		p.Previous, _ = s.store.FunctionCodeRED(pctx, t.Codes, prevFrom, from)
	}
	var filters [][]chstore.FilterExpr
	if t.HasSpan {
		filters = append(filters, []chstore.FilterExpr{{Key: "name", Op: "=", Values: []string{t.Name}}})
	} else if sc.SpanKey != "" {
		for _, c := range t.Codes {
			filters = append(filters, []chstore.FilterExpr{{Key: sc.SpanKey, Op: "=", Values: []string{c}}})
		}
	}
	var sets [][]chstore.TraceRow
	for _, errorsOnly := range []bool{true, false} { // önce hatalılar
		for _, fx := range filters {
			f := traceSearchFilter(svc, "", "", false, from, to)
			f.Filters, f.HasError, f.Limit = fx, errorsOnly, opTraceLimit
			if rows, _, _, terr := s.store.GetTraces(pctx, f); terr == nil {
				sets = append(sets, rows)
			}
		}
	}
	ids := []string{}
	for _, r := range mergeErrorsFirst(sets, opTraceLimit) {
		p.Traces = append(p.Traces, opTraceRow{TraceID: r.TraceID, Service: r.ServiceName, DurationMs: r.DurationMs, Error: r.HasError})
		ids = append(ids, r.TraceID)
	}
	if sc.Enabled {
		p.Oracle, _ = s.store.OracleErrorsForTraces(pctx, ids, from, to)
	}
	p.TracesHref = opTracesHref(t, sc.SpanKey, from, to)
	if !t.HasSpan && len(t.Codes) > 0 && sc.SpanKey != "" {
		p.codeKey = sc.SpanKey // v0.10.1297 — döngü linkleri kod süzgecine çevrilir
	}
	emitGuidedStepResult(emit, n, opPrefetchTool, p.summary(), nil)
	// Faz 3 — sohbet kartı (metrikler, hatalı trace'ler, Oracle kodları,
	// filtreli Traces linki). seq yüksek: döngünün kendi blokları (b1…) önce.
	emit("block", map[string]any{"id": "op-prefetch", "type": blocks.TypeOperation, "seq": 1000, "final": true, "payload": p})
	return p, false
}
