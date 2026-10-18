package api

// v0.10.1337 — Feature D (operatör): servis raporu. "Bir servisi analiz
// ederken operasyonları, son 1 saatteki veya 3 saatteki teknik hata
// exceptions var mı ve logları analiz et, ona göre bir rapor çıkar. Teknik
// hatadaki birkaç trace'e hızlıca Ask CoSRE gibi bakıp birleştir, bütünle."

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/healthverdict"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestHasServiceReportSignal(t *testing.T) {
	for _, c := range []struct {
		msg  string
		want bool
	}{
		{"payment-service servisini analiz et", true},
		{"payment-service için rapor çıkar", true},
		{"checkout'u incele", true},
		{"checkout servisini değerlendir", true},
		{"analyze checkout", true},
		{"assess payments", true},
		{"reporting-service sağlığı nasıl", false}, // ad-şekilli jeton (ayraçlı) sinyal değil
		{"analytics-v2 hata oranı", false},
		{"checkout neden yavaş", false},
	} {
		if got := hasServiceReportSignal(guidedTokens(normalizeGuidedMsg(c.msg))); got != c.want {
			t.Errorf("hasServiceReportSignal(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

// TestServiceReportRouting — router sırası ve sınırları (TestRouteGuidedIntent
// tablosundaki v0.10.1337 satırlarının tamamlayıcısı).
func TestServiceReportRouting(t *testing.T) {
	cases := []struct {
		msg     string
		intent  guidedIntent
		service string
	}{
		{"payment-service servisini analiz et", guidedServiceReport, "payment-service"},
		{"payment-service için rapor çıkar", guidedServiceReport, "payment-service"},
		{"payment-service neden hatalı", guidedRootCause, "payment-service"},            // D7: neden → kök-neden
		{"payment-service'in durumu nasıl", guidedServiceHealth, "payment-service"},     // D7: durum → sağlık
		{"payment-service loglarını analiz et", guidedServiceHealth, "payment-service"}, // bugünkü rotası (log sinyali)
		{"payment-service deploy etkisini analiz et", guidedDeployImpact, "payment-service"},
		{"payment-service tüm podlarını ve operasyonlarını tara", guidedServiceHealth, "payment-service"}, // tarama önce
		{"takımımın servislerini analiz et", guidedMyServices, ""},
		{"açık problemleri analiz et", guidedProblems, ""},
	}
	for _, c := range cases {
		r := routeGuidedIntent(c.msg, guidedTestServices, guidedTestEnvs, nil, "")
		if r.Intent != c.intent || r.Service != c.service {
			t.Errorf("%q → %s/%q, want %s/%q", c.msg, r.Intent, r.Service, c.intent, c.service)
		}
	}
	// Öznesiz rapor isteği: "hangi servis?" — çipler rapor cümlesiyle.
	r := routeGuidedIntent("servisi analiz et, rapor çıkar", guidedTestServices, guidedTestEnvs, nil, "")
	if r.Intent != guidedAskService || r.AskIntent != guidedServiceReport {
		t.Fatalf("öznesiz rapor → %+v; ask_service/service_report bekleniyordu", r)
	}
	// Aile yakalandıysa sağlık gibi yan yana RED korunur (v0.9.192 davranışı).
	if r := routeGuidedIntent("mobile bff servisini analiz et", guidedTestServices, guidedTestEnvs, nil, ""); r.Intent != guidedFamilyHealth {
		t.Fatalf("aile → %+v; family_health bekleniyordu", r)
	}
	// Adaylı netleştirme: çip gidiş-dönüşü aynı rotaya.
	r = guidedRoute{Intent: guidedAskService, AskIntent: guidedServiceReport, ServiceOptions: []string{"mobile-bff", "ledger-service"}}
	for _, chip := range guidedSuggestions(r) {
		if got := routeGuidedIntent(chip, guidedTestServices, guidedTestEnvs, nil, ""); got.Intent != guidedServiceReport {
			t.Errorf("aday çipi %q → %s, service_report bekleniyordu", chip, got.Intent)
		}
	}
	if got := serviceIntentFor("payments'ı analiz et", guidedTokens("payments'ı analiz et")); got != guidedServiceReport {
		t.Errorf("serviceIntentFor = %s", got)
	}
}

// TestServiceReportFollowups — D7 çipleri + raporun kendi takip çipleri ve linkleri.
func TestServiceReportFollowups(t *testing.T) {
	svcs := []string{"payments"}
	for _, in := range []guidedIntent{guidedServiceHealth, guidedRootCause} {
		found := false
		for _, s := range guidedSuggestions(guidedRoute{Intent: in, Service: "payments"}) {
			if s == "payments servisini analiz et" {
				found = true
				if got := routeGuidedIntent(s, svcs, nil, nil, ""); got.Intent != guidedServiceReport || got.Service != "payments" {
					t.Errorf("%s çipi %q → %s/%s", in, s, got.Intent, got.Service)
				}
			}
		}
		if !found {
			t.Errorf("%s cevabında rapor çipi yok", in)
		}
	}
	want := []string{"payments neden yavaş?", "payments teknik hataları?", "payments hata logları?"}
	got := guidedSuggestions(guidedRoute{Intent: guidedServiceReport, Service: "payments"})
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("rapor çipleri %v", got)
	}
	wantIntent := []guidedIntent{guidedRootCause, guidedServiceExceptions, guidedLogErrors}
	for i, s := range got {
		if r := routeGuidedIntent(s, svcs, nil, nil, ""); r.Intent != wantIntent[i] {
			t.Errorf("%q → %s, want %s", s, r.Intent, wantIntent[i])
		}
	}
	links := guidedAnswerLinkTargets(guidedRoute{Intent: guidedServiceReport, Service: "pay ments"})
	hrefs := []string{"/service?name=pay+ments", "/service?name=pay+ments&tab=operations", "/problems?service=pay+ments"}
	if len(links) != 3 {
		t.Fatalf("linkler %v", links)
	}
	for i, l := range links {
		if l.Href != hrefs[i] || l.Label == "" {
			t.Errorf("link %d = %+v, want %s", i, l, hrefs[i])
		}
	}
	if askServiceChip(guidedServiceReport, "payments") != "payments servisini analiz et" {
		t.Error("ask çipi rapor cümlesi olmalı")
	}
}

func opRow(name string, calls, errs uint64, p99, prior float64) chstore.OperationSummary {
	o := chstore.OperationSummary{Name: name, SpanCount: calls, ErrorCount: errs, P99Ms: p99}
	if calls > 0 {
		o.ErrorRate = float64(errs) / float64(calls) * 100
	}
	if prior > 0 {
		o.HasPrior, o.PriorP99Ms = true, prior
	}
	return o
}

func TestRankOpDeltas(t *testing.T) {
	ops := []chstore.OperationSummary{
		opRow("a", 100, 0, 900, 900),  // ×1
		opRow("b", 100, 5, 800, 100),  // ×8
		opRow("c", 100, 50, 700, 0),   // kıyas yok
		opRow("d", 10, 90, 5000, 100), // <20 çağrı: yavaşta yok, hatalıda var
		opRow("e", 100, 0, 600, 200),  // ×3
		opRow("f", 100, 1, 500, 100),  // ×5
		opRow("g", 100, 0, 100, 10),   // p99 ilk 5'te değil
	}
	slow, errs := rankOpDeltas(ops)
	var names []string
	for _, o := range slow {
		names = append(names, o.Name)
	}
	if got := strings.Join(names, ","); got != "b,f,e,a,c" {
		t.Errorf("slow sırası %s, want b,f,e,a,c (ilk 5 p99 → kat sırası, kıyassız sonda)", got)
	}
	if slow[0].Ratio != 8 || slow[4].Ratio != 0 || slow[4].HasPrior {
		t.Errorf("kat alanları %+v / %+v", slow[0], slow[4])
	}
	names = nil
	for _, o := range errs {
		names = append(names, o.Name)
	}
	if got := strings.Join(names, ","); got != "d,c,b,f" {
		t.Errorf("erroring sırası %s, want d,c,b,f", got)
	}
	// Girdi tavanı: 500 sonrası satır okunmaz.
	big := make([]chstore.OperationSummary, 0, 600)
	for i := 0; i < 600; i++ {
		big = append(big, opRow("x", 100, 0, 10, 0))
	}
	big[550] = opRow("late", 100, 99, 99999, 0)
	slow, errs = rankOpDeltas(big)
	for _, o := range append(slow, errs...) {
		if o.Name == "late" {
			t.Fatal("500 sonrası satır sıralamaya girmemeli")
		}
	}
	if s, e := rankOpDeltas(nil); s == nil || e == nil || len(s)+len(e) != 0 {
		t.Error("boş girdi boş (nil değil) dilim döner")
	}
}

func TestPickReportTraces(t *testing.T) {
	slow := []chstore.TraceRow{
		{TraceID: "s2", DurationMs: 800},
		{TraceID: "s1", DurationMs: 900},
		{TraceID: "s3", DurationMs: 700, HasError: true, RootName: "POST /pay"},
	}
	errRows := []chstore.TraceRow{
		{TraceID: "e1", DurationMs: 600, HasError: true, RootName: "GET /x"},
		{TraceID: "e2", DurationMs: 500, HasError: true, RootName: "POST /pay"},
	}
	ex := exceptionGroupsEvidence{Groups: []exGroupLine{
		{Fingerprint: "span1", Type: "java.io.IOException", SampleTraceIDs: []string{"s1", "x1"}}, // s1 zaten seçili → x1
		{Fingerprint: "ora:1", Oracle: true, Code: "COR-1", Operation: "OP", HourKnown: true, LastHour: 90, PrevHour: 10, SampleTraceIDs: []string{"o1"}},
		{Fingerprint: "span2", Type: "Other", SampleTraceIDs: []string{"z1"}},
	}}
	got := pickReportTraces(slow, errRows, ex, "POST /pay")
	ids := func(cs []drillCandidate) string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Row.TraceID)
		}
		return strings.Join(out, ",")
	}
	if ids(got) != "s1,o1,x1" {
		t.Fatalf("exceptions varken %s, want s1,o1,x1 (en yavaş + patlayan Oracle önce + farklı grup, tekrarsız)", ids(got))
	}
	if !strings.HasPrefix(got[1].Label, "teknik hata örneği · COR-1") || got[0].Label != "en yavaş" {
		t.Errorf("etiketler %q / %q", got[0].Label, got[1].Label)
	}
	// Exceptions yok: 2 en yavaş + en hatalı operasyonun hatalı trace'i.
	got = pickReportTraces(slow, errRows, exceptionGroupsEvidence{}, "POST /pay")
	if ids(got) != "s1,s2,e2" || got[2].Label != "en hatalı operasyonun hatalı trace'i" {
		t.Fatalf("exceptions yokken %s %+v", ids(got), got)
	}
	// Exceptions OKUNAMADI: örnek kullanılmaz (fallback); en hatalı op yoksa en yavaş hatalı.
	got = pickReportTraces(slow, errRows, exceptionGroupsEvidence{Err: "timeout", Groups: ex.Groups}, "")
	if ids(got) != "s1,s2,e1" || got[2].Label != "en yavaş hatalı" {
		t.Fatalf("okunamadı: %s %+v", ids(got), got)
	}
	// Tek örnek: en yavaş + 1 örnek + hatalı tamamlama.
	one := exceptionGroupsEvidence{Groups: []exGroupLine{{Type: "T", SampleTraceIDs: []string{"q1"}}}}
	if g := pickReportTraces(slow, errRows, one, "POST /pay"); ids(g) != "s1,q1,e2" {
		t.Fatalf("tek örnek: %s", ids(g))
	}
	if g := pickReportTraces(nil, nil, exceptionGroupsEvidence{}, ""); len(g) != 0 {
		t.Fatal("trace yoksa boş")
	}
	if g := pickReportTraces(slow, errRows, ex, "POST /pay"); len(g) > serviceReportTracesMax {
		t.Fatal("≤3")
	}
}

