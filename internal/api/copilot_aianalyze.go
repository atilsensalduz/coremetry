package api

// copilot_aianalyze.go — servis bağlamı (RED + baseline + en sık hatalar +
// deploy + komşular) toplayıcısı ve Türkçe özet çizicisi: buildServiceContext
// / renderServiceSnapshot (guided sohbet, servis raporu, bubble-up) ve
// buildOperationContext / renderOperationSnapshot (guided operasyon dalı).
// Sunucu her şeyi Go'da toplar; modele yalnız küçük, temiz bir ÖZET gider —
// ham span/log asla.
//
// v0.10.1408 — POST /api/copilot/analyze-service ("CoSRE Analizi — ✨ AI ile
// analiz et", Service Overview'un alt bölümü) kalktı (operatör: "alttaki
// servisi CoSRE analiz et kaldırabiliriz"); uç, verdict tipi, ayrıştırıcı,
// post-check ve prompt'u (copilot.systemServiceAnalysis) onunla gitti.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// ── Context (the summary the model sees) ─────────────────────────────────────

type aiRED struct {
	Spans      uint64  `json:"spans"`
	Rate       float64 `json:"rate"` // v0.10.944 — span/s (tüm span türleri, service_summary_5m); istek hızı değil
	ErrorRate  float64 `json:"errorRate"`
	ErrorCount uint64  `json:"errorCount"`
	AvgMs      float64 `json:"avgMs"`
	P50Ms      float64 `json:"p50Ms"`
	P95Ms      float64 `json:"p95Ms"`
	P99Ms      float64 `json:"p99Ms"`
}

type aiErrCount struct {
	Type          string `json:"type"`
	Message       string `json:"message"`
	Service       string `json:"service"`
	Count         uint64 `json:"count"`
	SampleTraceID string `json:"sampleTraceId"`
}

type aiDeploy struct {
	Version    string `json:"version"`
	TimeUnixNs int64  `json:"timeUnixNs"`
}

type aiServiceContext struct {
	Service    string       `json:"service"`
	RangeS     int64        `json:"rangeS"`
	Current    aiRED        `json:"current"`
	Baseline   aiRED        `json:"baseline"`
	TopErrors  []aiErrCount `json:"topErrors"`
	Deploys    []aiDeploy   `json:"deploys"`
	Upstream   []string     `json:"upstream"`
	Downstream []string     `json:"downstream"`
	// v0.9.580 (operatör: "örnek request_id, CHANNEL_CODE değerlerini
	// de söylesin") — cevabı EYLEME dönüştüren somut kimlikler.
	// "Hata oranı %14" bir gözlem; "örnek request_id: 8f3c…, en çok
	// hata üreten CHANNEL_CODE: 0012" bir başlangıç noktası: operatör
	// onu alıp kendi log'una, kaydına, çağrı merkezine gider.
	Business    map[string][]chstore.BusinessSlice `json:"business,omitempty"`
	Correlation []chstore.CorrelationSample        `json:"correlation,omitempty"`

	// v0.10.944 — pencere RED okumalarının hatası (dışa aktarılmaz: JSON
	// sözleşmesi ve lib/types.ts değişmez). Zaman aşımı ya da erişilemeyen
	// okuma artık Current/Baseline'ı sessizce sıfır bırakıp "veri yok"
	// diye sunulmaz; renderServiceSnapshot ve guided adımları bunu okur.
	curErr  error
	baseErr error

	// v0.10.1410 — komşu toplamları (Upstream/Downstream ilk
	// serviceContextNeighbourCap ile kesilir; kalan "+N daha" diye söylenir).
	upTotal, downTotal int
}

// serviceContextNeighbourCap — v0.10.1410: servis bağlamında yön başına
// komşu tavanı (prompt bütçesi). Eski ham-span örneklemi (50 trace) fiilen
// bu mertebede komşu görüyordu; özetten okunca hub servislerde yüzlerce
// komşu olabilir.
const serviceContextNeighbourCap = 20

