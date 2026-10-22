package api

// resource_bundle.go — v0.10.1431 (CoSRE kaynak incelemesi R1). Operatör:
// "CoSRE sorulan sorulara frontier model gibi anlamlandırarak gerekli
// toolset'lerini koşsun … bir pod'un CPU/memory metriklerini grafik olarak
// getirme; gerekirse GC'sine, JVM metriklerine bakıp LLM'e yorumlatma."
//
// SAF çekirdek + sınırlı okuyucu: Thanos (cAdvisor + kube-state) PromQL
// kurucuları, aile → sorgu kümesi, konsol sonucu çözücü, pod başına
// deterministik istatistik (limit yüzdesi, eşik üstü süre, bellek eğimi,
// pencere içi restart), eşik tablosu ve modelin okuduğu "KAYNAK ÖZETİ" bloğu.
// İnvaryant: "sunucu deterministik toplar, model anlatır" — tüm sayılar,
// yüzdeler, eşik kararları ve eğim burada hesaplanır; prompt yeniden
// hesaplamayı yasaklar (copilot.SystemPromptResourceInvestigation).
//
// Sorgu bütçesi (aile başına ≤4, "all" ≤7; hepsi TEK cluster, pod seçici
// pod=~"<regex>" + cluster'ın NamespaceFilter kalkanı):
//
//	cpu      cpu_usage (range) · cpu_throttle (range) · limits · requests
//	memory   mem_ws (range) · limits · requests · last_term
//	restarts restarts (range, sayaç) · last_term · waiting
//	all      cpu_usage · cpu_throttle · mem_ws · restarts · limits · last_term · waiting
//
// Range: ConsoleQueryRange, adım ⌈aralık/120⌉ (≥15 sn, metric_chart ile aynı),
// rate penceresi max(4×adım, 60 sn), topk(20) + MaxSeries 20; anlık sorgular
// pencerenin sonunda. Toplam bütçe TEK 8 sn son tarih, ≤4 paralel. Her sorgu
// 60 sn önbellekli; anahtar TÜM girdiler (kaynak = thanos:<cluster id|ad>,
// sorgu kimliği, son PromQL, pod regex'i, dakikaya oturtulmuş uçlar, adım, tür).
//
// Eşik tablosu (resourceThresholds; tek yer):
//
//	CPU limit yüzdesi   ≥%80 uyarı · ≥%95 kritik (tepe değer)
//	CPU throttle        ≥%25 uyarı (tepe değer)
//	bellek limit yüzdesi ≥%80 uyarı · ≥%90 kritik (tepe değer)
//	restart (pencere)   >0 uyarı · ≥3 kritik; bekleme sebebi (CrashLoopBackOff …) kritik
//
// Limit/istek pod düzeyinde toplanır (Infrastructure sekmesinin podLimitQuery
// sözleşmesiyle aynı); limiti olmayan pod için yüzde YAZILMAZ ("limit yok").
// R2 (JVM heap/GC/thread satırları + eğim + drill kuralı) ve R3 (sınıflandırıcı
// yedeği) bu dosyada YOK.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// Aileler — router yazar (route.ResFamily).
const (
	resFamCPU      = "cpu"
	resFamMemory   = "memory"
	resFamRestarts = "restarts"
	resFamAll      = "all"
)

// Sorgu kimlikleri.
const (
	resQCPU      = "cpu_usage"
	resQThrottle = "cpu_throttle"
	resQMem      = "mem_ws"
	resQRestarts = "restarts"
	resQLimits   = "limits"
	resQRequests = "requests"
	resQLastTerm = "last_term"
	resQWaiting  = "waiting"
)

// Bütçeler.
const (
	resQueryTimeout   = 8 * time.Second
	resLabelTimeout   = 4 * time.Second
	resParallel       = 4
	resMaxSeries      = 20
	resCacheTTL       = 60 * time.Second
	resLabelLookback  = 24 * time.Hour
	resPrefixHitsMax  = 8
	resSummaryMaxRows = 12 // KAYNAK ÖZETİ gövdesi (başlık hariç)
	resPodRowsMax     = 6
	resMinRangeS      = 300
	resMaxRangeS      = 86400
)

// resourceThresholds — eşik tablosu (dosya başlığında belgeli; tek yer).
var resourceThresholds = struct {
	CPUWarn, CPUCrit         float64
	ThrottleWarn             float64
	MemWarn, MemCrit         float64
	RestartWarn, RestartCrit int
}{CPUWarn: 80, CPUCrit: 95, ThrottleWarn: 25, MemWarn: 80, MemCrit: 90, RestartWarn: 1, RestartCrit: 3}