func TestServiceReportVerdict(t *testing.T) {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	healthy := reportRED{Cur: healthverdict.RED{Spans: 5000, ErrorRate: 0.1, P99Ms: 120}, Base: healthverdict.RED{Spans: 5000, ErrorRate: 0.1, P99Ms: 120}}
	if l, r := serviceReportVerdict(healthy, reportOpsView{}, exceptionGroupsEvidence{Groups: []exGroupLine{}, at: now}, nil); l != healthverdict.LabelHealthy || len(r) == 0 {
		t.Fatalf("sağlıklı: %s %v", l, r)
	}
	burst := exceptionGroupsEvidence{at: now, Groups: []exGroupLine{{Oracle: true, Code: "COR-30518", HourKnown: true, LastHour: 900, PrevHour: 200, LastSeen: now.Add(-4 * time.Minute).UnixNano()}}}
	if l, r := serviceReportVerdict(healthy, reportOpsView{}, burst, nil); l != healthverdict.LabelCrit || !strings.Contains(strings.Join(r, ";"), "COR-30518 son 1 sa 900 / önceki 1 sa 200") {
		t.Fatalf("patlama: %s %v", l, r)
	}
	small := exceptionGroupsEvidence{at: now, Groups: []exGroupLine{{Oracle: true, Code: "C", HourKnown: true, LastHour: 3, PrevHour: 1, LastSeen: now.UnixNano()}}}
	if l, _ := serviceReportVerdict(healthy, reportOpsView{}, small, nil); l != healthverdict.LabelWarn {
		t.Fatalf("küçük patlama Dikkat: %s", l)
	}
	recent := exceptionGroupsEvidence{at: now, Groups: []exGroupLine{{Type: "IOException", LastSeen: now.Add(-10 * time.Minute).UnixNano()}}}
	if l, _ := serviceReportVerdict(healthy, reportOpsView{}, recent, nil); l != healthverdict.LabelWarn {
		t.Fatalf("son 1 saatte grup Dikkat: %s", l)
	}
	p1 := exceptionGroupsEvidence{at: now, Groups: []exGroupLine{{Type: "X", Priority: "P1", LastSeen: now.Add(-time.Minute).UnixNano()}}}
	if l, _ := serviceReportVerdict(healthy, reportOpsView{}, p1, nil); l != healthverdict.LabelCrit {
		t.Fatalf("P1 son 1 sa Sorunlu: %s", l)
	}
	old := exceptionGroupsEvidence{at: now, Groups: []exGroupLine{{Type: "X", LastSeen: now.Add(-2 * time.Hour).UnixNano()}}}
	if l, r := serviceReportVerdict(healthy, reportOpsView{}, old, nil); l != healthverdict.LabelHealthy || !strings.Contains(strings.Join(r, ";"), "son 1 saatte yok") {
		t.Fatalf("yalnız 3 sa grubu sağlığı bozmaz ama söylenir: %s %v", l, r)
	}
	// Okunamadı ≠ yok: en az Dikkat.
	if l, r := serviceReportVerdict(reportRED{Err: errors.New("timeout")}, reportOpsView{}, exceptionGroupsEvidence{}, nil); l != healthverdict.LabelWarn || !strings.Contains(r[0], "OKUNAMADI") {
		t.Fatalf("RED okunamadı: %s %v", l, r)
	}
	if l, _ := serviceReportVerdict(healthy, reportOpsView{}, exceptionGroupsEvidence{Err: "x"}, nil); l != healthverdict.LabelWarn {
		t.Fatalf("exceptions okunamadı: %s", l)
	}
	if l, _ := serviceReportVerdict(healthy, reportOpsView{Err: errors.New("x")}, exceptionGroupsEvidence{}, nil); l != healthverdict.LabelWarn {
		t.Fatalf("ops okunamadı: %s", l)
	}
	ops := reportOpsView{Slow: []reportOp{{Name: "POST /pay", P99Ms: 2400, Ratio: 8}}}
	if l, r := serviceReportVerdict(healthy, ops, exceptionGroupsEvidence{}, nil); l != healthverdict.LabelWarn || !strings.Contains(strings.Join(r, ";"), "POST /pay") {
		t.Fatalf("yavaş operasyon: %s %v", l, r)
	}
	drill := traceDrill{Label: "en yavaş", Pick: drillPick{OK: true}, Fail: drillFailure{HTTPCode: 504}, Peer: peerRef{Target: "auth-gateway"}}
	if l, r := serviceReportVerdict(healthy, reportOpsView{}, exceptionGroupsEvidence{}, []traceDrill{drill}); l != healthverdict.LabelWarn || !strings.Contains(strings.Join(r, ";"), "auth-gateway") {
		t.Fatalf("trace hatası: %s %v", l, r)
	}
	bad := reportRED{Cur: healthverdict.RED{Spans: 5000, ErrorRate: 9, P99Ms: 120}, Base: healthverdict.RED{Spans: 5000, ErrorRate: 0.1, P99Ms: 120}}
	if l, _ := serviceReportVerdict(bad, reportOpsView{}, exceptionGroupsEvidence{}, nil); l != healthverdict.LabelCrit {
		t.Fatalf("hata oranı %%9 Sorunlu: %s", l)
	}
}