// buildServiceContext gathers + SUMMARISES the per-service signals. All numbers
// are aggregates; no raw spans/logs leave the server.
func (s *Server) buildServiceContext(ctx context.Context, service string, from, to time.Time) *aiServiceContext {
	span := to.Sub(from)
	// v0.9.597 — dizi alanları BOŞ DİLİM olarak ilklenir, nil olarak DEĞİL.
	//
	// Go'da nil dilim JSON'a `null` serileşir. TS tipi ise
	// `deploys: AiDeploy[]` diyor — yani NULLABLE DEĞİL. Sözleşme
	// yalandı ve yalan sessiz değildi: ContextView `ctx.deploys.length`
	// okuyor, null'da TypeError atıyor ve route ErrorBoundary'si TÜM
	// Servis Overview sayfasını hata ekranıyla değiştiriyordu — yalnız
	// AI kartını değil.
	//
	// Tetikleyici NADİR DEĞİL, NORMAL: penceresinde deploy olmayan bir
	// servis. 30 dakikalık varsayılan pencerede servislerin çoğu böyle.
	// Aynı hat hatasız serviste topErrors, izole serviste upstream/
	// downstream için de geçerliydi.
	//
	// omitempty ÇÖZÜM DEĞİL: alanı tamamen düşürür, TS'te `undefined`
	// olur ve `.length` yine patlar — daha sinsi bir biçimde.
	cx := &aiServiceContext{
		Service: service, RangeS: int64(span.Seconds()),
		TopErrors: []aiErrCount{}, Deploys: []aiDeploy{},
		Upstream: []string{}, Downstream: []string{},
	}

	// RED — current window + the immediately-preceding baseline window.
	// v0.10.944 — TEK pencere okuması (tdigest durumlarının birleşimi); eskisi
	// 5 dk kovaları çekip yüzdeliklerini span ağırlıklı ORTALIYORDU (aggRED).
	// v0.10.944 (inceleme) — hata artık atılmıyor: ServiceWindowRED 15 s
	// max_execution_time taşıyor, zaman aşımı yolu gerçek; atılan hata
	// Current/Baseline'ı sıfır bırakıp prompt'ta "veri yok" diye sunuluyordu.
	if w, err := s.store.ServiceWindowRED(ctx, service, from, to); err == nil {
		cx.Current = windowRED(w, span.Seconds())
	} else {
		cx.curErr = err
	}
	// v0.10.1028 — baseline penceresi chstore.PriorWindow'dan. ServiceWindowRED
	// alt sınırı 5 dk kovaya indiriyor (alignBucketStart); eski `[from − span,
	// from)` hizasız from'da current'ın ilk kovasını baseline'a da sayıyordu
	// (from=10:03 → 10:00 kovası iki pencerede) ve modele giden "önceki
	// pencere" kıyası şimdinin trafiğiyle sulanıyordu. Rate paydası iki
	// tarafta da span kaldı: iki pencere aynı sayıda kova okur, oran
	// sayaç oranına eşit kalır.
	bFrom, bTo := chstore.PriorWindow(from, to)
	if w, err := s.store.ServiceWindowRED(ctx, service, bFrom, bTo); err == nil {
		cx.Baseline = windowRED(w, span.Seconds())
	} else {
		cx.baseErr = err
	}

	// v0.9.580 — iş boyutu kırılımı + örnek istek kimlikleri. İkisi de
	// soft-fail: eksik olmaları cevabı engellemez, yalnız somutluğunu
	// azaltır (aynı desen: RED, deploys, topology).
	for _, key := range []string{"CHANNEL_CODE", "FUNCTION_CODE"} {
		if sl, err := s.store.BusinessBreakdown(ctx, service, key, from, to); err == nil && len(sl) > 0 {
			if cx.Business == nil {
				cx.Business = map[string][]chstore.BusinessSlice{}
			}
			cx.Business[key] = sl
		}
	}
	if cs, err := s.store.SampleCorrelationIDs(ctx, service, from, to); err == nil {
		cx.Correlation = cs
	}

	// Top error messages (most-frequent first, top 5).
	if errs, err := s.store.GetExceptions(ctx, chstore.ExceptionFilter{
		Service: service, GroupBy: "full", From: from, To: to, Limit: 50,
	}); err == nil {
		sort.Slice(errs, func(i, j int) bool { return errs[i].Count > errs[j].Count })
		for i, e := range errs {
			if i >= 5 {
				break
			}
			cx.TopErrors = append(cx.TopErrors, aiErrCount{
				Type: e.Type, Message: e.Message, Service: e.Service, Count: e.Count, SampleTraceID: e.SampleTraceID,
			})
		}
	}

	// Deploy markers in window.
	if deps, err := s.store.GetServiceDeploys(ctx, service, from, to); err == nil {
		for _, d := range deps {
			cx.Deploys = append(cx.Deploys, aiDeploy{Version: d.Version, TimeUnixNs: d.TimeUnixNs})
		}
	}

	// Dependency chain — 1-hop upstream callers + downstream callees.
	// v0.10.1410 (operatör: "Downstream düzgün çalışmıyor") — topoloji
	// özetinden (serviceNeighborsRollup, Topology sekmesinin okuması), AYNI
	// pencere: DB / kuyruk / dış HTTP çağrıları artık downstream'de. Eski ham
	// span yürüyüşü yalnız enstrümante çocuk servisleri görüyordu.
	if up, down, err := s.serviceNeighborsRollup(ctx, service, from, to); err == nil {
		setServiceContextNeighbours(cx, up, down)
	}
	return cx
}

