package anomaly

// alert_vm_query.go — v0.10.1489: ALARM KURALLARININ VictoriaMetrics kaynağı —
// SAF parçalar (metrik eşlemesi, PromQL kurucuları, değer birleştirme, kaynak
// seçimi). G/Ç alert_vm.go'da.
//
// NEDEN: v0.10.1478 anomali dedektörünü örneklenmemiş VM histogramına taşıdı;
// alarm kuralları (evaluator evaluateOne) hâlâ span MV'sinden ölçüyordu. Prod
// trace'leri gateway'de kuyruk örnekleniyor (hatalar hep + ~%5 taban): span
// error_rate yukarı yanlı, request_rate gürültülü bir örnek → sahte alarm.
// Kural semantiği (eşik / forSec / cooldown / minSamples / karşılaştırıcı)
// DEĞİŞMEZ; yalnız ölçünün kaynağı değişir.
//
// Aile kataloğu, kapsam keşfi (servis / durum / ortam etiketi), durum-etiketli
// servis kümesi ve geri çekilme 1478'in (red_vm_query.go / red_vm_source.go)
// AYNI kodudur — burada yalnız PENCERE ifadeleri var: anomali 5 dk kovalarını
// okur, kural kendi WindowSec penceresini.
//
// Metrik eşlemesi (pencere W, değerlendirme anı t = şimdi − vmIngestLag):
//
//	request_rate = increase(count[W]) / W
//	error_count  = increase(count{durum=~<sunucu hatası>}[W])   (HTTP 5.., gRPC pinli küme)
//	error_rate   = error_count / increase(count[W]) · 100
//	avg_ms       = increase(sum[W]) / increase(count[W]) · ölçek
//	p50/p95/p99  = histogram_quantiles("phi", 0.5, 0.95, 0.99, sum by (svc, le) (increase(bucket[W]))) · ölçek
//	minSamples   = increase(count[W])  — artık GERÇEK istek sayısı
//
// Aileler ASLA toplanmaz (HTTP > gRPC legacy > gRPC stable; servis ilk
// bulunduğu ailede). Taşıma metrikleri (http_* / db_* / rpc_* / mq_*) ve 2 dk
// altı pencereler span'de kalır (gerekçeli).

import (
	"math"
	"strconv"
	"strings"
)

// alertVMKind — kural penceresinde okunan büyüklük.
type alertVMKind int

const (
	akCount alertVMKind = iota // istek sayısı (pencere artışı)
	akErr                      // sunucu-hatası sayısı
	akSum                      // süre toplamı (ailenin biriminde)
	akQuant                    // p50 / p95 / p99 (ailenin biriminde)
	akN
)

func (k alertVMKind) String() string {
	switch k {
	case akCount:
		return "count"
	case akErr:
		return "err"
	case akSum:
		return "sum"
	case akQuant:
		return "quantile"
	}
	return "?"
}

// alertVMMinWindowSec — VM penceresinin alt sınırı: collector dışa aktarımı
// ≈60 sn; 2 dk altı pencerede increase() tek örnekten tahmin yürütür.
const alertVMMinWindowSec = 120

// alertVMPhis — kural yüzdelikleri (tek sorguda, histogram_quantiles).
var alertVMPhis = []string{"0.5", "0.95", "0.99"}

// alertVMMetricKinds — SAF: metriğin VM'de gereken türleri. count HER metrikte
// var: aile üyeliği + minSamples aynı kaynaktan (kaynak karışmaz).
func alertVMMetricKinds(metric string) ([]alertVMKind, bool) {
	switch metric {
	case "request_rate":
		return []alertVMKind{akCount}, true
	case "error_count", "error_rate":
		return []alertVMKind{akCount, akErr}, true
	case "avg_ms":
		return []alertVMKind{akCount, akSum}, true
	case "p50_ms", "p95_ms", "p99_ms":
		return []alertVMKind{akCount, akQuant}, true
	}
	return nil, false
}

// alertVMPhi — SAF: yüzdelik metriğinin phi etiketi ("" = yüzdelik değil).
func alertVMPhi(metric string) string {
	switch metric {
	case "p50_ms":
		return "0.5"
	case "p95_ms":
		return "0.95"
	case "p99_ms":
		return "0.99"
	}
	return ""
}

// alertVMScale — SAF: ailenin süre birimi → ms çarpanı.
func alertVMScale(f vmREDFamily) float64 {
	if f.Unit == "ms" {
		return 1
	}
	return 1000
}