func reportFixture() serviceReportData {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	return serviceReportData{
		Service: "payment-service", RangeS: 3600, Now: now,
		Snapshot: "Servis: payment-service (son 60 dakika)\nRED: rate=42.0 span/s (tüm span türleri), error=7.20% (1240 hata), p50=85ms, p95=410ms, p99=1850ms\n",
		Ops:      reportOpsView{Read: 2, Slow: []reportOp{{Name: "POST /api/pay", Calls: 4120, P99Ms: 2400, PriorP99Ms: 300, Ratio: 8, ErrorRate: 21.6}}, Erroring: []reportOp{{Name: "POST /api/pay", Calls: 4120, Errors: 890, ErrorRate: 21.6}}},
		Ex: exceptionGroupsEvidence{at: now, LookbackS: 10800, Groups: []exGroupLine{{
			Fingerprint: "ora:1", Oracle: true, Code: "COR-30518", Operation: "PAYMENT_AUTH_REST", State: "new", Priority: "P1",
			HourKnown: true, LastHour: 900, PrevHour: 200, Occurrences: 12345, LastSeen: now.Add(-4 * time.Minute).UnixNano(),
			Sample: "Yetkilendirme servisi yanıt vermedi", SampleTraceIDs: []string{"4bf92f3577b34da6a3ce929d0e0e4736"}, Href: "/problems?exc=ora%3A1",
		}}},
		Cands:      []drillCandidate{{Row: chstore.TraceRow{TraceID: "0af7651916cd43dd8448eb211c80319c"}, Label: "en yavaş"}},
		Drills:     []traceDrill{{TraceID: "0af7651916cd43dd8448eb211c80319c", Label: "en yavaş", NotFound: true}},
		LogBackend: "elasticsearch",
		LogsPage: &logstore.Page{Logs: []*logstore.LogRecord{{
			Timestamp: now.Add(-5 * time.Minute).UnixNano(), Severity: 17, ServiceName: "payment-service", Body: "auth call failed: Read timed out", TraceID: "0af7651916cd43dd8448eb211c80319c",
		}}},
		Verdict: "Sorunlu", Reasons: []string{"hata oranı %7.20 (baseline %0.40)"},
	}
}