// setServiceContextNeighbours — SAF (tablo testli): katlanmış komşuları
// bağlamın etiket dizilerine yazar (calls DESC, yön başına
// serviceContextNeighbourCap; toplam ayrıca tutulur).
func setServiceContextNeighbours(cx *aiServiceContext, up, down []serviceNeighbor) {
	cx.upTotal, cx.downTotal = len(up), len(down)
	for i, n := range up {
		if i >= serviceContextNeighbourCap {
			break
		}
		cx.Upstream = append(cx.Upstream, neighbourEvidenceLabel(n, true))
	}
	for i, n := range down {
		if i >= serviceContextNeighbourCap {
			break
		}
		cx.Downstream = append(cx.Downstream, neighbourEvidenceLabel(n, true))
	}
}

// neighbourListTR — "a, b, c" + kesildiyse " (+N daha)".
func neighbourListTR(names []string, total int) string {
	out := strings.Join(names, ", ")
	if total > len(names) {
		out += fmt.Sprintf(" (+%d daha)", total-len(names))
	}
	return out
}

// windowRED — v0.10.944: tek pencerenin RED özeti (chstore.ServiceWindowRED)
// → aiRED. Yüzdelikler OLDUĞU GİBİ geçer: service_summary_5m kovalarının
// tdigest durumları SQL'de birleştirildi (quantilesTDigestMerge), yani değer
// pencerenin TAMAMININ yüzdeliğidir. Eski aggRED kova yüzdeliklerinin span
// ağırlıklı ortalamasını alıyordu — bir kovanın p99'u ile diğerinin p99'unun
// ortalaması hiçbir popülasyonun p99'u değildir (kısa bir patlamayı sulandırır)
// ve bu sayı AI prompt'larına "p99" diye giriyordu. Popülasyon MV'nin kendisi:
// servisin TÜM span'leri (kind ayrımı yok) — Rate de span/s'dir.
func windowRED(w chstore.ServiceWindowRED, windowSec float64) aiRED {
	red := aiRED{Spans: w.Spans, ErrorCount: w.Errors}
	if w.Spans > 0 {
		red.ErrorRate = float64(w.Errors) / float64(w.Spans) * 100
		red.AvgMs, red.P50Ms, red.P95Ms, red.P99Ms = w.AvgMs, w.P50Ms, w.P95Ms, w.P99Ms
	}
	if windowSec > 0 {
		red.Rate = float64(w.Spans) / windowSec
	}
	return red
}

