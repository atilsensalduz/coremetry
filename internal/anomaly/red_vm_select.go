package anomaly

// red_vm_select.go — v0.10.1478: servis × metrik başına RED kaynağı seçimi +
// span toplu okumasının ÜSTÜNE VM serisinin bindirilmesi + kanıt satırı.
//
// SÖZLEŞME (aiops — tek kaynak kuralı): bir (servis, metrik) çiftinin ardışık
// serisi, hacim serisi, mevsimsel tabanı ve mevsimsel hacmi AYNI kaynaktan
// gelir. VM seçildiyse span'in o çiftteki dört serisi de SİLİNİR/EZİLİR —
// örneklenmiş span error_rate'i VM'in tabanına ya da cari değerine ASLA
// karışmaz (TestOverlayNeverMixesSampledSpanErrorRate). VM'in mevsimsel tabanı
// yetersizse (gün çeşitliliği) span'inkine değil, VM'in ardışık penceresine
// düşülür (chooseBaseline'ın bugünkü yedeği).
//
// Seçim (pickREDSource, SAF, tablo testli):
//
//	kaynak ayarı span             → span ("kaynak ayarı span")
//	VM yapılandırılmamış          → span
//	servisin HTTP/gRPC ailesi yok → span
//	aile okunamadı / ısınıyor     → span (gerekçeli)
//	metrik ölçülemiyor            → span (ör. durum etiketi yok → error_rate)
//	VM serisi yetersiz            → span (yeni enstrümantasyon: span geçmişi uzun)
//	aksi                          → VM
//
// Batch yük kapısı / dwell / tabanlar / terfi kapısı DEĞİŞMEZ: kapılar
// bindirilmiş seriyi okur (hacim VM'de gerçek istek/sn).

import (
	"math"
	"time"
)

const (
	redSourceVM   = "vm"
	redSourceSpan = "span"
)

// Gerekçe metinleri (kanıt + durum ucu).
const (
	redReasonSpanSetting   = "kaynak ayarı span"
	redReasonNotConfigured = "VictoriaMetrics yapılandırılmamış"
	redReasonNoFamily      = "OTel HTTP/gRPC sunucu histogramı VictoriaMetrics'te yok"
	redReasonNoService     = "servisin HTTP/gRPC sunucu histogramı serisi yok"
	redReasonVMError       = "VictoriaMetrics okunamadı (okunamadı ≠ yok)"
	redReasonWarming       = "VictoriaMetrics geçmişi ısınıyor"
	redReasonNoStatus      = "durum kodu etiketi yok — hata oranı VM'den ölçülemez"
	redReasonShort         = "VictoriaMetrics serisi yetersiz (taban + dwell kovası yok)"
)

// redSourceUsed — çiftin kaynağı (kanıt).
type redSourceUsed struct {
	VM     bool
	Family string // VM: "HTTP" | "gRPC"
	Reason string // span: neden VM değil ("" = VM hiç denenmedi değil, ayar)
}

// redSourceEvidenceTR — SAF: Problem açıklamasına / olay ayrıntısına eklenen
// kaynak satırı. Operatörün prod'da arayacağı metin budur.
func redSourceEvidenceTR(u redSourceUsed) string {
	if u.VM {
		s := "kaynak: VictoriaMetrics (örneklenmemiş)"
		if u.Family != "" {
			s += " · " + u.Family + " sunucu histogramı"
		}
		return s
	}
	s := "kaynak: span (örneklenmiş)"
	if u.Reason != "" {
		s += " — VM kullanılmadı: " + u.Reason
	}
	return s
}

// withREDEvidence — SAF: açıklamaya kaynak satırı (boşsa aynen).
func withREDEvidence(desc, ev string) string {
	if ev == "" {
		return desc
	}
	return desc + " " + ev + "."
}

// vmPickInput — seçim kararının girdileri.
type vmPickInput struct {
	SpanSetting  bool
	Configured   bool
	FamilyFound  bool   // servis bir ailede VAR (ardışık pencerede istek)
	FamilyReason string // aile bulunamadıysa en anlamlı gerekçe
	MetricOK     bool
	MetricReason string
	Enough       bool // enoughHistory(VM serisi)
}

// pickREDSource — SAF: VM mi span mi + gerekçe.
func pickREDSource(in vmPickInput) (bool, string) {
	switch {
	case in.SpanSetting:
		return false, redReasonSpanSetting
	case !in.Configured:
		return false, redReasonNotConfigured
	case !in.FamilyFound:
		if in.FamilyReason == "" {
			return false, redReasonNoService
		}
		return false, in.FamilyReason
	case !in.MetricOK:
		return false, in.MetricReason
	case !in.Enough:
		return false, redReasonShort
	}
	return true, ""
}