// alertVMWindow — SAF: PromQL pencere eki.
func alertVMWindow(windowSec int) string { return "[" + strconv.Itoa(windowSec) + "s]" }

// alertVMSelector — SAF: vmSelector + isteğe bağlı ek eşleştiriciler.
func alertVMSelector(f vmREDFamily, nameKind string, sc vmQueryScope, extras ...string) string {
	var ex []string
	for _, e := range extras {
		if e = strings.TrimSpace(e); e != "" {
			ex = append(ex, e)
		}
	}
	return vmSelector(f, nameKind, sc, strings.Join(ex, ", "))
}

// alertVMExpr — SAF: (aile, tür, pencere) ifadesi. svcMatcher: servis
// süzgeci ("" = filo geneli). phis: yüzdelik listesi (nil = alertVMPhis).
// ok=false: tür bu kapsamda ölçülemez (durum etiketi yok → hata YOK; 0 sanılmasın).
func alertVMExpr(f vmREDFamily, k alertVMKind, sc vmQueryScope, windowSec int, svcMatcher string, phis []string) (string, bool) {
	if sc.SvcLabel == "" || windowSec <= 0 {
		return "", false
	}
	by := sc.SvcLabel
	win := alertVMWindow(windowSec)
	switch k {
	case akCount:
		return "sum by (" + by + ") (increase(" + alertVMSelector(f, "count", sc, svcMatcher) + win + "))", true
	case akErr:
		if len(sc.StatusLabels) == 0 {
			return "", false
		}
		parts := make([]string, 0, len(sc.StatusLabels))
		for _, l := range sc.StatusLabels {
			parts = append(parts, "sum by ("+by+") (increase("+alertVMSelector(f, "count", sc, svcMatcher, l+`=~"`+f.ServerErr+`"`)+win+"))")
		}
		// `or`: semconv geçişinde iki yazımı birden taşıyan servis iki kez sayılmaz.
		return "(" + strings.Join(parts, " or ") + ")", true
	case akSum:
		return "sum by (" + by + ") (increase(" + alertVMSelector(f, "sum", sc, svcMatcher) + win + "))", true
	case akQuant:
		if len(phis) == 0 {
			phis = alertVMPhis
		}
		return `histogram_quantiles("phi", ` + strings.Join(phis, ", ") + ", sum by (" + by + ", le) (increase(" +
			alertVMSelector(f, "bucket", sc, svcMatcher) + win + ")))", true
	}
	return "", false
}

// alertVMPresenceQuery — SAF: son 1 sa'te ailede istek serisi TAŞIYAN
// servisler (anlık). Aile üyeliği buradan: pencerede istek yoksa servis
// ailesinden düşmez (request_rate < kuralı sessizlikte 0 okur, span'e kaymaz).
func alertVMPresenceQuery(f vmREDFamily, sc vmQueryScope) (string, bool) {
	if sc.SvcLabel == "" {
		return "", false
	}
	return "group by (" + sc.SvcLabel + ") (last_over_time(" + vmSelector(f, "count", sc, "") + "[1h]))", true
}

// alertVMLabel — SAF: GroupKey (`k="v"`) içinden etiket değeri.
func alertVMLabel(key []string, name string) (string, bool) {
	for _, kv := range key {
		i := strings.IndexByte(kv, '=')
		if i <= 0 || kv[:i] != name {
			continue
		}
		raw := kv[i+1:]
		v, err := strconv.Unquote(raw)
		if err != nil {
			v = strings.Trim(raw, `"`)
		}
		return v, v != ""
	}
	return "", false
}

// alertVMSample — bir servisin pencere ölçüleri (ham, ailenin biriminde).
type alertVMSample struct {
	Count, Err, Sum    float64
	HaveCount, HaveErr bool
	HaveSum            bool
	Quant              map[string]float64 // phi → değer
	Scale              float64            // süre → ms
	WindowSec          int
}

// alertVMValue — SAF: metriğin değeri. present=false → pencerede ölçü yok
// (span yolunun boş sonucu; çağıran absentMeasure ile aynı anlama çevirir).
// Sayım türlerinde seri yokluğu 0'dır (aile üyesi servis, pencerede istek yok).
func alertVMValue(metric string, s alertVMSample) (float64, bool) {
	scale := s.Scale
	if scale <= 0 {
		scale = 1
	}
	count := 0.0
	if s.HaveCount && s.Count > 0 {
		count = s.Count
	}
	errs := 0.0
	if s.HaveErr && s.Err > 0 {
		errs = s.Err
	}
	switch metric {
	case "request_rate":
		if s.WindowSec <= 0 {
			return 0, false
		}
		return count / float64(s.WindowSec), true
	case "error_count":
		return errs, true
	case "error_rate":
		if count <= 0 {
			return 0, false
		}
		return math.Min(errs/count*100, 100), true
	case "avg_ms":
		if count <= 0 || !s.HaveSum || s.Sum < 0 {
			return 0, false
		}
		return s.Sum / count * scale, true
	case "p50_ms", "p95_ms", "p99_ms":
		q, ok := s.Quant[alertVMPhi(metric)]
		if !ok || math.IsNaN(q) || math.IsInf(q, 0) {
			return 0, false
		}
		return q * scale, true
	}
	return 0, false
}

