package api

// ai_subject.go — v0.10.1405 (Tek CoSRE adım 2). Operatör:
//   "Trace ve exception üzerinden çalışan Ask CoSRE açıklamaları güzel ama
//    sanki ayrı drawer hissi, CoSRE bütünselliğine uymuyor."
//
// `?ai=` öznesi kodeğinin (frontend/src/lib/aiSubject.ts formatAiParam /
// parseAiParam) TAM sunucu ikizi. v0.9.482'den beri çekmece kanıtı için
// yalnız trace/span/exception/endpoint çözülüyordu (parseDrawerSubject);
// açıklama turu artık span / service-health / log öznelerini de taşıdığı
// için kodek tek yerde, tüm türlerle. Çekmece kademesi (parseDrawerSubject)
// ve açıklama kaydı (chat_explain_registry.go) ikisi de buradan okur.
//
// Katılık aiSubject.ts ile aynı: kimlik encodeURIComponent'li (çözümde '+'
// boşluk DEĞİL — decodeURIComponent gibi PathUnescape; geçersiz UTF-8 red),
// basit özne TAM 2 parça, span TAM 3, service-health 4, charts 5, endpoint
// 5|6, log 6, log-spike 7|8; pencereler pozitif ve ARTAN. Kısa (SELF) biçim
// (`?ai=trace`) yalnız adres çubuğunda yaşar — sunucuya hep kanonik biçim gelir.

import (
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// aiSubject — `?ai=` öznesi (tüm türler). Türe özgü alanlar yalnız o türde dolu.
type aiSubject struct {
	Kind   string
	ID     string
	SpanID string // span
	// v0.10.1367 — endpoint: rota + beyaz listeli fiil ("" = yok).
	Route  string
	Method string
	// service-health / charts / log-spike / endpoint penceresi (ns → time.Time).
	From time.Time
	To   time.Time
	// charts
	Scope string
	// log-spike
	Mode, Env, Q, Level string
	// log — tsNs ONDALIK METİN (2^53 sınırı; kodekte basamaklar aynen).
	TsNs, Pod, TraceID, Hash string
	// v0.10.1435 — metric_panel: ID = panel KONUMU (d.<pano>.<panel> | x.<harf>
	// | m); brush (sıfır = yok), ≤3 seçili seri adı, önceki pencereyle kıyas.
	BrushFrom, BrushTo time.Time
	Series             []string
	Cmp                bool
	// Panel — v0.10.1435: niyetin yapısal yükü (chatExplainAsk.Panel). KODEKTE
	// YOK: yalnız kayıt turu (chatExplainRegistryTurn) niyetten doldurur.
	Panel *metricPanelAsk
	// Loc — v0.10.1435: operatörün saat dilimi (kodekte YOK; kayıt turu
	// isteğin tz'sinden doldurur — ölçüm saatleri grafikteki yerel saatle aynı).
	Loc *time.Location
}

// drawerSubject — çekmece kanıt kademesinin adı (v0.9.482); aynı tip.
type drawerSubject = aiSubject

// aiSubjectKinds — aiSubject.ts AI_KINDS.
var aiSubjectKinds = map[string]bool{
	"trace": true, "span": true, "problem": true, "incident": true, "anomaly": true,
	"service-health": true, "runbook": true, "exception": true, "charts": true,
	"log-spike": true, "log": true, "endpoint": true,
	"metric_panel": true, // v0.10.1435
}

// logSpikeQueryMax — aiSubject.ts LOG_SPIKE_QUERY_MAX.
const logSpikeQueryMax = 200

// decodeAiPart — decodeURIComponent ikizi: bozuk yüzde dizisi / geçersiz UTF-8 → ok=false.
func decodeAiPart(s string) (string, bool) {
	v, err := url.PathUnescape(s)
	if err != nil || !utf8.ValidString(v) {
		return "", false
	}
	return v, true
}

// parseAiWindow — SAF: pozitif, ARTAN ns penceresi.
func parseAiWindow(fromS, toS string) (time.Time, time.Time, bool) {
	from, ferr := strconv.ParseInt(fromS, 10, 64)
	to, terr := strconv.ParseInt(toS, 10, 64)
	if ferr != nil || terr != nil || from <= 0 || to <= from {
		return time.Time{}, time.Time{}, false
	}
	return time.Unix(0, from), time.Unix(0, to), true
}

// parseAiSubject — SAF: kanonik `?ai=` kodeği → özne. aiSubject.ts
// parseAiParam'ın (sayfa parametresiz) birebir ikizi; tablo-testli
// (ai_subject_test.go, aiSubject.test.ts'in aynası).
func parseAiSubject(raw string) (aiSubject, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return aiSubject{}, false
	}
	parts := strings.Split(raw, ":")
	kind := parts[0]
	if !aiSubjectKinds[kind] || len(parts) < 2 {
		return aiSubject{}, false
	}
	switch kind {
	case "log-spike":
		return parseAiLogSpike(parts)
	case "log":
		return parseAiLogLine(parts)
	case "endpoint":
		return parseAiEndpoint(parts)
	case "metric_panel": // v0.10.1435
		return parseAiMetricPanel(parts)
	}
	id, ok := decodeAiPart(parts[1])
	if !ok || id == "" {
		return aiSubject{}, false
	}
	switch kind {
	case "span":
		if len(parts) != 3 {
			return aiSubject{}, false
		}
		sp, ok := decodeAiPart(parts[2])
		if !ok || sp == "" {
			return aiSubject{}, false
		}
		return aiSubject{Kind: kind, ID: id, SpanID: sp}, true
	case "service-health", "charts":
		want := 4
		if kind == "charts" {
			want = 5
		}
		if len(parts) != want {
			return aiSubject{}, false
		}
		from, to, ok := parseAiWindow(parts[2], parts[3])
		if !ok {
			return aiSubject{}, false
		}
		s := aiSubject{Kind: kind, ID: id, From: from, To: to}
		if kind == "charts" {
			s.Scope = normalizeChartScope(parts[4]) // parse GEVŞEK: bilinmeyen kapsam → all
		}
		return s, true
	}
	if len(parts) != 2 {
		return aiSubject{}, false
	}
	return aiSubject{Kind: kind, ID: id}, true
}