// renderServiceSnapshot formats the context as the compact Turkish summary the
// model reasons over (human-readable; field order + labels affect the output).
func renderServiceSnapshot(cx *aiServiceContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Servis: %s (son %d dakika)\n", cx.Service, cx.RangeS/60)
	c := cx.Current
	// v0.10.944 — okuma hatası "veri yok" DEĞİL: sınıfı (timeout/unreachable…)
	// söylenir ve model sayı uydurmaz.
	if cx.curErr != nil {
		fmt.Fprintf(&b, "RED: mevcut pencere OKUNAMADI (%s) — veri yok DEĞİL; sayı uydurma.\n", sourcestate.Classify(cx.curErr))
	} else {
		// v0.10.944 — birim span/s: değer tüm span türleri üzerinden (guided
		// window_compare ile aynı etiket); "req/s" istek hızını abartıyordu.
		fmt.Fprintf(&b, "RED: rate=%.1f span/s (tüm span türleri), error=%.2f%% (%d hata), p50=%.0fms, p95=%.0fms, p99=%.0fms\n",
			c.Rate, c.ErrorRate, c.ErrorCount, c.P50Ms, c.P95Ms, c.P99Ms)
	}
	bl := cx.Baseline
	switch {
	case cx.baseErr != nil:
		fmt.Fprintf(&b, "Baseline: önceki pencere OKUNAMADI (%s) — 'veri yok' değil; baseline hakkında sonuç çıkarma.\n", sourcestate.Classify(cx.baseErr))
	case bl.Spans > 0:
		fmt.Fprintf(&b, "Baseline (önceki %d dk): error=%.2f%%, p99=%.0fms, p50=%.0fms\n",
			cx.RangeS/60, bl.ErrorRate, bl.P99Ms, bl.P50Ms)
	default:
		b.WriteString("Baseline: önceki pencerede veri yok.\n")
	}
	if len(cx.TopErrors) > 0 {
		b.WriteString("En sık hatalar: ")
		var parts []string
		for _, e := range cx.TopErrors {
			label := e.Type
			if label == "" {
				label = e.Message
			}
			parts = append(parts, fmt.Sprintf("%s ×%d", label, e.Count))
		}
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("\n")
	}
	// v0.9.580 — somut kimlikler. Model bunları YORUMLAR, üretmez:
	// değerler doğrudan ClickHouse'tan geliyor ve prompt'a olduğu gibi
	// basılıyor, yani uydurulamaz.
	for _, key := range []string{"CHANNEL_CODE", "FUNCTION_CODE"} {
		sl := cx.Business[key]
		if len(sl) == 0 {
			continue
		}
		var parts []string
		for i, v := range sl {
			if i >= 5 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s (%d çağrı, %d hata, %%%.1f)",
				v.Value, v.Calls, v.Errors, v.ErrPct))
		}
		fmt.Fprintf(&b, "%s kırılımı (en çok hata üreten önce): %s\n", key, strings.Join(parts, ", "))
	}
	for _, c := range cx.Correlation {
		fmt.Fprintf(&b, "Örnek %s: %s\n", c.Key, strings.Join(c.Values, ", "))
	}
	if len(cx.Deploys) > 0 {
		var parts []string
		for _, d := range cx.Deploys {
			parts = append(parts, d.Version)
		}
		fmt.Fprintf(&b, "Deploy(lar): %s\n", strings.Join(parts, ", "))
	}
	if len(cx.Upstream) > 0 {
		fmt.Fprintf(&b, "Upstream (çağıranlar): %s\n", neighbourListTR(cx.Upstream, cx.upTotal))
	}
	if len(cx.Downstream) > 0 {
		fmt.Fprintf(&b, "Downstream (bağımlılıklar): %s\n", neighbourListTR(cx.Downstream, cx.downTotal))
	}
	return b.String()
}

// ── Operation-level context (v0.9.184) — the operasyon twin of the
// service context. CoSRE answers "GET /orders nasıl" / "bu operasyonun
// durumu" by scoping RED to a single span name. All numbers come from
// operation_summary_5m via ONE GetOperationSummaryCompared call, which
// carries both the current window AND the prior-equal-window baseline
// (Prior* fields) — so unlike buildServiceContext we don't run two
// aggregate reads. No raw spans leave the server. ───────────────────