// resourceThresholdsTR — SAF: eşiklerin tek satırlık okunur hâli.
func resourceThresholdsTR() string {
	t := resourceThresholds
	return fmt.Sprintf("eşikler: CPU limitin %%%.0f/%%%.0f'i, throttle %%%.0f, bellek limitin %%%.0f/%%%.0f'i, restart >%d/≥%d (uyarı/kritik)",
		t.CPUWarn, t.CPUCrit, t.ThrottleWarn, t.MemWarn, t.MemCrit, t.RestartWarn-1, t.RestartCrit)
}

// resQuery — tek sorgunun SAF tanımı.
type resQuery struct {
	ID    string
	Range bool
	Title string // grafik başlığının ölçü kısmı
	Unit  string // grafik birimi (FE: By / % / boş = ham sayı)
	Expr  string
}

// resPromStr — SAF: PromQL dize çerçevesi kaçışı (ters bölü, tırnak).
func resPromStr(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	return strings.ReplaceAll(v, `"`, `\"`)
}

// resPodSel — SAF: `{[container!="",container!="POD",][extra,]pod=~"<re>"[,namespace=~"…"]}`.
func resPodSel(podRe, nsFilter string, container bool, extra string) string {
	var parts []string
	if container {
		parts = append(parts, `container!=""`, `container!="POD"`)
	}
	if extra != "" {
		parts = append(parts, extra)
	}
	parts = append(parts, `pod=~"`+resPromStr(podRe)+`"`)
	return "{" + strings.Join(parts, ",") + thanos.NamespaceMatcher(nsFilter) + "}"
}

// resourceQueries — SAF: aile → sorgu kümesi (sıra = grafik sırası). Aile
// başına ≤4, all ≤7 (TestResourceQueryBudget pinler).
func resourceQueries(family, podRe, nsFilter string, winS int) []resQuery {
	c := resPodSel(podRe, nsFilter, true, "")
	p := resPodSel(podRe, nsFilter, false, "")
	r := resPodSel(podRe, nsFilter, false, `resource=~"cpu|memory"`)
	w := fmt.Sprintf("%ds", winS)
	cpu := resQuery{ID: resQCPU, Range: true, Title: "CPU (core)",
		Expr: fmt.Sprintf("topk(%d, sum by (namespace, pod) (rate(container_cpu_usage_seconds_total%s[%s])))", resMaxSeries, c, w)}
	thr := resQuery{ID: resQThrottle, Range: true, Title: "CPU throttle (%)", Unit: "%",
		Expr: fmt.Sprintf("topk(%d, 100 * sum by (namespace, pod) (rate(container_cpu_cfs_throttled_periods_total%s[%s])) / sum by (namespace, pod) (rate(container_cpu_cfs_periods_total%s[%s])))", resMaxSeries, c, w, c, w)}
	mem := resQuery{ID: resQMem, Range: true, Title: "bellek (working set)", Unit: "By",
		Expr: fmt.Sprintf("topk(%d, sum by (namespace, pod) (container_memory_working_set_bytes%s))", resMaxSeries, c)}
	rst := resQuery{ID: resQRestarts, Range: true, Title: "restart sayacı",
		Expr: fmt.Sprintf("topk(%d, sum by (namespace, pod) (kube_pod_container_status_restarts_total%s))", resMaxSeries, p)}
	lim := resQuery{ID: resQLimits, Expr: "sum by (namespace, pod, resource) (kube_pod_container_resource_limits" + r + ")"}
	req := resQuery{ID: resQRequests, Expr: "sum by (namespace, pod, resource) (kube_pod_container_resource_requests" + r + ")"}
	last := resQuery{ID: resQLastTerm, Expr: "max by (namespace, pod, reason) (kube_pod_container_status_last_terminated_reason" + p + " == 1)"}
	wait := resQuery{ID: resQWaiting, Expr: "max by (namespace, pod, reason) (kube_pod_container_status_waiting_reason" + p + " == 1)"}
	switch family {
	case resFamCPU:
		return []resQuery{cpu, thr, lim, req}
	case resFamMemory:
		return []resQuery{mem, lim, req, last}
	case resFamRestarts:
		return []resQuery{rst, last, wait}
	}
	return []resQuery{cpu, thr, mem, rst, lim, last, wait}
}

// resourcePodLookupMatch — SAF: hedef çözümünün kube_pod_info seçicisi.
// pod kipinde önek araması (tam ad da önektir), diğerlerinde pod regex'i.
func resourcePodLookupMatch(podRe, nsFilter string) string {
	return "kube_pod_info" + resPodSel(podRe, nsFilter, false, "")
}

// resourceCacheKey — SAF (CLAUDE.md "cache key hashes ALL inputs").
func resourceCacheKey(src, id, query, podRe string, rng bool, from, to time.Time, step int) string {
	return "cosre-res:v1:" + fnvStr(src, id, query, podRe, strconv.FormatBool(rng),
		fmt.Sprint(from.Unix()/60), fmt.Sprint(to.Unix()/60), fmt.Sprint(step))
}