// TestRenderServiceReportTR — HÜKÜM en üstte, sabit bölüm sırası, KANIT
// satırları, ~6 KB tavan; OKUNAMADI / boş / depo yok her bölümde ayrı cümle.
func TestRenderServiceReportTR(t *testing.T) {
	d := reportFixture()
	out := renderServiceReportTR(d)
	if !strings.HasPrefix(out, "HÜKÜM (deterministik eşik, model değil): Sorunlu — hata oranı %7.20 (baseline %0.40).\n") {
		t.Fatalf("HÜKÜM ilk satır olmalı:\n%s", out)
	}
	last := -1
	for _, h := range []string{"OPERASYONLAR", "TEKNİK HATALAR (son 1 sa / 3 sa", "TRACE KANITI", "LOG KANITI", "AÇIK PROBLEMLER + HİPOTEZ", "KURAL:"} {
		i := strings.Index(out, h)
		if i < 0 || i < last {
			t.Fatalf("bölüm %q yok ya da sırası bozuk:\n%s", h, out)
		}
		last = i
	}
	for _, w := range []string{
		"KANIT: servis RED: rate=42.0",
		`KANIT: op "POST /api/pay" — p99 2.40 s (önceki 300 ms, ×8.0)`,
		"KANIT: 1. COR-30518 · PAYMENT_AUTH_REST · P1",
		"son 1 sa 900 · önceki 1 sa 200 → patlama (≥3×)",
		"örnek trace: 4bf92f3577b34da6a3ce929d0e0e4736",
		"KANIT: en yavaş: Trace 0af7651916cd43dd8448eb211c80319c trace deposunda BULUNAMADI",
		"log deposu: elasticsearch",
		"KANIT: log 09:55:00 ERROR payment-service auth call failed: Read timed out",
		"Açık problem yok — kayıtlı kök-neden hipotezi de yok.",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("kanıtta %q yok:\n%s", w, out)
		}
	}
	if strings.Contains(out, "Servis: payment-service (son 60 dakika)") {
		t.Error("snapshot başlığı tekrarlanmamalı (kapsam satırı var)")
	}

	// OKUNAMADI ≠ boş — her bölüm.
	bad := reportFixture()
	bad.Ops = reportOpsView{Err: errors.New("code: 159, timeout exceeded")}
	bad.Ex = exceptionGroupsEvidence{Err: "timeout"}
	bad.Drills, bad.Cands, bad.TracesErr = nil, nil, errors.New("timeout")
	bad.ProbErr = errors.New("timeout")
	o := renderServiceReportTR(bad)
	for _, w := range []string{"Operasyonlar OKUNAMADI", "Exceptions grupları OKUNAMADI", "Trace listesi OKUNAMADI", "Kırılan trace yok — log okunmadı.", "Problem kayıtları OKUNAMADI"} {
		if !strings.Contains(o, w) {
			t.Errorf("okunamadı metni %q yok:\n%s", w, o)
		}
	}
	empty := reportFixture()
	empty.Ops = reportOpsView{}
	empty.Ex = exceptionGroupsEvidence{Groups: []exGroupLine{}}
	empty.Drills, empty.Cands = nil, nil
	o = renderServiceReportTR(empty)
	for _, w := range []string{"Bu pencerede operasyon verisi yok.", "Grup yok: son 3 saatte", "Bu pencerede kırılacak trace yok."} {
		if !strings.Contains(o, w) {
			t.Errorf("boş metni %q yok:\n%s", w, o)
		}
	}
	if strings.Contains(o, "OKUNAMADI (") {
		t.Errorf("boş raporda okunamadı cümlesi olmamalı:\n%s", o)
	}
	// Loglar: depo yok / OKUNAMADI / log yok — üç ayrı cümle.
	ns := reportFixture()
	ns.LogsNoStore, ns.LogsPage, ns.LogBackend = true, nil, ""
	if o := renderReportLogsTR(ns); !strings.Contains(o, "depo yok") || !strings.Contains(o, "log deposu: yok") || strings.Contains(o, "OKUNAMADI") {
		t.Errorf("depo yok:\n%s", o)
	}
	le := reportFixture()
	le.LogsErr, le.LogsPage = errors.New("es: timeout"), nil
	if o := renderReportLogsTR(le); !strings.Contains(o, "Loglar OKUNAMADI") || !strings.Contains(o, "'log yok' DEMEK DEĞİL") {
		t.Errorf("log okunamadı:\n%s", o)
	}
	lz := reportFixture()
	lz.LogsPage = &logstore.Page{}
	if o := renderReportLogsTR(lz); !strings.Contains(o, "bu trace'e bağlı log kaydı YOK") || strings.Contains(o, "OKUNAMADI") {
		t.Errorf("log yok:\n%s", o)
	}
	// Tavan: büyük girdide bile ≤ ~6 KB ve HÜKÜM korunur.
	big := reportFixture()
	for i := 0; i < 40; i++ {
		big.Ops.Slow = append(big.Ops.Slow, reportOp{Name: strings.Repeat("GET /very/long/operation/name/", 4), Calls: 100, P99Ms: 1000})
		big.Ex.Groups = append(big.Ex.Groups, exGroupLine{Type: strings.Repeat("java.lang.VeryLongException", 3), Message: strings.Repeat("m", 300), LastSeen: d.Now.UnixNano(), Href: "/problems?exc=x"})
		big.LogsPage.Logs = append(big.LogsPage.Logs, &logstore.LogRecord{TraceID: "0af7651916cd43dd8448eb211c80319c", Body: strings.Repeat("b", 400), Severity: 17})
	}
	if o := renderServiceReportTR(big); len(o) > serviceReportEvidenceMax+64 || !strings.HasPrefix(o, "HÜKÜM") {
		t.Errorf("tavan aşıldı: %d bayt", len(o))
	}
}