// vmREDView — tikin VM görünümü (prepare üretir; okuma tik goroutine'inde).
type vmREDView struct {
	mode          string
	configured    bool
	reason        string
	store         *vmREDStore
	meta          []vmFamMeta
	vnow          time.Time
	upper         int64
	consec        []int64
	seasonal      []int64
	seasonalClass string
	behavior      []int64
	readyDet      [vmFamN][vmKindN]bool
	readyBeh      [vmFamN][vmKindN]bool
	failed        [vmFamN][vmKindN]bool // son okuma hata verdi (geri çekilme sürüyor)
	// ölçüm
	queries, queryErrs, bucketsFetched, pending int
	lastErr                                     string
}

// vmSeries — bir çiftin VM serileri (span toplu okumasıyla aynı şekil).
type vmSeries struct {
	Family                  string
	Values, Rates           []float64
	Seasonal, SeasonalRates []float64
}

// redSeriesProvider — overlay'in kaynağı (görünüm ya da test sahtesi).
type redSeriesProvider interface {
	seriesFor(service, metric string, dwell int) (vmSeries, redSourceUsed)
}

// famFor — servisin ailesi: sıradaki ilk aile ki kapsamı kullanılabilir,
// istek türü hazır ve servisin ardışık pencerede en az bir isteği var.
// Aileler ASLA toplanmaz. ok=false → gerekçe.
func (v *vmREDView) famFor(service string) (int, string) {
	reason := ""
	for fi := range vmREDFamilies {
		if fi >= len(v.meta) {
			break
		}
		m := v.meta[fi]
		if !m.ok {
			if reason == "" && m.reason != "" && m.reason != redReasonNoFamily {
				reason = m.reason
			}
			continue
		}
		if !v.readyDet[fi][vmKindCount] {
			reason = v.notReadyReason(fi, vmKindCount)
			continue
		}
		for _, b := range v.consec {
			if c, ok := v.store.get(fi, vmKindCount, b, service); ok && c > 0 {
				return fi, ""
			}
		}
		if reason == "" {
			reason = redReasonNoService
		}
	}
	if reason == "" {
		reason = redReasonNoFamily // hiçbir aile VM'de yok
	}
	return -1, reason
}

// metricOK — metriğin bu ailede ölçülebilirliği.
func (v *vmREDView) metricOK(fi int, service, metric string) (bool, string) {
	need := []vmREDKind{vmKindCount}
	switch metric {
	case "error_rate":
		m := v.meta[fi]
		if len(m.scope.StatusLabels) == 0 {
			return false, redReasonNoStatus
		}
		if m.statusSvc == nil {
			return false, redReasonVMError + ": durum etiketi kümesi"
		}
		if !m.statusSvc[service] {
			return false, redReasonNoStatus
		}
		need = append(need, vmKindErr)
	case "p99_ms":
		need = append(need, vmKindP99)
	}
	for _, k := range need {
		if !v.readyDet[fi][k] {
			return false, v.notReadyReason(fi, k)
		}
	}
	return true, ""
}

// notReadyReason — hazır olmayan türün gerekçesi: okuma hatası mı ısınma mı.
func (v *vmREDView) notReadyReason(fi int, k vmREDKind) string {
	if v.failed[fi][k] {
		return redReasonVMError + " (" + k.String() + ")"
	}
	return redReasonWarming + " (" + k.String() + ")"
}

// vmBucketValue — SAF: kovanın metrik değeri + hacmi; ok=false = satır yok
// (span MV'sinde GROUP BY satırı olmayan kovanın ikizi: istek yok).
func (v *vmREDView) vmBucketValue(fi int, service, metric string, b int64) (val, rate float64, ok bool) {
	c, ok := v.store.get(fi, vmKindCount, b, service)
	if !ok || c <= 0 {
		return 0, 0, false
	}
	rate = c / float64(bucketSeconds)
	switch metric {
	case "error_rate":
		e, _ := v.store.get(fi, vmKindErr, b, service) // seri yok = 0 hata (durum etiketi servisçe var)
		if e < 0 {
			e = 0
		}
		return math.Min(e/c*100, 100), rate, true
	case "request_rate":
		return rate, rate, true
	case "p99_ms":
		p, ok := v.store.get(fi, vmKindP99, b, service)
		if !ok {
			return 0, 0, false
		}
		return p, rate, true
	}
	return 0, 0, false
}

// seriesFor — redSeriesProvider.
func (v *vmREDView) seriesFor(service, metric string, dwell int) (vmSeries, redSourceUsed) {
	in := vmPickInput{SpanSetting: v.mode == "span", Configured: v.configured}
	if in.SpanSetting || !in.Configured {
		_, r := pickREDSource(in)
		return vmSeries{}, redSourceUsed{Reason: r}
	}
	fi, freason := v.famFor(service)
	in.FamilyFound, in.FamilyReason = fi >= 0, freason
	if !in.FamilyFound {
		_, r := pickREDSource(in)
		return vmSeries{}, redSourceUsed{Reason: r}
	}
	in.MetricOK, in.MetricReason = v.metricOK(fi, service, metric)
	var s vmSeries
	if in.MetricOK {
		s = v.buildSeries(fi, service, metric)
	}
	in.Enough = enoughHistory(len(s.Values), dwell)
	useVM, r := pickREDSource(in)
	if !useVM {
		return vmSeries{}, redSourceUsed{Reason: r}
	}
	return s, redSourceUsed{VM: true, Family: vmREDFamilies[fi].Label}
}