// resourceLookupKey — SAF: hedef çözümü (etiket değerleri) önbelleği.
func resourceLookupKey(src, match string, to time.Time, limit int) string {
	return "cosre-res-pods:v1:" + fnvStr(src, match, fmt.Sprint(to.Unix()/60), fmt.Sprint(int64(resLabelLookback/time.Second)), fmt.Sprint(limit))
}

// resClusterSrc — SAF: önbellek anahtarının kaynak bileşeni.
func resClusterSrc(c thanos.ClusterConfig) string {
	return "thanos:" + c.EffectiveID() + "|" + c.Name
}

// ─── Konsol sonucu çözücü (SAF) ─────────────────────────────────────────

// resSeries — çözülmüş seri: etiketler + [ms, değer] noktaları.
type resSeries struct {
	Labels map[string]string `json:"labels"`
	Points [][2]float64      `json:"points"`
}

// decodeResResult — SAF: Prometheus data.result (matrix | vector) → seriler.
// NaN/Inf noktalar atılır; bozuk sayı hata.
func decodeResResult(resultType string, raw json.RawMessage) ([]resSeries, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	switch resultType {
	case "matrix", "vector":
	default:
		return nil, fmt.Errorf("beklenmeyen sonuç türü %q", resultType)
	}
	var rows []struct {
		Metric map[string]string   `json:"metric"`
		Value  []json.RawMessage   `json:"value"`
		Values [][]json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("sonuç çözülemedi: %w", err)
	}
	pair := func(p []json.RawMessage) ([2]float64, bool, error) {
		if len(p) != 2 {
			return [2]float64{}, false, fmt.Errorf("nokta biçimi bozuk")
		}
		var ts float64
		if err := json.Unmarshal(p[0], &ts); err != nil {
			return [2]float64{}, false, err
		}
		var vs string
		if err := json.Unmarshal(p[1], &vs); err != nil {
			return [2]float64{}, false, err
		}
		v, err := strconv.ParseFloat(vs, 64)
		if err != nil {
			return [2]float64{}, false, err
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return [2]float64{}, false, nil
		}
		return [2]float64{math.Round(ts * 1000), v}, true, nil
	}
	out := make([]resSeries, 0, len(rows))
	for _, r := range rows {
		s := resSeries{Labels: r.Metric}
		if s.Labels == nil {
			s.Labels = map[string]string{}
		}
		if len(r.Value) > 0 {
			r.Values = append(r.Values, r.Value)
		}
		for _, p := range r.Values {
			pt, ok, err := pair(p)
			if err != nil {
				return nil, fmt.Errorf("nokta çözülemedi: %w", err)
			}
			if ok {
				s.Points = append(s.Points, pt)
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// resChartSeries — SAF: çözülmüş seriler → grafik serileri (metric_chart'ın
// ≤20 seri / ≤120 nokta kesimi; seri adı pod).
func resChartSeries(in []resSeries, unit string) ([]mcptools.EvChartSeries, bool) {
	conv := make([]chstore.SpanMetricSeries, 0, len(in))
	for _, s := range in {
		sm := chstore.SpanMetricSeries{GroupKey: []string{`pod="` + s.Labels["pod"] + `"`}}
		for _, p := range s.Points {
			sm.Points = append(sm.Points, chstore.SpanMetricPoint{Time: int64(p[0]) * 1e6, Value: p[1]})
		}
		conv = append(conv, sm)
	}
	return metricChartSeries(conv, unit, "pod")
}

// ─── İstatistik (SAF) ───────────────────────────────────────────────────

// resPodStats — tek pod'un deterministik özeti (yalnız okunan aileler dolu).
type resPodStats struct {
	Pod, Namespace string

	HasCPU           bool
	CPU              metricChartSeriesStats
	CPULimit, CPUReq float64 // core; 0 = yok
	CPUAboveS        int64   // limitin uyarı eşiği üstünde geçen süre (sn)

	HasThrottle              bool
	ThrottleMax, ThrottleAvg float64

	HasMem           bool
	Mem              metricChartSeriesStats
	MemLimit, MemReq float64 // bayt; 0 = yok
	MemSlopeH        float64 // en küçük kareler eğimi, bayt/saat
	MemAboveS        int64

	HasRestarts bool
	Restarts    int
	LastReason  string // son sonlanma sebebi (OOMKilled …)
	Waiting     string // şu anki bekleme sebebi (CrashLoopBackOff …)
}

// CPUPctMax / MemPctMax — limit yüzdesi (tepe); limit yoksa -1.
func (p resPodStats) CPUPctMax() float64 { return resPct(p.CPU.Max, p.CPULimit, p.HasCPU) }
func (p resPodStats) MemPctMax() float64 { return resPct(p.Mem.Max, p.MemLimit, p.HasMem) }

func resPct(v, lim float64, has bool) float64 {
	if !has || lim <= 0 {
		return -1
	}
	return 100 * v / lim
}

// resSlopePerHour — SAF: en küçük kareler eğimi (değer/saat); <2 nokta → 0.
func resSlopePerHour(pts [][2]float64) float64 {
	n := float64(len(pts))
	if n < 2 {
		return 0
	}
	// x ilk noktaya göre (saat): epoch ölçeğinde n·Σx² − (Σx)² sadeleşmesi
	// float64 hassasiyetini yer (ölçülen sapma ~%0.1).
	var sx, sy, sxx, sxy float64
	for _, p := range pts {
		x := (p[0] - pts[0][0]) / 3.6e6
		sx += x
		sy += p[1]
		sxx += x * x
		sxy += x * p[1]
	}
	d := n*sxx - sx*sx
	if d == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / d
}

// resAboveS — SAF: değer/limit ≥ eşik% olan nokta sayısı × adım (sn).
func resAboveS(pts [][2]float64, limit, thrPct float64, stepS int) int64 {
	if limit <= 0 {
		return 0
	}
	var n int64
	for _, p := range pts {
		if 100*p[1]/limit >= thrPct {
			n++
		}
	}
	return n * int64(stepS)
}

// resCounterIncrease — SAF: sayaç serisinin pencere içi artışı (pozitif
// adımların toplamı; sıfırlanma negatif adımı sayılmaz).
func resCounterIncrease(pts [][2]float64) int {
	inc := 0.0
	for i := 1; i < len(pts); i++ {
		if d := pts[i][1] - pts[i-1][1]; d > 0 {
			inc += d
		}
	}
	return int(math.Round(inc))
}

// resBuildStats — SAF: sorgu kimliği → çözülmüş seriler ⇒ pod başına özet.
// Anahtar namespace/pod; yalnız anlık sorguda görünen pod (ör. CrashLoop'ta
// kullanım serisi olmayan) da satır alır.
func resBuildStats(results map[string][]resSeries, stepS int) []resPodStats {
	by := map[string]*resPodStats{}
	get := func(l map[string]string) *resPodStats {
		k := l["namespace"] + "/" + l["pod"]
		p := by[k]
		if p == nil {
			p = &resPodStats{Pod: l["pod"], Namespace: l["namespace"]}
			by[k] = p
		}
		return p
	}
	for _, s := range results[resQLimits] {
		if v, ok := resInstantVal(s); ok {
			p := get(s.Labels)
			switch s.Labels["resource"] {
			case "cpu":
				p.CPULimit = v
			case "memory":
				p.MemLimit = v
			}
		}
	}
	for _, s := range results[resQRequests] {
		if v, ok := resInstantVal(s); ok {
			p := get(s.Labels)
			switch s.Labels["resource"] {
			case "cpu":
				p.CPUReq = v
			case "memory":
				p.MemReq = v
			}
		}
	}
	for _, s := range results[resQCPU] {
		if st, ok := metricChartStats(s.Points); ok {
			p := get(s.Labels)
			p.HasCPU, p.CPU = true, st
			p.CPUAboveS = resAboveS(s.Points, p.CPULimit, resourceThresholds.CPUWarn, stepS)
		}
	}
	for _, s := range results[resQThrottle] {
		if st, ok := metricChartStats(s.Points); ok {
			p := get(s.Labels)
			p.HasThrottle, p.ThrottleMax, p.ThrottleAvg = true, st.Max, st.Avg
		}
	}
	for _, s := range results[resQMem] {
		if st, ok := metricChartStats(s.Points); ok {
			p := get(s.Labels)
			p.HasMem, p.Mem = true, st
			p.MemSlopeH = resSlopePerHour(s.Points)
			p.MemAboveS = resAboveS(s.Points, p.MemLimit, resourceThresholds.MemWarn, stepS)
		}
	}
	for _, s := range results[resQRestarts] {
		if len(s.Points) > 0 {
			p := get(s.Labels)
			p.HasRestarts, p.Restarts = true, resCounterIncrease(s.Points)
		}
	}
	for _, s := range results[resQLastTerm] {
		if s.Labels["reason"] != "" {
			get(s.Labels).LastReason = s.Labels["reason"]
		}
	}
	for _, s := range results[resQWaiting] {
		if s.Labels["reason"] != "" {
			get(s.Labels).Waiting = s.Labels["reason"]
		}
	}
	out := make([]resPodStats, 0, len(by))
	for _, p := range by {
		if p.Pod == "" {
			continue
		}
		out = append(out, *p)
	}
	return out
}

func resInstantVal(s resSeries) (float64, bool) {
	if len(s.Points) == 0 {
		return 0, false
	}
	return s.Points[len(s.Points)-1][1], true
}

// resBreach — eşik aşımı satırı.
type resBreach struct {
	Pod  string
	Crit bool
	Text string
}

// resBreaches — SAF: eşik tablosuna göre aşımlar (pod sırası korunur).
func resBreaches(p resPodStats) []resBreach {
	t := resourceThresholds
	var out []resBreach
	level := func(v, warn, crit float64) (bool, bool) {
		if crit > 0 && v >= crit {
			return true, true
		}
		return v >= warn, false
	}
	if pct := p.CPUPctMax(); pct >= 0 {
		if hit, crit := level(pct, t.CPUWarn, t.CPUCrit); hit {
			out = append(out, resBreach{Pod: p.Pod, Crit: crit, Text: fmt.Sprintf("CPU tepe %s = limitin %%%.0f'i (eşik %%%.0f uyarı / %%%.0f kritik; uyarı eşiği üstünde ~%s)",
				fmtCores(p.CPU.Max), pct, t.CPUWarn, t.CPUCrit, fmtDurS(p.CPUAboveS))})
		}
	}
	if p.HasThrottle && p.ThrottleMax >= t.ThrottleWarn {
		out = append(out, resBreach{Pod: p.Pod, Text: fmt.Sprintf("CPU throttle tepe %%%.0f (ort %%%.0f; eşik %%%.0f)", p.ThrottleMax, p.ThrottleAvg, t.ThrottleWarn)})
	}
	if pct := p.MemPctMax(); pct >= 0 {
		if hit, crit := level(pct, t.MemWarn, t.MemCrit); hit {
			txt := fmt.Sprintf("bellek tepe %s = limitin %%%.0f'i (eşik %%%.0f uyarı / %%%.0f kritik; uyarı eşiği üstünde ~%s)",
				fmtMetricChartValue(p.Mem.Max, "By"), pct, t.MemWarn, t.MemCrit, fmtDurS(p.MemAboveS))
			if p.MemSlopeH > 0 {
				txt += "; eğim " + fmtSlopeBytes(p.MemSlopeH)
			}
			out = append(out, resBreach{Pod: p.Pod, Crit: crit, Text: txt})
		}
	}
	if p.HasRestarts && p.Restarts >= t.RestartWarn {
		txt := fmt.Sprintf("pencerede %d restart (eşik >%d uyarı / ≥%d kritik)", p.Restarts, t.RestartWarn-1, t.RestartCrit)
		if p.LastReason != "" {
			txt += "; son sonlanma sebebi " + p.LastReason
		}
		out = append(out, resBreach{Pod: p.Pod, Crit: p.Restarts >= t.RestartCrit, Text: txt})
	}
	if p.Waiting != "" {
		out = append(out, resBreach{Pod: p.Pod, Crit: true, Text: "şu an bekleme sebebi " + p.Waiting})
	}
	return out
}

// resSortStats — SAF: önce kritik, sonra uyarı sayısı; sonra bellek ve CPU
// limit yüzdesi, restart; eşitlikte ad (deterministik).
func resSortStats(ps []resPodStats) {
	score := func(p resPodStats) (int, int) {
		c, w := 0, 0
		for _, b := range resBreaches(p) {
			if b.Crit {
				c++
			} else {
				w++
			}
		}
		return c, w
	}
	sort.SliceStable(ps, func(i, j int) bool {
		ci, wi := score(ps[i])
		cj, wj := score(ps[j])
		switch {
		case ci != cj:
			return ci > cj
		case wi != wj:
			return wi > wj
		case ps[i].MemPctMax() != ps[j].MemPctMax():
			return ps[i].MemPctMax() > ps[j].MemPctMax()
		case ps[i].CPUPctMax() != ps[j].CPUPctMax():
			return ps[i].CPUPctMax() > ps[j].CPUPctMax()
		case ps[i].Restarts != ps[j].Restarts:
			return ps[i].Restarts > ps[j].Restarts
		}
		return ps[i].Namespace+"/"+ps[i].Pod < ps[j].Namespace+"/"+ps[j].Pod
	})
}

// fmtCores — SAF: çekirdek (0.42 core; çok küçükte 3 ondalık).
func fmtCores(v float64) string {
	if math.Abs(v) < 0.01 {
		return strconv.FormatFloat(v, 'f', 3, 64) + " core"
	}
	return strconv.FormatFloat(v, 'f', 2, 64) + " core"
}

// fmtDurS — SAF: saniye → "6 dk" / "1 sa 10 dk" / "45 sn".
func fmtDurS(s int64) string {
	switch {
	case s <= 0:
		return "0 dk"
	case s < 60:
		return fmt.Sprintf("%d sn", s)
	case s < 3600:
		return fmt.Sprintf("%d dk", s/60)
	}
	if m := (s % 3600) / 60; m > 0 {
		return fmt.Sprintf("%d sa %d dk", s/3600, m)
	}
	return fmt.Sprintf("%d sa", s/3600)
}

// fmtSlopeBytes — SAF: bayt/saat eğimi işaretli ("+45 MiB/sa").
func fmtSlopeBytes(v float64) string {
	sign := "+"
	if v < 0 {
		sign = "-"
	}
	return sign + fmtMetricChartValue(math.Abs(v), "By") + "/sa"
}

// resPctTR — SAF: limit yüzdesi ya da "limit yok".
func resPctTR(pct float64) string {
	if pct < 0 {
		return "limit yok"
	}
	return fmt.Sprintf("limitin %%%.0f'i", pct)
}

// resPodLineTR — SAF: pod'un tek satırlık özeti (yalnız okunan aileler).
func resPodLineTR(p resPodStats, loc *time.Location) string {
	hm := func(ms int64) string { return time.UnixMilli(ms).In(loc).Format("15:04") }
	trendTR := map[string]string{"up": "yükseliyor", "down": "düşüyor", "flat": "yatay"}
	var parts []string
	if p.HasCPU {
		s := fmt.Sprintf("CPU son %s, tepe %s (%s; %s)", fmtCores(p.CPU.Last), fmtCores(p.CPU.Max), hm(p.CPU.MaxAtMs), resPctTR(p.CPUPctMax()))
		if p.CPUReq > 0 {
			s += fmt.Sprintf(", ort isteğin %%%.0f'i", 100*p.CPU.Avg/p.CPUReq)
		}
		parts = append(parts, s)
	}
	if p.HasThrottle {
		parts = append(parts, fmt.Sprintf("throttle tepe %%%.0f", p.ThrottleMax))
	}
	if p.HasMem {
		s := fmt.Sprintf("bellek son %s, tepe %s (%s; %s), eğilim %s (%s)", fmtMetricChartValue(p.Mem.Last, "By"), fmtMetricChartValue(p.Mem.Max, "By"),
			hm(p.Mem.MaxAtMs), resPctTR(p.MemPctMax()), trendTR[p.Mem.Trend], fmtSlopeBytes(p.MemSlopeH))
		parts = append(parts, s)
	}
	if p.HasRestarts || p.LastReason != "" || p.Waiting != "" {
		s := fmt.Sprintf("restart %d", p.Restarts)
		if !p.HasRestarts {
			s = "restart sayacı okunmadı"
		}
		var why []string
		if p.LastReason != "" {
			why = append(why, "son sonlanma "+p.LastReason)
		}
		if p.Waiting != "" {
			why = append(why, "bekleme "+p.Waiting)
		}
		if len(why) > 0 {
			s += " (" + strings.Join(why, "; ") + ")"
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		parts = append(parts, "seri yok")
	}
	return "- " + p.Pod + ": " + strings.Join(parts, " · ")
}

// renderResourceSummaryTR — SAF: modelin okuduğu "KAYNAK ÖZETİ" bloğu.
// Gövde ≤12 satır: önce pod satırları (≤6, kritik önce), sonra EŞİK AŞIMI.
func renderResourceSummaryTR(ps []resPodStats, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	sorted := append([]resPodStats(nil), ps...)
	resSortStats(sorted)
	var b strings.Builder
	b.WriteString("KAYNAK ÖZETİ (deterministik; model noktaları yeniden sayma, yüzde/eğim yeniden hesaplama):\n")
	if len(sorted) == 0 {
		b.WriteString("- veri yok (seçici bu pencerede seri döndürmedi)\n")
		b.WriteString("EŞİK AŞIMI: değerlendirilemedi (veri yok)\n")
		return b.String()
	}
	rows := 0
	shown := sorted
	if len(shown) > resPodRowsMax {
		shown = shown[:resPodRowsMax]
	}
	for _, p := range shown {
		b.WriteString(resPodLineTR(p, loc) + "\n")
		rows++
	}
	if extra := len(sorted) - len(shown); extra > 0 {
		fmt.Fprintf(&b, "- (+%d pod daha; eşik aşımı olanlar yukarıda önce)\n", extra)
		rows++
	}
	var all []resBreach
	for _, p := range sorted {
		all = append(all, resBreaches(p)...)
	}
	if len(all) == 0 {
		b.WriteString("EŞİK AŞIMI: yok (" + resourceThresholdsTR() + ")\n")
		return b.String()
	}
	b.WriteString("EŞİK AŞIMI (" + resourceThresholdsTR() + "):\n")
	rows++
	budget := resSummaryMaxRows - rows
	for i, br := range all {
		if i >= budget-1 && len(all)-i > 1 {
			fmt.Fprintf(&b, "- (+%d eşik satırı daha)\n", len(all)-i)
			break
		}
		lvl := "UYARI"
		if br.Crit {
			lvl = "KRİTİK"
		}
		fmt.Fprintf(&b, "- %s %s: %s\n", lvl, br.Pod, br.Text)
	}
	return b.String()
}

// ─── Okuyucu ────────────────────────────────────────────────────────────

// resourceThanosReader — demetin Thanos'tan okuduğu üç şey (*thanos.Service
// karşılar; testler sahte okuyucu verir). Hepsi okuma.
type resourceThanosReader interface {
	ConsoleQuery(ctx context.Context, c thanos.ClusterConfig, q thanos.ConsoleInstantQuery, lim thanos.ConsoleLimits) (*thanos.ConsoleResult, error)
	ConsoleQueryRange(ctx context.Context, c thanos.ClusterConfig, q thanos.ConsoleRangeQuery, lim thanos.ConsoleLimits) (*thanos.ConsoleResult, error)
	ConsoleLabelValues(ctx context.Context, c thanos.ClusterConfig, name string, q thanos.ConsoleMetaQuery, lim thanos.ConsoleLimits) (*thanos.ConsoleLabelsResult, error)
}

// resQueryResult — tek sorgunun sonucu (önbellek gövdesi; Err önbelleğe girmez).
type resQueryResult struct {
	Series     []resSeries `json:"series"`
	Effective  string      `json:"effective"`
	Truncated  bool        `json:"truncated,omitempty"`
	DurationMs int64       `json:"durationMs"`
	Err        error       `json:"-"`
}

// resourceRunQueries — sorgu kümesi TEK cluster'da: önbellek, yoksa ≤4
// paralel, TEK 8 sn son tarih (ConsoleLimits{Timeout 8 sn, MaxSeries 20}).
// Sonuç sırası qs sırası.
func (s *Server) resourceRunQueries(ctx context.Context, th resourceThanosReader, c thanos.ClusterConfig, qs []resQuery, podRe string, from, to time.Time, step int) []resQueryResult {
	out := make([]resQueryResult, len(qs))
	qctx, cancel := context.WithTimeout(ctx, resQueryTimeout)
	defer cancel()
	lim := thanos.ConsoleLimits{Timeout: resQueryTimeout, MaxSeries: resMaxSeries}
	sem := make(chan struct{}, resParallel)
	var wg sync.WaitGroup
	src := resClusterSrc(c)
	for i, q := range qs {
		key := resourceCacheKey(src, q.ID, q.Expr, podRe, q.Range, from, to, step)
		if s.metricChartCacheGet(ctx, key, &out[i]) {
			continue
		}
		wg.Add(1)
		go func(i int, q resQuery, key string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			start := time.Now()
			var res *thanos.ConsoleResult
			var err error
			if q.Range {
				res, err = th.ConsoleQueryRange(qctx, c, thanos.ConsoleRangeQuery{Query: q.Expr, Start: from, End: to, Step: time.Duration(step) * time.Second}, lim)
			} else {
				res, err = th.ConsoleQuery(qctx, c, thanos.ConsoleInstantQuery{Query: q.Expr, Time: to}, lim)
			}
			r := resQueryResult{DurationMs: time.Since(start).Milliseconds(), Effective: q.Expr}
			if err == nil && res != nil {
				r.Series, err = decodeResResult(res.ResultType, res.Result)
				r.Truncated = res.Truncated
				if res.EffectiveQuery != "" {
					r.Effective = res.EffectiveQuery
				}
			}
			if err != nil {
				r.Err = err
				out[i] = r
				return
			}
			s.metricChartCacheSet(ctx, key, r, resCacheTTL)
			out[i] = r
		}(i, q, key)
	}
	wg.Wait()
	return out
}

// resLookup — tek cluster'ın hedef çözümü (kube_pod_info pod değerleri).
type resLookup struct {
	Cluster   string
	Pods      []string
	Truncated bool
	Err       error `json:"-"`
}

// resourceLookupPods — etkin cluster'larda (≤4 paralel, 4 sn) son 24 sa
// kube_pod_info pod değerleri; en çok resPrefixHitsMax+1 (kesim nöbetçisi).
// 60 sn önbellek (anahtar: cluster, seçici, uç, pencere, tavan).
func (s *Server) resourceLookupPods(ctx context.Context, th resourceThanosReader, clusters []thanos.ClusterConfig, podRe string, to time.Time) []resLookup {
	out := make([]resLookup, len(clusters))
	lctx, cancel := context.WithTimeout(ctx, resLabelTimeout)
	defer cancel()
	sem := make(chan struct{}, resParallel)
	var wg sync.WaitGroup
	limit := resPrefixHitsMax + 1
	for i, c := range clusters {
		wg.Add(1)
		go func(i int, c thanos.ClusterConfig) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			match := resourcePodLookupMatch(podRe, c.NamespaceFilter)
			key := resourceLookupKey(resClusterSrc(c), match, to, limit)
			lk := resLookup{Cluster: c.Name}
			var cached resLookup
			if s.metricChartCacheGet(ctx, key, &cached) {
				cached.Cluster = c.Name
				out[i] = cached
				return
			}
			res, err := th.ConsoleLabelValues(lctx, c, "pod", thanos.ConsoleMetaQuery{Match: []string{match}, Start: to.Add(-resLabelLookback), End: to, Limit: limit},
				thanos.ConsoleLimits{Timeout: resLabelTimeout})
			if err != nil {
				lk.Err = err
				out[i] = lk
				return
			}
			if res != nil {
				lk.Pods, lk.Truncated = append([]string(nil), res.Values...), res.Truncated
			}
			sort.Strings(lk.Pods)
			s.metricChartCacheSet(ctx, key, lk, resCacheTTL)
			out[i] = lk
		}(i, c)
	}
	wg.Wait()
	return out
}

// Hedef kararının türleri.
const (
	resDecideRun        = "run"
	resDecideAskPod     = "ask_pod"
	resDecideAskCluster = "ask_cluster"
	resDecideNotFound   = "not_found"
	resDecideUnreadable = "unreadable"
)

// resTargetDecision — SAF hedef kararı.
type resTargetDecision struct {
	Kind     string
	Cluster  string
	Pod      string   // pod kipinde çözülen tam ad
	Options  []string // ask_pod: pod adları; ask_cluster: cluster adları
	More     bool     // aday listesi tavana takıldı
	Failed   []string // okunamayan cluster'lar (kısmi okuma)
	Searched []string // denenen cluster'lar
}

// decideResourceTarget — SAF: çözüm okumaları → koş / sor / bulunamadı.
// pod kipi: tam ad tek cluster'da → koş; birden çok cluster'da → cluster sor;
// tam ad yoksa önek eşleşmeleri: 1 → koş, 2–8 → pod sor, >8 → ilk 8 + More;
// 0 → bulunamadı. Servis/workload kipi: pod'u olan cluster sayısı 0/1/çok.
// Hiçbir cluster okunamadıysa unreadable (okunamadı ≠ yok).
func decideResourceTarget(kind, target string, looks []resLookup) resTargetDecision {
	var d resTargetDecision
	var ok []resLookup
	for _, l := range looks {
		d.Searched = append(d.Searched, l.Cluster)
		if l.Err != nil {
			d.Failed = append(d.Failed, l.Cluster)
			continue
		}
		ok = append(ok, l)
	}
	if len(ok) == 0 {
		d.Kind = resDecideUnreadable
		return d
	}
	if kind == resKindPod {
		var exact []string
		type hit struct{ cluster, pod string }
		var pref []hit
		seen := map[string]bool{}
		more := false
		for _, l := range ok {
			more = more || l.Truncated
			for _, p := range l.Pods {
				if p == target {
					exact = append(exact, l.Cluster)
				}
				if strings.HasPrefix(p, target) && !seen[p] {
					seen[p] = true
					pref = append(pref, hit{l.Cluster, p})
				}
			}
		}
		switch {
		case len(exact) == 1:
			d.Kind, d.Cluster, d.Pod = resDecideRun, exact[0], target
		case len(exact) > 1:
			d.Kind, d.Options = resDecideAskCluster, exact
		case len(pref) == 1 && !more:
			d.Kind, d.Cluster, d.Pod = resDecideRun, pref[0].cluster, pref[0].pod
		case len(pref) >= 1:
			sort.Slice(pref, func(i, j int) bool { return pref[i].pod < pref[j].pod })
			d.Kind, d.More = resDecideAskPod, more || len(pref) > resPrefixHitsMax
			for i, h := range pref {
				if i >= resPrefixHitsMax {
					break
				}
				d.Options = append(d.Options, h.pod)
			}
		default:
			d.Kind = resDecideNotFound
		}
		return d
	}
	var have []string
	for _, l := range ok {
		if len(l.Pods) > 0 {
			have = append(have, l.Cluster)
		}
	}
	switch len(have) {
	case 0:
		d.Kind = resDecideNotFound
	case 1:
		d.Kind, d.Cluster = resDecideRun, have[0]
	default:
		d.Kind, d.Options = resDecideAskCluster, have
	}
	return d
}