// parseAiLogSpike — aiSubject.ts parseLogSpike: 7|8 parça, kip sel|auto,
// bilinmeyen seviye → all, arama ≤200.
func parseAiLogSpike(parts []string) (aiSubject, bool) {
	if len(parts) != 7 && len(parts) != 8 {
		return aiSubject{}, false
	}
	id := ""
	if parts[1] != "*" {
		v, ok := decodeAiPart(parts[1])
		if !ok || v == "" {
			return aiSubject{}, false
		}
		id = v
	}
	from, to, ok := parseAiWindow(parts[2], parts[3])
	if !ok || (parts[4] != "sel" && parts[4] != "auto") {
		return aiSubject{}, false
	}
	env, _ := decodeAiPart(parts[5])
	q, _ := decodeAiPart(parts[6])
	if r := []rune(q); len(r) > logSpikeQueryMax {
		q = string(r[:logSpikeQueryMax])
	}
	level := "all"
	if len(parts) == 8 && parts[7] == "error" {
		level = "error"
	}
	return aiSubject{Kind: "log-spike", ID: id, From: from, To: to, Mode: parts[4], Env: env, Q: q, Level: level}, true
}

// parseAiLogLine — aiSubject.ts parseLogLine: TAM 6 parça; zaman pozitif
// ondalık (≤20 basamak), servis zorunlu, trace boş ya da 32-hex, özet 8-hex.
func parseAiLogLine(parts []string) (aiSubject, bool) {
	if len(parts) != 6 {
		return aiSubject{}, false
	}
	ts, rawSvc, rawPod, traceID, hash := parts[1], parts[2], parts[3], parts[4], parts[5]
	if !isLogTsNs(ts) || !isLowerHexN(hash, 8) || (traceID != "" && !isLowerHexN(traceID, 32)) {
		return aiSubject{}, false
	}
	svc, ok := decodeAiPart(rawSvc)
	if !ok || svc == "" {
		return aiSubject{}, false
	}
	pod := ""
	if rawPod != "" {
		if pod, ok = decodeAiPart(rawPod); !ok || pod == "" {
			return aiSubject{}, false
		}
	}
	return aiSubject{Kind: "log", ID: svc, TsNs: ts, Pod: pod, TraceID: traceID, Hash: hash}, true
}

// parseAiEndpoint — v0.10.1367: `endpoint:enc(service):enc(route):fromNs:toNs[:METHOD]`.
// TAM 5 ya da 6 parça, servis ve rota boş olamaz, pencere ARTAN; bilinmeyen
// fiil özneyi reddettirmez, DÜŞER (fiilsiz endpoint).
func parseAiEndpoint(parts []string) (aiSubject, bool) {
	if len(parts) != 5 && len(parts) != 6 {
		return aiSubject{}, false
	}
	svc, ok := decodeAiPart(parts[1])
	if !ok || svc == "" {
		return aiSubject{}, false
	}
	route, ok := decodeAiPart(parts[2])
	if !ok || route == "" {
		return aiSubject{}, false
	}
	from, to, ok := parseAiWindow(parts[3], parts[4])
	if !ok {
		return aiSubject{}, false
	}
	method := ""
	if len(parts) == 6 && isHTTPVerb(parts[5]) {
		method = strings.ToUpper(parts[5])
	}
	return aiSubject{Kind: "endpoint", ID: svc, Route: route, Method: method, From: from, To: to}, true
}

