package api

// service_vm_red_charts.go — v0.10.1415 (operatör: "bir servisle ilgili tam
// rapor çekerken CoSRE VictoriaMetrics'ten HTTP grafiklerini getirsin.
// Response time ve throughput.").
//
// v0.10.1391 servis SAĞLIĞI cevabına VictoriaMetrics HTTP RED grafiklerini
// (istek hızı · 5xx % · p95/p99, http_server_request_duration_seconds_*)
// getirdi; servis RAPORU (copilot_service_report.go) bilinçli olarak grafiksiz
// kaldı (kart + trace listesi). Bu dosya iki şeyi taşır:
//
//  1. ORTAK grafik kurucusu svcHTTPREDViews — okunmuş svcHTTPREDResult'tan
//     grafik başına emit'e hazır görünüm (adım argümanı, kanıt metni,
//     ChatEvidence, veri grafiği yükü). Servis sağlığının emitServiceHTTPRED'i
//     ve servis raporu AYNI kurucuyu kullanır (kopya yok; okuma tarafı zaten
//     ortak: startServiceHTTPRED / readServiceHTTPRED).
//  2. Rapor tarafı: grafikler rapor KARTINA (serviceReportPayload.HTTPCharts)
//     girer — kartın özet satırlarından hemen sonra çizilir (FE
//     ServiceReportCard); kanıt çipi (PromQL kopyala + "Metrics'te aç") adım
//     şeridinde kalır ama ev_block YAYINLANMAZ (aynı grafik cevabın altında
//     ikinci kez açılmasın). Grafik başına deterministik özet
//     (metricChartStatsTR) rapor kanıtına "KANIT:" satırı olarak girer —
//     anlatım throughput / yanıt süresi için ÖRNEKLENMEMİŞ VM sayılarını alıntılar.
//     VM yok / histogram yok / servis serisi yok (yalnız gRPC sunan servis) /
//     okunamadı → grafik YOK + dürüst not; rapor zaten span grafiği basmadığı
//     için yedek grafik de UYDURULMAZ.
//
// Salt-okur: yazma, denetim, model çağrısı yok (TestServiceVMREDChartsReadOnly).

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// svcHTTPREDView — tek grafiğin emit'e hazır hâli (SAF kurucunun çıktısı).
type svcHTTPREDView struct {
	Kind  string
	Title string
	Unit  string
	Query string
	Href  string
	Args  string // metric_chart adım argümanı
	Text  string // adımın kanıt metni (başlık + PromQL + özet / hata)
	Stats string // metricChartStatsTR gövdesi ("- seri: …" satırları); çizilmediyse boş
	Err   error
	Drawn bool // okuma başarılı VE ≥1 seri
	Ev    mcptools.ChatEvidence
	Chart *mcptools.EvChartPayload // Err != nil ise nil
}

// svcHTTPREDViews — SAF (v0.10.1415, emitServiceHTTPRED'den çıkarıldı):
// okunmuş VM HTTP RED sonucunun grafik başına görünümü. Metin ve kanıt
// v0.10.1391'in yayınladığıyla bayt bayt aynı (TestEmitServiceHTTPRED).
func svcHTTPREDViews(service string, rangeS int64, res svcHTTPREDResult, loc *time.Location) []svcHTTPREDView {
	out := make([]svcHTTPREDView, 0, len(res.Charts))
	for _, c := range res.Charts {
		v := svcHTTPREDView{Kind: c.Kind, Title: c.Title, Unit: c.Unit, Query: c.Query, Href: c.Href, Err: c.Err}
		v.Args = withEnvArg(fmt.Sprintf(`{"template":%s,"service":%s,"range_s":%d}`, jsonStr("svc_http_red/"+c.Kind), jsonStr(service), rangeS), res.Env)
		var t strings.Builder
		fmt.Fprintf(&t, "%s — PromQL: %s\n", c.Title, c.Query)
		if c.Err != nil {
			fmt.Fprintf(&t, "VictoriaMetrics sorgusu BAŞARISIZ (okunamadı ≠ yok): %v — bu grafik YOK.\n", c.Err)
			v.Text = t.String()
			out = append(out, v)
			continue
		}
		if len(c.Series) == 0 {
			t.WriteString("Bu pencerede SERİ YOK — grafik YOK.\n")
		} else {
			v.Drawn = true
			stats := metricChartStatsTR(c.Series, c.Unit, loc)
			if _, body, ok := strings.Cut(stats, "\n"); ok {
				stats = body // tek "GRAFİK ÖZETİ" başlığı çağıranda
			}
			v.Stats = stats
			t.WriteString(stats)
		}
		v.Text = t.String()
		nSeries := len(c.Series)
		v.Ev = mcptools.ChatEvidence{Tool: "metric_chart", Source: "VictoriaMetrics", Query: c.Query, QueryKind: "promql",
			Window: mcptools.EvidenceWindow{FromMs: res.From.UnixMilli(), ToMs: res.To.UnixMilli()}, Series: &nSeries,
			DurationMs: c.DurationMs, Href: c.Href, Env: res.Env}
		v.Chart = &mcptools.EvChartPayload{Title: c.Title, DeepLink: c.Href,
			Window: mcptools.EvBlockWindow{FromNs: res.From.UnixNano(), ToNs: res.To.UnixNano()}, Series: c.Series, Truncated: c.Truncated}
		out = append(out, v)
	}
	return out
}