type aiOperationContext struct {
	Service     string     `json:"service"`
	Operation   string     `json:"operation"`
	RangeS      int64      `json:"rangeS"`
	Current     aiRED      `json:"current"`
	Baseline    aiRED      `json:"baseline"`
	HasBaseline bool       `json:"hasBaseline"`
	Apdex       float64    `json:"apdex"`
	Deploys     []aiDeploy `json:"deploys"`
}

// buildOperationContext summarises a single (service, operation) RED
// window + its baseline from operation_summary_5m. operation is the raw
// span name (normalized=false) so it matches the frontend `?op=` value
// and the chart's `name = "..."` DSL.
func (s *Server) buildOperationContext(ctx context.Context, service, operation string, from, to time.Time) *aiOperationContext {
	span := to.Sub(from)
	winSec := span.Seconds()
	cx := &aiOperationContext{Service: service, Operation: operation, RangeS: int64(winSec)}

	// env "" — AI analysis explains the whole service, env-agnostic.
	rows, err := s.store.GetOperationSummaryCompared(ctx, service, 0, from, to, false, "")
	if err != nil {
		return cx
	}
	var row *chstore.OperationSummary
	for i := range rows {
		if rows[i].Name == operation {
			row = &rows[i]
			break
		}
	}
	if row == nil {
		return cx
	}
	cx.Current = aiRED{
		Spans: row.SpanCount, ErrorCount: row.ErrorCount, ErrorRate: row.ErrorRate,
		AvgMs: row.AvgMs, P50Ms: row.P50Ms, P95Ms: row.P95Ms, P99Ms: row.P99Ms,
	}
	if winSec > 0 {
		cx.Current.Rate = float64(row.SpanCount) / winSec
	}
	cx.Apdex = row.Apdex
	if row.HasPrior {
		cx.HasBaseline = true
		cx.Baseline = aiRED{
			Spans: row.PriorSpanCount, ErrorCount: row.PriorErrorCount, ErrorRate: row.PriorErrorRate,
			AvgMs: row.PriorAvgMs, P50Ms: row.PriorP50Ms, P95Ms: row.PriorP95Ms, P99Ms: row.PriorP99Ms,
		}
		// Baseline.Rate KASITLI olarak set edilmez (v0.9.187): prior
		// pencere GetOperationSummaryCompared'da sınır bucket'ı atıldığı
		// için current ile aynı winSec'e bölmek asimetrik/yanıltıcı olur;
		// renderOperationSnapshot baseline rate göstermiyor zaten.
	}
	// Deploy markers are service-level (a deploy touches every operation).
	if deps, derr := s.store.GetServiceDeploys(ctx, service, from, to); derr == nil {
		for _, d := range deps {
			cx.Deploys = append(cx.Deploys, aiDeploy{Version: d.Version, TimeUnixNs: d.TimeUnixNs})
		}
	}
	return cx
}

// renderOperationSnapshot mirrors renderServiceSnapshot for a single
// operation (compact Turkish; field order/labels affect the narration).
func renderOperationSnapshot(cx *aiOperationContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Operasyon: %s / %s (son %d dakika)\n", cx.Service, cx.Operation, cx.RangeS/60)
	c := cx.Current
	fmt.Fprintf(&b, "RED: rate=%.2f req/s, error=%.2f%% (%d hata), p50=%.0fms, p95=%.0fms, p99=%.0fms, apdex=%.2f\n",
		c.Rate, c.ErrorRate, c.ErrorCount, c.P50Ms, c.P95Ms, c.P99Ms, cx.Apdex)
	if cx.HasBaseline {
		bl := cx.Baseline
		fmt.Fprintf(&b, "Baseline (önceki %d dk): error=%.2f%%, p99=%.0fms, p50=%.0fms\n",
			cx.RangeS/60, bl.ErrorRate, bl.P99Ms, bl.P50Ms)
	} else {
		b.WriteString("Baseline: önceki pencerede bu operasyon için veri yok.\n")
	}
	if len(cx.Deploys) > 0 {
		var parts []string
		for _, d := range cx.Deploys {
			parts = append(parts, d.Version)
		}
		fmt.Fprintf(&b, "Deploy(lar) (servis geneli): %s\n", strings.Join(parts, ", "))
	}
	return b.String()
}