func TestServiceReportPayload(t *testing.T) {
	d := reportFixture()
	d.Drills = []traceDrill{{TraceID: "0af7651916cd43dd8448eb211c80319c", Label: "en yavaş", Source: traceSourceTempo,
		Pick: drillPick{OK: true, SelfNs: 50e9, WallNs: 50.02e9, Share: 0.999, Span: chstore.SpanRow{Name: "POST", Kind: "client", ServiceName: "payment-service"}},
		Peer: peerRef{Kind: "http", Target: "auth-gateway", Path: "/v2/authorize"}, Fail: drillFailure{HTTPCode: 504, ExType: "java.net.SocketTimeoutException", ExMsg: "Read timed out"}}}
	p := buildServiceReportPayload(d, reportRED{Cur: healthverdict.RED{Spans: 10, ErrorRate: 7.2, P99Ms: 1850}}, []guidedAnswerLink{{Label: "x", Href: "/service?name=payment-service"}})
	if p.Verdict != "Sorunlu" || len(p.Drills) != 1 || len(p.Exceptions) != 1 || p.RED == nil || p.RED.P99Ms != 1850 || len(p.Links) != 1 {
		t.Fatalf("yük: %+v", p)
	}
	v := p.Drills[0]
	if !v.OK || v.Target != "auth-gateway" || v.HTTPCode != 504 || v.Error != "java.net.SocketTimeoutException: Read timed out" || v.LogsState != "ok" || len(v.Logs) != 1 || v.Source != "Tempo" {
		t.Fatalf("drill görünümü: %+v", v)
	}
	ns := d
	ns.LogsNoStore, ns.LogsPage = true, nil
	if v := buildTraceDrillViews(ns)[0]; v.LogsState != "no_store" || v.Logs == nil {
		t.Fatalf("depo yok görünümü: %+v", v)
	}
	er := d
	er.LogsErr, er.LogsPage = errors.New("x"), nil
	if v := buildTraceDrillViews(er)[0]; v.LogsState != "error" {
		t.Fatalf("okunamadı görünümü: %+v", v)
	}
	p = buildServiceReportPayload(d, reportRED{Err: errors.New("timeout")}, nil)
	if p.RED == nil || p.RED.Unavailable == "" || p.Links == nil {
		t.Fatalf("okunamayan RED: %+v", p.RED)
	}
}

