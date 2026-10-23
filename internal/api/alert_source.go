package api

// alert_source.go — v0.10.1489: alarm kuralı ölçüsünün KAYNAĞI Alerts
// sayfasında (önizleme + taban önerisi) — değerlendiricinin kullanacağı kaynak.
//
// Değerlendirici (internal/evaluator/alert_source.go) kural×servis başına
// VictoriaMetrics'in ÖRNEKLENMEMİŞ HTTP/gRPC sunucu histogramını ya da span
// MV'sini seçiyor. Önizleme başka kaynaktan çizerse operatör örneklenmiş
// error_rate'e göre eşik seçer, canlı kural örneklenmemiş değeri ölçer —
// "önizleme ≠ canlı". Bu yüzden üç yüzey TEK okuyucudan (anomaly.AlertVMReader,
// main.go'da süreç başına bir örnek, evaluator ile paylaşılır) ve TEK karar
// işlevinden geçer, cevap kaynağı SÖYLER (`source`).
//
//	GET /api/alert-rules/preview-series?service&metric&windowSec&from[&to] — editor;
//	    kural taslağının dizisi (ConditionPreview). alert_rule_series.go ile aynı
//	    kurucu (alertSeriesWithSource), pencere taslaktan.
//	GET /api/alert-rules/baseline?service&metric&comparator[&windowSec] — editor;
//	    ✨ eşik önerisi. v0.10.1489'da api.go'dan buraya TAŞINDI (api.go büyümez;
//	    gövde kaynak-duyarlı oldu).
//
// Neden ayrı uç (alert_rule_series'e "taslak" kipi DEĞİL): o uç kural kimliği
// ister ve viewer'a açıktır (problem detayı); taslak henüz kaydedilmedi ve
// editörün kural formu yalnız editor'e açık.

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("alert-source", (*Server).registerAlertSourceRoutes) }

func (s *Server) registerAlertSourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/alert-rules/preview-series", auth.RequireAnyRole(editorRoles, s.getAlertPreviewSeries))
	mux.HandleFunc("GET /api/alert-rules/baseline", auth.RequireAnyRole(editorRoles, s.getAlertBaseline))
}

// alertVMReaderPtr — süreç-içi okuyucu (main.go SetAlertVM; nil = span).
var alertVMReaderPtr atomic.Pointer[anomaly.AlertVMReader]

// SetAlertVM — main.go: evaluator ile AYNI örnek (önizleme = canlı karar).
func (s *Server) SetAlertVM(r *anomaly.AlertVMReader) { alertVMReaderPtr.Store(r) }

