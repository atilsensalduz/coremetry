package api

// chat_cluster_compare_vm.go — v0.10.1376 (CoSRE küme kıyası, RED metrikten):
// prod trace'leri %1 örnekliyor; v0.10.1374'ün span'den saydığı ortam başına
// çağrı gerçek trafiğin ~%1'iydi ve "eu-a'da 12 çağrı" diye okunuyordu.
// VictoriaMetrics'teki http_server_request_duration_seconds histogramı
// örneklenmemiş ve kümeyi ortam etiketinde (varsayılan
// deployment_environment_name, değerler "prod-<küme>") taşıyor. Bu dosya KÜME
// KIYASI'nın RED satırlarını oradan okur:
//
//   - yalnız tek servis + http_route hedefinde (yol-yalnız / span adı hedefi
//     VM'de http_route olarak ifade edilemez → trace'ten, notla);
//   - ortam adları canlı etiket değerlerine çözülür (http_env_scope.go +
//     vmetrics.MatchEnvValues; önek toleranslı, belirsizlik görünür);
//   - ÜÇ anlık sorgu paralel, clusterVMBudget içinde: çağrı (durum kırılımlı),
//     p50, p95 — frontend windowQueries ile bayt bayt aynı şekil (altın dosya
//     frontend/src/lib/httpMetrics.envScope.golden.json);
//   - hiçbir ortam çözülmezse ya da VM okunamazsa span yoluna düşülür (notla);
//     kısmi çözümde çözülen ortamlar metrikten, kalanlar "metrikte eşleşen
//     ortam değeri yok" — bir blokta iki kaynak KARIŞMAZ. Sonuç önbelleği yok.

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// v0.10.1376 — VM okuma sabitleri.
const (
	clusterVMBudget        = 4 * time.Second // etiket değerleri + 3 anlık sorgu
	clusterVMCountMetric   = "http_server_request_duration_seconds_count"
	clusterVMBucketMetric  = "http_server_request_duration_seconds_bucket"
	clusterVMNoRouteNote   = "yol/span adı VM'de http_route olarak ifade edilemez — trace'ten"
	clusterVMNoMatchNote   = "metrikte hiçbir ortam eşleşmedi"
	clusterVMUnresolvedRow = "metrikte eşleşen ortam değeri yok"
)

// clusterVMReader — v0.10.1376: küme kıyasının VM'den okuduğu her şey
// (vmMetricSource karşılar; testler sahte okuyucu verir).
type clusterVMReader interface {
	httpEnvLabelReader
	QueryPromQLInstant(ctx context.Context, query string, at time.Time) ([]chstore.SpanMetricSeries, error)
}

// clusterVMPlan — SAF (v0.10.1376): RED metrikten okunabilir mi; okunamıyorsa
// (VM varken) neden. VM yoksa ya da servis çözülmediyse not boş (servis notu
// REDNote'ta zaten var).
func clusterVMPlan(t opTarget, svc string, vmOK bool) (bool, string) {
	switch {
	case !vmOK || svc == "":
		return false, ""
	case t.SearchPath != "" || !t.HasRoute || t.Route == "":
		return false, clusterVMNoRouteNote
	}
	return true, ""
}

// httpStatusLabels — SAF (v0.10.1376): durum adaylarının VM yazımı, sırayla
// (http_response_status_code, http_status_code).
func httpStatusLabels() []string {
	out := make([]string, len(httpStatusKeyCandidates))
	for i, k := range httpStatusKeyCandidates {
		out[i] = vmetrics.PromLabel(k)
	}
	return out
}

// clusterVMQuerySet — üç anlık sorgu + altın dosyanın pinlediği ara parçalar.
type clusterVMQuerySet struct {
	Sel        string `json:"serverSel"`
	TotalBy    string `json:"totalBy"`
	QuantileBy string `json:"quantileBy"`
	Total      string `json:"total"`
	P50        string `json:"p50"`
	P95        string `json:"p95"`
}