func TestServiceReportCacheKey(t *testing.T) {
	from := time.Date(2026, 10, 18, 9, 0, 10, 0, time.UTC)
	to := from.Add(time.Hour)
	base := serviceReportCacheKey("svc", "prod", from, to)
	if serviceReportCacheKey("svc", "prod", from.Add(20*time.Second), to.Add(20*time.Second)) != base {
		t.Error("aynı dakika aynı anahtar")
	}
	for name, k := range map[string]string{
		"servis": serviceReportCacheKey("svc2", "prod", from, to),
		"ortam":  serviceReportCacheKey("svc", "uat", from, to),
		"from":   serviceReportCacheKey("svc", "prod", from.Add(-time.Hour), to),
		"to":     serviceReportCacheKey("svc", "prod", from, to.Add(time.Minute)),
		"ayraç":  serviceReportCacheKey("svcp", "rod", from, to),
	} {
		if k == base {
			t.Errorf("%s anahtara girmiyor", name)
		}
	}
}

// TestServiceReportCacheReplay — isabette adım çipleri aynı kanıtla yeniden
// yayınlanır (okuma yok, anlatım önbellekte yok).
func TestServiceReportCacheReplay(t *testing.T) {
	var kinds []string
	var results []map[string]any
	emit := withStepIDs(func(kind string, payload any) {
		kinds = append(kinds, kind)
		if kind == "step-result" {
			results = append(results, payload.(map[string]any))
		}
	})
	rec := &reportRecorder{emit: emit}
	a := rec.step("service_context", `{"service":"x"}`)
	b := rec.step("top_operations", `{"service":"x"}`)
	rec.done(a, "RED: ...", nil)
	rec.done(b, "", errors.New("timeout"))
	if len(rec.steps) != 2 || rec.steps[1].Err != "timeout" || rec.steps[0].Text != "RED: ..." {
		t.Fatalf("kayıt: %+v", rec.steps)
	}
	kinds, results = nil, nil
	replayReportSteps(emit, rec.steps)
	if strings.Join(kinds, ",") != "step,step-result,step,step-result" || len(results) != 2 || results[0]["ok"] != true || results[1]["ok"] != false {
		t.Fatalf("yeniden yayın: %v %v", kinds, results)
	}
}