// alertSourceView — cevaptaki kaynak (rozet + kanıt satırı).
type alertSourceView struct {
	VM       bool   `json:"vm"`
	Family   string `json:"family,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Evidence string `json:"evidence"`
}

func newAlertSourceView(src anomaly.AlertSource) *alertSourceView {
	return &alertSourceView{VM: src.VM, Family: src.Family, Reason: src.Reason, Evidence: src.Evidence()}
}

// alertSourceKeyPart — SAF: önbellek anahtarının kaynak girdileri (ayar kipi +
// ortam kapsamı + okuyucu bağlı mı). Karar türetilmiş veridir; girdileri anahtarda.
func alertSourceKeyPart(cfg chstore.AnomalySensitivityConfig, wired bool) string {
	return fmt.Sprintf("src=%s:env=%s:vm=%t", cfg.AlertSourceMode(), cfg.REDSourceEnvScope(), wired)
}

// alertSourceAuditDetail — SAF: alarm kaynağı değiştiyse denetim metni.
func alertSourceAuditDetail(prev, next chstore.AnomalySensitivityConfig) (string, bool) {
	pm, nm := prev.AlertSourceMode(), next.AlertSourceMode()
	if pm == nm {
		return "", false
	}
	return fmt.Sprintf(`{"from":{"alertSource":%q},"to":{"alertSource":%q}}`, pm, nm), true
}

// alertSourceSpanOnlyReason — yaygın yavaşlama dizisinin gerekçesi.
const alertSourceSpanOnlyReason = "yaygın yavaşlama span MV'sinden karar verir"

// alertSeriesWithSource — kural dizisi değerlendiricinin kaynağından: VM
// seçildiyse VM aralık okuması, değilse span (chstore.AlertMetricSeries).
// spanOnly: kuralın kararı alarm değerlendiricisinde değil, span MV'sinden
// veren başka bir dedektörde (yaygın yavaşlama) — dizi de span.
func alertSeriesWithSource(ctx context.Context, st alertRuleSeriesStore, q chstore.AlertMetricSeriesQuery, stepSec int, spanOnly bool) ([]chstore.AlertMetricPoint, anomaly.AlertSource, error) {
	if spanOnly {
		pts, err := st.AlertMetricSeries(ctx, q)
		return pts, anomaly.SpanAlertSource(alertSourceSpanOnlyReason), err
	}
	pts, src, used := alertVMReaderPtr.Load().Series(ctx, q.Service, q.Metric, q.WindowSec, q.From, q.To, stepSec)
	if used {
		return pts, src, nil
	}
	spanPts, err := st.AlertMetricSeries(ctx, q)
	return spanPts, src, err
}

// getAlertPreviewSeries — kural taslağının dizisi (son 1 sa varsayılan).
func (s *Server) getAlertPreviewSeries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	service := strings.TrimSpace(q.Get("service"))
	metric := strings.TrimSpace(q.Get("metric"))
	windowSec, _ := strconv.Atoi(q.Get("windowSec"))
	if service == "" || metric == "" {
		writeJSONError(w, http.StatusBadRequest, "service ve metric zorunlu (kural servis başına değerlendirilir)")
		return
	}
	if windowSec <= 0 {
		windowSec = 300
	}
	spec, err := chstore.AlertMetricSeriesSpec(metric, windowSec)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "bu metrik için dizi yok")
		return
	}
	now := time.Now()
	from := parseTime(q.Get("from"))
	if from.IsZero() {
		from = now.Add(-time.Hour)
	}
	f, t, ok := snapAlertRuleSeriesWindow(from, parseTime(q.Get("to")), now, spec)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "from, to'dan önce olmalı")
		return
	}
	st := alertRuleSeriesStoreOf(s)
	srcKey := alertSourceKeyPart(st.AnomalySensitivity(), alertVMReaderPtr.Load() != nil)
	key := alertRuleSeriesKey("preview", metric, service, windowSec, f, t, spec.StepSec, 0) + ":" + srcKey
	s.serveCached(w, r, key, alertRuleSeriesTTL, func(ctx context.Context) (any, error) {
		pts, src, err := alertSeriesWithSource(ctx, st, chstore.AlertMetricSeriesQuery{
			Metric: metric, Service: service, WindowSec: windowSec, From: f, To: t, Now: time.Now(),
		}, spec.StepSec, false)
		if err != nil {
			return nil, err
		}
		if pts == nil {
			pts = []chstore.AlertMetricPoint{}
		}
		return alertRuleSeriesResponse{
			Metric: metric, Service: service, WindowSec: windowSec, StepSec: spec.StepSec,
			From: f.UnixNano(), To: t.UnixNano(), Points: pts, Source: newAlertSourceView(src),
		}, nil
	})
}

// getAlertBaseline computes recent percentile distribution
// for the requested service + metric so the alert-rule editor
// can pre-fill a sane threshold instead of making the operator
// guess. Lookback defaults to 7d (capped server-side); the
// service param is optional — empty returns a global
// baseline.
//
// Drives the ✨ "suggest threshold" panel next to the rule
// form's threshold input. Pure stats, no LLM round-trip, so
// the response lands in ms.
//
// Response also carries `suggestedWarning` / `suggestedCritical`
// — interpreted relative to the comparator the operator
// picked:
//   - `>`  / `>=` → high values trip (p95 → warn, p99 → crit)
//   - `<`  / `<=` → low values trip (5×p99/100 capped, 0.01 fallback)
//
// Operator can ignore them and use the raw percentiles too.
//
// Cached 5 min per (service, metric, comparator) tuple — the
// distribution doesn't shift by the second.
//
// v0.10.1489 — dağılım değerlendiricinin KAYNAĞINDAN (anomaly.AlertVMReader
// .Baseline: VM seçildiyse örneklenmemiş 5 dk kovalar / 7 günlük histogram,
// değilse span MV'si) ve cevap `source` taşır. Servis boşsa (genel taban) span.
func (s *Server) getAlertBaseline(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	metric := strings.TrimSpace(q.Get("metric"))
	if metric == "" {
		http.Error(w, "metric required", http.StatusBadRequest)
		return
	}
	service := strings.TrimSpace(q.Get("service"))
	comparator := strings.TrimSpace(q.Get("comparator"))
	if comparator == "" {
		comparator = ">"
	}
	// v0.10.1489 — kuralın penceresi kaynak kararının girdisi (2 dk altı span).
	windowSec, _ := strconv.Atoi(q.Get("windowSec"))
	if windowSec <= 0 {
		windowSec = 300
	}
	srcKey := alertSourceKeyPart(s.store.AnomalySensitivity(), alertVMReaderPtr.Load() != nil)
	cacheKey := fmt.Sprintf("alert-baseline:v2:%s:%s:%s:w=%d:%s", service, metric, comparator, windowSec, srcKey)
	s.serveCached(w, r, cacheKey, 5*time.Minute, func(ctx context.Context) (any, error) {
		b, src, used := alertVMReaderPtr.Load().Baseline(ctx, service, metric, windowSec)
		if !used {
			var err error
			b, err = s.store.GetMetricBaseline(ctx, service, metric, 7*24*time.Hour)
			if err != nil {
				return nil, err
			}
		}
		warn, crit := suggestThresholds(b, comparator)
		return map[string]any{
			"metric":  b.Metric,
			"service": b.Service,
			"p50":     b.P50, "p95": b.P95, "bucketSec": b.BucketSec, // v0.10.877 — oran dağılımlarının kova genişliği (300; gecikme 0)
			"p99":               b.P99,
			"max":               b.Max,
			"mean":              b.Mean,
			"sampleCount":       b.SampleCount,
			"windowSec":         b.WindowSec,
			"suggestedWarning":  warn,
			"suggestedCritical": crit,
			"source":            newAlertSourceView(src), // v0.10.1489
		}, nil
	})
}

// suggestThresholds picks "safe to page on" levels from the
// distribution, biased by the comparator the operator chose.
//
//   - `>` / `>=` (alert on spike): warn at p95, crit at p99 —
//     fires when ~5% / ~1% of samples already crossed. Pure
//     percentile floors keep operators from setting "1 / 5"
//     thresholds that page nightly because the actual baseline
//     is 800ms / 8%.
//   - `<` / `<=` (alert on drop): warn at p5, crit at p1
//     approximated as max(0.01, value/2 / value/5) — fires
//     when traffic genuinely fell below the floor instead of
//     just dipping at a quiet hour.
//   - Round to a reasonable precision so the threshold input
//     doesn't end up at 412.7345 ms.
func suggestThresholds(b *chstore.MetricBaseline, comparator string) (warn, crit float64) {
	switch comparator {
	case "<", "<=":
		// Low-side: floor at 1% of the typical value to avoid
		// "alert when traffic = 0" being meaningless during
		// known quiet windows. Operator can tighten manually.
		warn = b.P50 * 0.20
		crit = b.P50 * 0.05
		if crit < 0.01 {
			crit = 0.01
		}
	default:
		// High-side: lean on the actual distribution.
		warn = b.P95
		crit = b.P99
	}
	return roundThreshold(warn), roundThreshold(crit)
}

// roundThreshold drops absurd decimals from a float so the UI
// shows "420 ms" not "419.7833... ms".
func roundThreshold(v float64) float64 {
	switch {
	case v >= 1000:
		return float64(int64(v/10+0.5)) * 10
	case v >= 100:
		return float64(int64(v + 0.5))
	case v >= 10:
		return float64(int64(v*10+0.5)) / 10
	default:
		return float64(int64(v*100+0.5)) / 100
	}
}