// buildSeries — span okumalarının VM ikizi: ardışık seri (istekli kovalar,
// artan zaman, kuyruk sessizliği sıfırla — padTrailingSilence) + mevsimsel
// örnekler (gün çeşitliliği budaması fetchAllSeasonal ile aynı).
func (v *vmREDView) buildSeries(fi int, service, metric string) vmSeries {
	s := vmSeries{Family: vmREDFamilies[fi].Label}
	var lastT int64
	for _, b := range v.consec {
		val, rate, ok := v.vmBucketValue(fi, service, metric, b)
		if !ok {
			continue
		}
		s.Values = append(s.Values, val)
		s.Rates = append(s.Rates, rate)
		lastT = b
	}
	s.Values = padTrailingSilence(s.Values, lastT, v.upper)
	s.Rates = padTrailingSilence(s.Rates, lastT, v.upper)
	days := map[int64]struct{}{}
	var seas, seasRates []float64
	for _, b := range v.seasonal {
		val, rate, ok := v.vmBucketValue(fi, service, metric, b)
		if !ok {
			continue
		}
		seas = append(seas, val)
		seasRates = append(seasRates, rate)
		days[b/86400] = struct{}{}
	}
	if len(days) >= seasonalMinDaysFor(v.seasonalClass) {
		s.Seasonal, s.SeasonalRates = seas, seasRates
	}
	return s
}

// overlayREDSeries — SAF (sağlayıcı verilince): span toplu okumasının
// haritalarına VM serisini bindirir. Döner: "servis|metrik" → kaynak.
// seasonalRates YALNIZ batch gecikme metriği için taşınır (fetchAllSeasonal
// sözleşmesi).
func overlayREDSeries(p redSeriesProvider, services, tracked []string, dwell int,
	buckets, seasonal, rates, seasonalRates map[string]map[string][]float64) map[string]redSourceUsed {
	used := make(map[string]redSourceUsed, len(services)*len(tracked))
	ensure := func(m map[string]map[string][]float64, metric string) map[string][]float64 {
		if m[metric] == nil {
			m[metric] = map[string][]float64{}
		}
		return m[metric]
	}
	for _, svc := range services {
		for _, metric := range tracked {
			s, u := p.seriesFor(svc, metric, dwell)
			used[svc+"|"+metric] = u
			if !u.VM {
				continue
			}
			ensure(buckets, metric)[svc] = s.Values
			ensure(rates, metric)[svc] = s.Rates
			if s.Seasonal != nil {
				ensure(seasonal, metric)[svc] = s.Seasonal
			} else if seasonal[metric] != nil {
				delete(seasonal[metric], svc) // span tabanı VM cari değerine karışmasın
			}
			if metric == batchLatencyMetric && s.SeasonalRates != nil {
				ensure(seasonalRates, metric)[svc] = s.SeasonalRates
			} else if seasonalRates[metric] != nil {
				delete(seasonalRates[metric], svc)
			}
		}
	}
	return used
}

// behaviorRowsFor — davranış motorunun VM satırları: üç tür de hazır, servis
// durum etiketi taşıyor (hata oranı ölçülebilir) ve aile seçilmiş olmalı.
// ok=false → span satırları kalır.
func (v *vmREDView) behaviorRowsFor(service string) ([]behaviorRow, redSourceUsed, bool) {
	if v == nil || v.mode == "span" || !v.configured || len(v.behavior) == 0 {
		return nil, redSourceUsed{}, false
	}
	fi, _ := v.famFor(service)
	if fi < 0 {
		return nil, redSourceUsed{}, false
	}
	m := v.meta[fi]
	if len(m.scope.StatusLabels) == 0 || m.statusSvc == nil || !m.statusSvc[service] {
		return nil, redSourceUsed{}, false
	}
	for k := vmREDKind(0); k < vmKindN; k++ {
		if !v.readyBeh[fi][k] {
			return nil, redSourceUsed{}, false
		}
	}
	var rows []behaviorRow
	for _, b := range v.behavior {
		c, ok := v.store.get(fi, vmKindCount, b, service)
		spans := uint64(math.Round(c))
		if !ok || spans == 0 {
			continue
		}
		e, _ := v.store.get(fi, vmKindErr, b, service)
		errs := uint64(math.Round(math.Max(e, 0)))
		if errs > spans {
			errs = spans
		}
		p99, _ := v.store.get(fi, vmKindP99, b, service)
		rows = append(rows, behaviorRow{Unix: b, HOW: hourOfWeek(time.Unix(b, 0)), Spans: spans, Errs: errs, P99Ms: p99})
	}
	if len(rows) == 0 {
		return nil, redSourceUsed{}, false
	}
	return rows, redSourceUsed{VM: true, Family: vmREDFamilies[fi].Label}, true
}