// clusterVMSelector — SAF (v0.10.1376): frontend serverSel ile aynı sıra —
// service_name, ortam eşleyicisi, http_route, http_request_method.
func clusterVMSelector(svc, envMatcher, route, method string) string {
	parts := []string{"service_name=" + vmetrics.PromStrTS(svc)}
	if envMatcher != "" {
		parts = append(parts, envMatcher)
	}
	if route != "" {
		parts = append(parts, "http_route="+vmetrics.PromStrTS(route))
	}
	if method != "" {
		parts = append(parts, "http_request_method="+vmetrics.PromStrTS(method))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// clusterVMQueries — SAF (v0.10.1376): W = max(60, round(pencere sn)).
// Çağrı: sum by (<L>, durum adayları) (increase(count[W])); yüzdelik:
// histogram_quantile(p, sum by (le, <L>) (increase(bucket[W]))) — frontend
// windowQueries(…, by).total / .p50 / .p95 ile bayt bayt aynı.
func clusterVMQueries(svc, route, method, label string, values []string, windowSec float64) clusterVMQuerySet {
	w := fmt.Sprintf("%ds", int64(math.Max(60, math.Round(windowSec))))
	sel := clusterVMSelector(svc, vmetrics.EnvMatcher(label, values), route, method)
	inc := func(m string) string { return "increase(" + m + sel + "[" + w + "])" }
	totalBy := strings.Join(append([]string{label}, httpStatusLabels()...), ", ")
	qt := func(p string) string {
		return "histogram_quantile(" + p + ", sum by (le, " + label + ") (" + inc(clusterVMBucketMetric) + "))"
	}
	return clusterVMQuerySet{
		Sel: sel, TotalBy: totalBy, QuantileBy: label,
		Total: "sum by (" + totalBy + ") (" + inc(clusterVMCountMetric) + ")",
		P50:   qt("0.5"), P95: qt("0.95"),
	}
}

// parseVMGroupKey — SAF (v0.10.1376): anlık sorgu serisinin GroupKey'i
// (`k="v"`, vmetrics labelSetGroupKey) → etiket haritası. Kaçış çözülür
// (\\, \", \n).
func parseVMGroupKey(gk []string) map[string]string {
	out := make(map[string]string, len(gk))
	for _, kv := range gk {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		v := kv[i+1:]
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = unquoteProm(v[1 : len(v)-1])
		}
		out[kv[:i]] = v
	}
	return out
}

// unquoteProm — SAF: quotePromString'in tersi.
func unquoteProm(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// vmSeriesValue — SAF: anlık serinin değeri; nokta yok / NaN / Inf → ok=false.
func vmSeriesValue(s chstore.SpanMetricSeries) (float64, bool) {
	if len(s.Points) == 0 {
		return 0, false
	}
	v := s.Points[len(s.Points)-1].Value
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// clusterVMRow — tek span ortamının metrik RED'i.
type clusterVMRow struct {
	RED           chstore.OpRED
	ErrorsUnknown bool
}

// clusterVMParse — SAF (v0.10.1376): span ortamı başına RED. Ortamın eşlenen
// TÜM VM değerleri toplanır: Calls = round(Σ çağrı), Errors = round(Σ 5xx).
// Durum anahtarı ortamın serilerinde boş-olmayan değeri olan İLK aday; hiçbiri
// yoksa ErrorsUnknown (hata 0 DEĞİL). Yüzdelik: değer başına
// histogram_quantile, birden çok değerde en yükseği (üst sınır — kovalar
// değerler arası birleştirilmez), ×1000 ms; NaN → 0. Measured ⇔ en az bir
// sonlu çağrı serisi.
func clusterVMParse(label string, total, p50, p95 []chstore.SpanMetricSeries, matches []vmetrics.EnvMatch) map[string]clusterVMRow {
	type lv struct {
		labels map[string]string
		v      float64
	}
	var tot []lv
	for _, s := range total {
		if v, ok := vmSeriesValue(s); ok {
			tot = append(tot, lv{parseVMGroupKey(s.GroupKey), v})
		}
	}
	quant := func(ser []chstore.SpanMetricSeries) map[string]float64 {
		out := map[string]float64{}
		for _, s := range ser {
			if v, ok := vmSeriesValue(s); ok {
				out[parseVMGroupKey(s.GroupKey)[label]] = v
			}
		}
		return out
	}
	q50, q95 := quant(p50), quant(p95)
	statusLabels := httpStatusLabels()
	out := make(map[string]clusterVMRow, len(matches))
	for _, m := range matches {
		if len(m.Values) == 0 {
			continue
		}
		in := make(map[string]bool, len(m.Values))
		for _, v := range m.Values {
			in[v] = true
		}
		var mine []lv
		for _, s := range tot {
			if in[s.labels[label]] {
				mine = append(mine, s)
			}
		}
		var row clusterVMRow
		if len(mine) == 0 {
			out[m.Want] = row
			continue
		}
		statusKey := ""
		for _, k := range statusLabels {
			for _, s := range mine {
				if s.labels[k] != "" {
					statusKey = k
					break
				}
			}
			if statusKey != "" {
				break
			}
		}
		var calls, errs float64
		for _, s := range mine {
			calls += s.v
			if statusKey != "" && isHTTP5xx(s.labels[statusKey]) {
				errs += s.v
			}
		}
		row.RED = chstore.OpRED{Calls: uint64(math.Round(math.Max(0, calls))), Errors: uint64(math.Round(math.Max(0, errs))), Measured: true}
		row.ErrorsUnknown = statusKey == ""
		for _, v := range m.Values {
			row.RED.P50Ms = math.Max(row.RED.P50Ms, q50[v]*1000)
			row.RED.P95Ms = math.Max(row.RED.P95Ms, q95[v]*1000)
		}
		out[m.Want] = row
	}
	return out
}

// clusterVMResult — metrik RED okuması.
type clusterVMResult struct {
	Label       string
	LabelSource string
	Matches     []vmetrics.EnvMatch
	Rows        map[string]clusterVMRow
	Resolved    int
}

// clusterVMRED — v0.10.1376: ortam etiketi + canlı değerler (önbellekli),
// eşleme, üç paralel anlık sorgu; hepsi clusterVMBudget içinde. Resolved=0 →
// sorgu atılmaz (çağıran span yoluna düşer). Herhangi bir okuma hatası → err.
func (s *Server) clusterVMRED(ctx context.Context, vm clusterVMReader, t opTarget, svc string, envs []string, from, to time.Time) (*clusterVMResult, error) {
	cctx, cancel := context.WithTimeout(ctx, clusterVMBudget)
	defer cancel()
	el, err := s.httpEnvLabelFor(cctx, vm, to)
	if err != nil {
		return nil, err
	}
	res := &clusterVMResult{Label: el.Label, LabelSource: el.Source, Matches: vmetrics.MatchEnvValues(envs, el.Values)}
	var values []string
	for _, m := range res.Matches {
		if len(m.Values) > 0 {
			res.Resolved++
			values = append(values, m.Values...)
		}
	}
	if res.Resolved == 0 {
		return res, nil
	}
	method := ""
	if t.MethodAsked {
		method = t.Method
	}
	qs := clusterVMQueries(svc, t.Route, method, el.Label, values, to.Sub(from).Seconds())
	queries := []string{qs.Total, qs.P50, qs.P95}
	out := make([][]chstore.SpanMetricSeries, len(queries))
	errs := make([]error, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			out[i], errs[i] = vm.QueryPromQLInstant(cctx, q, to)
		}(i, q)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	res.Rows = clusterVMParse(el.Label, out[0], out[1], out[2], res.Matches)
	return res, nil
}