// formatAiSubject — SAF: aiSubject.ts formatAiParam ikizi (kanonik biçim).
func formatAiSubject(s aiSubject) string {
	enc := mcptools.EncodeURIComponent
	head := s.Kind + ":" + enc(s.ID)
	ns := func(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }
	switch s.Kind {
	case "span":
		return head + ":" + enc(s.SpanID)
	case "service-health":
		return head + ":" + ns(s.From) + ":" + ns(s.To)
	case "charts":
		return head + ":" + ns(s.From) + ":" + ns(s.To) + ":" + normalizeChartScope(s.Scope)
	case "endpoint":
		out := head + ":" + enc(s.Route) + ":" + ns(s.From) + ":" + ns(s.To)
		if isHTTPVerb(s.Method) {
			out += ":" + strings.ToUpper(s.Method)
		}
		return out
	case "log":
		return "log:" + s.TsNs + ":" + enc(s.ID) + ":" + enc(s.Pod) + ":" + s.TraceID + ":" + s.Hash
	case "log-spike":
		id := "*"
		if s.ID != "" {
			id = enc(s.ID)
		}
		q := s.Q
		if r := []rune(q); len(r) > logSpikeQueryMax {
			q = string(r[:logSpikeQueryMax])
		}
		level := "all"
		if s.Level == "error" {
			level = "error"
		}
		return "log-spike:" + id + ":" + ns(s.From) + ":" + ns(s.To) + ":" + s.Mode + ":" + enc(s.Env) + ":" + enc(q) + ":" + level
	case "metric_panel": // v0.10.1435
		return formatAiMetricPanel(s)
	}
	return head
}

// metricPanelSeriesRunes — v0.10.1435: kodekteki seçili seri adının tavanı
// (rune; aiSubject.ts METRIC_PANEL_SERIES_MAX). Biçimleyici kırpar, ayrıştırıcı
// fazlasını reddeder.
const metricPanelSeriesRunes = 80

// metricPanelSelectedMax — kodekteki seçili seri tavanı.
const metricPanelSelectedMax = 3

// parseAiMetricPanel — v0.10.1435: aiSubject.ts parseMetricPanel ikizi.
// `metric_panel:<konum>:<fromNs>:<toNs>:<bFromNs|0>:<bToNs|0>:<enc(seri),…>[:cmp]`
// TAM 7|8 parça; konum biçimli (parseMetricPanelLoc), pencere ARTAN, brush ya
// 0:0 ya da pencerenin İÇİNDE artan; seri ≤3, her biri 1..80 rune, kontrol
// karakteri yok; 8. parça yalnız "cmp".
func parseAiMetricPanel(parts []string) (aiSubject, bool) {
	if len(parts) != 7 && len(parts) != 8 {
		return aiSubject{}, false
	}
	loc, ok := decodeAiPart(parts[1])
	if !ok {
		return aiSubject{}, false
	}
	if _, ok := parseMetricPanelLoc(loc); !ok {
		return aiSubject{}, false
	}
	from, to, ok := parseAiWindow(parts[2], parts[3])
	if !ok {
		return aiSubject{}, false
	}
	s := aiSubject{Kind: "metric_panel", ID: loc, From: from, To: to}
	if parts[4] != "0" || parts[5] != "0" {
		bf, bt, ok := parseAiWindow(parts[4], parts[5])
		if !ok || bf.Before(from) || bt.After(to) {
			return aiSubject{}, false
		}
		s.BrushFrom, s.BrushTo = bf, bt
	}
	if parts[6] != "" {
		items := strings.Split(parts[6], ",")
		if len(items) > metricPanelSelectedMax {
			return aiSubject{}, false
		}
		for _, it := range items {
			v, ok := decodeAiPart(it)
			if !ok || v == "" || utf8.RuneCountInString(v) > metricPanelSeriesRunes || hasControlRune(v) {
				return aiSubject{}, false
			}
			s.Series = append(s.Series, v)
		}
	}
	if len(parts) == 8 {
		if parts[7] != "cmp" {
			return aiSubject{}, false
		}
		s.Cmp = true
	}
	return s, true
}

// formatAiMetricPanel — v0.10.1435: aiSubject.ts formatMetricPanel ikizi.
// Seri adı 80 rune'a kırpılır; kodek 400 rune'u aşarsa sondaki seriler düşer
// (konum + pencere her zaman sığar).
func formatAiMetricPanel(s aiSubject) string {
	enc := mcptools.EncodeURIComponent
	ns := func(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }
	brush := "0:0"
	if !s.BrushFrom.IsZero() && !s.BrushTo.IsZero() {
		brush = ns(s.BrushFrom) + ":" + ns(s.BrushTo)
	}
	var items []string
	for _, v := range s.Series {
		if len(items) >= metricPanelSelectedMax {
			break
		}
		if r := []rune(v); len(r) > metricPanelSeriesRunes {
			v = string(r[:metricPanelSeriesRunes])
		}
		if v != "" {
			items = append(items, enc(v))
		}
	}
	tail := ""
	if s.Cmp {
		tail = ":cmp"
	}
	head := "metric_panel:" + enc(s.ID) + ":" + ns(s.From) + ":" + ns(s.To) + ":" + brush + ":"
	for len(items) > 0 && len(head)+len(strings.Join(items, ","))+len(tail) > chatExplainSubjectMax {
		items = items[:len(items)-1]
	}
	return head + strings.Join(items, ",") + tail
}

// hasControlRune — SAF: kontrol karakteri var mı.
func hasControlRune(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func isLogTsNs(s string) bool {
	if len(s) == 0 || len(s) > 20 || s[0] < '1' || s[0] > '9' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isLowerHexN(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}