// ─── Servis raporu (v0.10.1415) ─────────────────────────────────────────

// serviceReportHTTPChart — rapor kartının VM HTTP grafiği (FE
// ChatServiceReportHTTPChart). Veri grafiği yükü (EvChartPayload; evidenceRef
// boş — kart kendisi çizer) + tür + "PromQL kopyala" için sorgu.
type serviceReportHTTPChart struct {
	mcptools.EvChartPayload
	Kind  string `json:"kind"`
	Unit  string `json:"unit"`
	Query string `json:"query"`
}

// svcReportHTTPOrder — kart / kanıt sırası: operatörün istediği throughput ve
// yanıt süresi önce, 5xx oranı (aynı histogramdan, bedava) sonra.
var svcReportHTTPOrder = map[string]int{svcHTTPREDRate: 0, svcHTTPREDLatency: 1, svcHTTPRED5xx: 2}

// svcReportHTTPLabel — SAF: kanıt satırının okunur adı.
func svcReportHTTPLabel(kind string) string {
	switch kind {
	case svcHTTPREDRate:
		return "HTTP throughput"
	case svcHTTPREDLatency:
		return "HTTP yanıt süresi"
	case svcHTTPRED5xx:
		return "HTTP 5xx oranı"
	}
	return kind
}

// reportHTTPViews — SAF: ortak kurucu + rapor sırası (throughput, yanıt süresi, 5xx).
func reportHTTPViews(service string, rangeS int64, res svcHTTPREDResult, loc *time.Location) []svcHTTPREDView {
	if !res.UseVM {
		return nil
	}
	vs := svcHTTPREDViews(service, rangeS, res, loc)
	sort.SliceStable(vs, func(i, j int) bool { return svcReportHTTPOrder[vs[i].Kind] < svcReportHTTPOrder[vs[j].Kind] })
	return vs
}

// reportHTTPCharts — SAF: kartın grafikleri (yalnız çizilenler) + çizilmediyse
// kartın dürüst notu.
func reportHTTPCharts(res svcHTTPREDResult, views []svcHTTPREDView) ([]serviceReportHTTPChart, string) {
	if !res.UseVM {
		return nil, res.Reason
	}
	var out []serviceReportHTTPChart
	for _, v := range views {
		if !v.Drawn || v.Chart == nil {
			continue
		}
		out = append(out, serviceReportHTTPChart{EvChartPayload: *v.Chart, Kind: v.Kind, Unit: v.Unit, Query: v.Query})
	}
	if len(out) == 0 {
		return nil, "VictoriaMetrics HTTP sorguları bu pencerede seri döndürmedi ya da okunamadı"
	}
	return out, ""
}