// TestServiceReportReadOnly — rapor salt-okur: depo yazımı, denetim kaydı ve
// operatör oyu (zayıf sinyal; çalışma zamanı kararı vermez) bu dosyada yok.
func TestServiceReportReadOnly(t *testing.T) {
	b, err := os.ReadFile("copilot_service_report.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	for _, w := range []string{"Upsert", "Insert", "Save", "audit", "Vote", "vote", "Feedback", "feedback", "Exec(", "ALTER ", "DELETE"} {
		if strings.Contains(src, w) {
			t.Errorf("servis raporu %q içermemeli (salt-okur)", w)
		}
	}
	if strings.Contains(src, "os.Getenv") || strings.Contains(src, "COREMETRY_LOGS_BACKEND") {
		t.Error("log arka ucu env'den tahmin edilmez (yapılandırılmış depo)")
	}
	if strings.Contains(src, "SearchWithTimeout") || strings.Contains(src, "s.logs.Search(") {
		t.Error("ikinci log okuyucu yok — readDrillLogs (chat_trace_drill.go) kullanılır")
	}
	for _, w := range []string{"s.traceDrillFor(p3, c)", "s.readDrillLogs(p4, ids, lo, hi, from, to)", "s.exceptionGroupsEvidence(p1, exScope{Service: svc", "context.WithTimeout(ctx, serviceReportBudget)", "s.store.GetOperationSummaryCompared(p1, svc, 0, from, to, false, env)"} {
		if !strings.Contains(src, w) {
			t.Errorf("demet %q taşımalı (ortak dikiş)", w)
		}
	}
	if strings.Contains(src, "copilotStreamSurface") || strings.Contains(src, "copilotExplain") {
		t.Error("demet model çağırmaz — tek anlatım runGuidedRoute'ta")
	}
}

// TestServiceReportGoroutinesDoNotEmit — goroutine gövdeleri emit etmez.
func TestServiceReportGoroutinesDoNotEmit(t *testing.T) {
	b, err := os.ReadFile("copilot_service_report.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	re := regexp.MustCompile(`(?s)go func\([^)]*\) \{(.*?)\n\t+\}\(`)
	ms := re.FindAllStringSubmatch(src, -1)
	if len(ms) < 6 {
		t.Fatalf("goroutine sayısı %d, ≥6 bekleniyordu (faz 1 ×5 + faz 3)", len(ms))
	}
	for _, m := range ms {
		if strings.Contains(m[1], "emit") || strings.Contains(m[1], "rec.") {
			t.Errorf("goroutine emit etmemeli:\n%s", m[1])
		}
	}
}

func TestServiceReportWiring(t *testing.T) {
	g, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	gs := string(g)
	for _, w := range []string{
		"case guidedServiceReport: // v0.10.1337",
		"s.guidedServiceReportBundle(ctx, emit, route, from, to, rangeS)",
		"case svc != \"\" && serviceReportShape(toks):",
		"hasServiceReportSignal(toks) ||",
		"rangeS = serviceReportDefaultRangeS",
	} {
		if !strings.Contains(gs, w) {
			t.Errorf("copilot_guided.go %q taşımalı", w)
		}
	}
	if sw, wh := strings.Index(gs, "case svc != \"\" && hasSweepSignal(toks):"), strings.Index(gs, "case svc != \"\" && serviceReportShape(toks):"); sw < 0 || wh < sw || wh > strings.Index(gs, "case svc != \"\" && hasWhySignal(toks):") {
		t.Error("rapor dalı tarama ile neden dalı ARASINDA olmalı")
	}
	if guidedNarrationPrompt(guidedServiceReport) != copilot.SystemPromptServiceReport() {
		t.Error("anlatıcı SystemPromptServiceReport olmalı")
	}
	if intentAllowed["service_report"] != guidedServiceReport || !intentNeedsService[guidedServiceReport] {
		t.Error("sınıflandırıcı beyaz listesi + servis şartı")
	}
	if !strings.Contains(copilot.SystemPromptIntentClassify(), "- service_report") {
		t.Error("sınıflandırıcı prompt'u niyeti listelemeli (beyaz listeyle senkron)")
	}
	var got []map[string]any
	var seq blocks.Sequencer
	emit := withBlockSeq(func(kind string, payload any) {
		if kind == "block" {
			got = append(got, payload.(map[string]any))
		}
	}, &seq)
	emit(blocks.TypeServiceReport, serviceReportPayload{Service: "x"})
	if len(got) != 1 || got[0]["type"] != "service_report" {
		t.Fatalf("service_report olayı blok olmalı: %v", got)
	}
	if !disambigAskRescuable(guidedProblems) || disambigAskRescuable(guidedServiceReport) {
		t.Error("rapor netleştirmesi servis seçimini bekler (wiki kurtarması yok)")
	}
}

func TestDrillLogEnvelope(t *testing.T) {
	from, to := time.Unix(1000, 0), time.Unix(4600, 0)
	cands := []drillCandidate{{Row: chstore.TraceRow{TraceID: "a"}}, {Row: chstore.TraceRow{TraceID: "b", StartTime: 2000e9, DurationMs: 1000}}}
	drills := []traceDrill{{MinNs: 3000e9, MaxNs: 3001e9}, {}}
	lo, hi := drillLogEnvelope(cands, drills, from, to)
	if lo != 2000e9 || hi != 3001e9 {
		t.Fatalf("bilinen zarflar birleşimi: %d-%d", lo, hi)
	}
	lo, hi = drillLogEnvelope([]drillCandidate{{Row: chstore.TraceRow{TraceID: "x"}}}, []traceDrill{{}}, from, to)
	if lo != from.UnixNano() || hi != to.UnixNano() {
		t.Fatalf("bilinmeyen zarf pencereyi katar: %d-%d", lo, hi)
	}
	rows := reportTraceListRows([]drillCandidate{{Row: chstore.TraceRow{TraceID: "x", HasError: true}}}, []traceDrill{{MinNs: 5e9, MaxNs: 7e9, RootSvc: "svc", RootName: "GET /"}})
	if rows[0].StartTime != 5e9 || rows[0].DurationMs != 2000 || rows[0].ServiceName != "svc" || rows[0].RootName != "GET /" || !rows[0].HasError {
		t.Fatalf("örnek trace satırı: %+v", rows[0])
	}
}