// Gerekçeler (kanıt satırı + durum ucu). Aile/durum gerekçeleri 1478'inkiler.
const (
	alertReasonSpanSetting = "alarm kaynağı ayarı span"
	alertReasonMetric      = "metrik VM'de karşılanmıyor (yalnız temel RED metrikleri)"
	alertReasonWindow      = "pencere VictoriaMetrics için çok kısa (< 2 dk)"
	alertReasonBudget      = "VictoriaMetrics sorgu bütçesi bu tikte doldu"
	alertReasonNoTick      = "VictoriaMetrics okuyucusu bağlı değil"
)

// alertPickInput — kaynak kararının girdileri.
type alertPickInput struct {
	SpanSetting     bool
	Configured      bool
	MetricSupported bool
	WindowOK        bool
	FamilyFound     bool
	FamilyReason    string
	KindsOK         bool
	KindReason      string
}

// pickAlertSource — SAF: kural×servis için VM mi span mi + gerekçe.
func pickAlertSource(in alertPickInput) (bool, string) {
	switch {
	case in.SpanSetting:
		return false, alertReasonSpanSetting
	case !in.Configured:
		return false, redReasonNotConfigured
	case !in.MetricSupported:
		return false, alertReasonMetric
	case !in.WindowOK:
		return false, alertReasonWindow
	case !in.FamilyFound:
		if in.FamilyReason == "" {
			return false, redReasonNoService
		}
		return false, in.FamilyReason
	case !in.KindsOK:
		return false, in.KindReason
	}
	return true, ""
}

// AlertSource — bir kural×servis ölçüsünün kaynağı (kanıt + önizleme rozeti).
type AlertSource struct {
	VM     bool   `json:"vm"`
	Family string `json:"family,omitempty"` // VM: "HTTP" | "gRPC"
	Reason string `json:"reason,omitempty"` // span: neden VM değil
}

// SpanAlertSource — span kaynağı + gerekçe.
func SpanAlertSource(reason string) AlertSource { return AlertSource{Reason: reason} }

// Evidence — SAF: Problem açıklamasına / bildirime giden kaynak satırı (1478
// ile aynı metin): "kaynak: VictoriaMetrics (örneklenmemiş) · HTTP sunucu
// histogramı" ya da "kaynak: span (örneklenmiş) — VM kullanılmadı: <neden>".
func (s AlertSource) Evidence() string {
	return redSourceEvidenceTR(redSourceUsed(s))
}

// ShownReason — SAF: kullanıcıya gösterilen gerekçe (shownSourceReason; VM
// hiç yapılandırılmamışsa ""). Rozet / API cevabı bunu okur.
func (s AlertSource) ShownReason() string {
	if s.VM {
		return ""
	}
	return shownSourceReason(s.Reason)
}

// Label — SAF: kısa rozet ("vm" | "span") — durum sayımı.
func (s AlertSource) Label() string {
	if s.VM {
		return redSourceVM
	}
	return redSourceSpan
}

// WithAlertSourceEvidence — SAF: açıklamaya kaynak satırı.
func WithAlertSourceEvidence(desc string, s AlertSource) string {
	return withREDEvidence(desc, s.Evidence())
}

// alertVMQuantiles — SAF: sıralı örneklerden doğrusal aradeğerli yüzdelik
// (taban önerisi; boş → 0).
func alertVMQuantiles(sorted []float64, qs ...float64) []float64 {
	out := make([]float64, len(qs))
	n := len(sorted)
	if n == 0 {
		return out
	}
	for i, q := range qs {
		pos := q * float64(n-1)
		lo := int(math.Floor(pos))
		hi := int(math.Ceil(pos))
		if lo < 0 {
			lo = 0
		}
		if hi >= n {
			hi = n - 1
		}
		frac := pos - float64(lo)
		out[i] = sorted[lo] + (sorted[hi]-sorted[lo])*frac
	}
	return out
}