// svcReportHTTPNoteTR — SAF: VM grafiği çizilmeyen raporun kanıt notu. Rapor
// span grafiği basmaz; yedek grafik UYDURULMAZ.
func svcReportHTTPNoteTR(reason string) string {
	return "HTTP GRAFİKLERİ: YOK — VictoriaMetrics HTTP throughput / yanıt süresi grafikleri çizilmedi: " + reason +
		". Bu raporda grafik yok; servis RED ve operasyon sayıları span verisinden (örneklenmiş trace'ler).\n"
}

// renderReportHTTPTR — SAF: rapor kanıtının HTTP bölümü. Kaynak başlığı,
// notlar, grafik başına "KANIT:" özet satırları (örneklenmemiş), okunamayan
// grafik ayrı cümle; VM kipinde değilse dürüst not.
func renderReportHTTPTR(service string, rangeS int64, res svcHTTPREDResult, views []svcHTTPREDView) string {
	if !res.UseVM {
		return svcReportHTTPNoteTR(res.Reason)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP GRAFİKLERİ (%s, son %s; VictoriaMetrics, ÖRNEKLENMEMİŞ — http_server_request_duration_seconds_*; adım %ds; kartta çizildi):\n",
		service, fmtAgoTR(rangeS), res.Step)
	for _, nt := range res.Notes {
		b.WriteString("Not: " + nt + ".\n")
	}
	drawn := 0
	for _, v := range views {
		label := svcReportHTTPLabel(v.Kind)
		switch {
		case v.Err != nil:
			fmt.Fprintf(&b, "%s: VictoriaMetrics sorgusu OKUNAMADI (%s) — 'veri yok' DEMEK DEĞİL; sayı uydurma.\n", label, clipRunes(oneLine(v.Err.Error()), 120))
		case !v.Drawn:
			fmt.Fprintf(&b, "%s: bu pencerede seri yok.\n", label)
		default:
			drawn++
			for _, ln := range strings.Split(strings.TrimRight(v.Stats, "\n"), "\n") {
				if ln = strings.TrimPrefix(strings.TrimSpace(ln), "- "); ln != "" {
					fmt.Fprintf(&b, "KANIT: %s — %s\n", label, ln)
				}
			}
		}
	}
	if drawn > 0 {
		b.WriteString("KURAL (HTTP): throughput ve yanıt süresi için bu KANIT satırlarını esas al (örneklenmemiş); span SAYIMLARI örneklenmiş. " +
			"Noktaları yeniden sayma; grafik/chart çiti YAZMA.\n")
	}
	return b.String()
}

// reportHTTPSourcesTR — SAF: raporun kaynak satırına ek (yalnız VM kipinde).
func reportHTTPSourcesTR(charts []serviceReportHTTPChart) string {
	if len(charts) == 0 {
		return ""
	}
	return " + HTTP throughput / yanıt süresi grafikleri (VictoriaMetrics, örneklenmemiş)"
}

// recordReportHTTPSteps — v0.10.1415: grafik başına metric_chart adım çipi;
// kanıt (PromQL + pencere + seri + süre + "Metrics'te aç") kaydedilir ki
// önbellek isabetinde aynı çip yeniden yayınlansın. ev_block YOK (grafik kartta).
func recordReportHTTPSteps(rec *reportRecorder, views []svcHTTPREDView) {
	for _, v := range views {
		if v.Err != nil {
			k := rec.step("metric_chart", v.Args)
			rec.done(k, v.Text, v.Err)
			continue
		}
		rec.evidenceStep("metric_chart", v.Args, v.Text, v.Ev)
	}
}

// evidenceStep — kanıt zarflı guided adım (emitGuidedEvidenceResult, grafik
// bloğu YOK); kanıt kayda girer (replayReportSteps aynı zarfı yeniden basar).
func (r *reportRecorder) evidenceStep(tool, args, text string, ev mcptools.ChatEvidence) {
	ev.Ref = "" // ref yayın anında adım kimliğinden kurulur (ev<i>)
	e := ev
	r.steps = append(r.steps, reportStep{Tool: tool, Args: args, Text: text, Evidence: &e})
	i := emitGuidedStep(r.emit, tool, args)
	r.ids = append(r.ids, i)
	emitGuidedEvidenceResult(r.emit, i, tool, text, ev, nil)
}
