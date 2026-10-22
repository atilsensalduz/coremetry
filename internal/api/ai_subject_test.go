package api

// ai_subject_test.go — v0.10.1405 (Tek CoSRE adım 2). Operatör: "Trace ve
// exception üzerinden çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı
// drawer hissi, CoSRE bütünselliğine uymuyor."
//
// `?ai=` kodeğinin sunucu ikizi (parseAiSubject / formatAiSubject) istemcinin
// tablosunu (frontend/src/lib/aiSubject.test.ts) aynalar: her tür gidiş-dönüş
// eder, ':' içeren kimlik ayracı bozmaz, eksik/fazla segment / bozuk yüzde
// dizisi / ters pencere reddedilir. Açıklama turunun span / service-health /
// log öznesi bu kodekle gelir — iki taraf ayrışırsa sohbet yanlış özneyi açıklar.

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAiSubjectRoundTripMirrorsClient(t *testing.T) {
	ns := func(n int64) time.Time { return time.Unix(0, n) }
	for _, c := range []struct {
		name  string
		subj  aiSubject
		param string
	}{
		{"trace", aiSubject{Kind: "trace", ID: "1b67a4f0c9de"}, "trace:1b67a4f0c9de"},
		{"problem", aiSubject{Kind: "problem", ID: "p-42"}, "problem:p-42"},
		{"incident", aiSubject{Kind: "incident", ID: "inc-7"}, "incident:inc-7"},
		{"anomaly", aiSubject{Kind: "anomaly", ID: "an-9"}, "anomaly:an-9"},
		{"runbook", aiSubject{Kind: "runbook", ID: "p-42"}, "runbook:p-42"},
		{"exception", aiSubject{Kind: "exception", ID: "fp0badc0ffee"}, "exception:fp0badc0ffee"},
		{"span", aiSubject{Kind: "span", ID: "trace-1", SpanID: "span-2"}, "span:trace-1:span-2"},
		{"service-health", aiSubject{Kind: "service-health", ID: "checkout", From: ns(1000), To: ns(2000)}, "service-health:checkout:1000:2000"},
		{"service-health ':' kimlik", aiSubject{Kind: "service-health", ID: "ns:checkout", From: ns(1), To: ns(2)}, "service-health:ns%3Acheckout:1:2"},
		{"charts", aiSubject{Kind: "charts", ID: "checkout", From: ns(1000), To: ns(2000), Scope: "dur"}, "charts:checkout:1000:2000:dur"},
		{"log-spike", aiSubject{Kind: "log-spike", ID: "ns:checkout", From: ns(1000), To: ns(2000), Mode: "sel", Env: "prod", Q: `level:error AND "timeout"`, Level: "error"},
			"log-spike:ns%3Acheckout:1000:2000:sel:prod:level%3Aerror%20AND%20%22timeout%22:error"},
		{"log", aiSubject{Kind: "log", ID: "pay ments", TsNs: "1700000000123000000", Pod: "pod:1", TraceID: "0af7651916cd43dd8448eb211c80319c", Hash: "ca29aa39"},
			"log:1700000000123000000:pay%20ments:pod%3A1:0af7651916cd43dd8448eb211c80319c:ca29aa39"},
		{"endpoint", aiSubject{Kind: "endpoint", ID: "orders-api", Route: "/orders/{id}", Method: "POST", From: ns(1000), To: ns(2000)},
			"endpoint:orders-api:%2Forders%2F%7Bid%7D:1000:2000:POST"},
		{"endpoint fiilsiz", aiSubject{Kind: "endpoint", ID: "ns:cart", Route: "/cart", From: ns(1000), To: ns(2000)}, "endpoint:ns%3Acart:%2Fcart:1000:2000"},
		{"büyük ns penceresi", aiSubject{Kind: "service-health", ID: "orders-api", From: ns(1790000000000000000), To: ns(1790003600000000000)},
			"service-health:orders-api:1790000000000000000:1790003600000000000"},
		// v0.10.1435 — metric_panel: konum + pencere + brush + seçili seri (+ cmp).
		{"metric_panel dashboard", aiSubject{Kind: "metric_panel", ID: "d.dash01.p_2", From: ns(1000), To: ns(5000),
			BrushFrom: ns(2000), BrushTo: ns(3000), Series: []string{"pod-a", "ns:x y"}},
			"metric_panel:d.dash01.p_2:1000:5000:2000:3000:pod-a,ns%3Ax%20y"},
		{"metric_panel explore cmp", aiSubject{Kind: "metric_panel", ID: "x.B", From: ns(1000), To: ns(5000), Cmp: true},
			"metric_panel:x.B:1000:5000:0:0::cmp"},
		{"metric_panel metrics", aiSubject{Kind: "metric_panel", ID: "m", From: ns(1000), To: ns(5000), Series: []string{"a,b"}},
			"metric_panel:m:1000:5000:0:0:a%2Cb"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := formatAiSubject(c.subj); got != c.param {
				t.Fatalf("format %q, want %q", got, c.param)
			}
			got, ok := parseAiSubject(c.param)
			if !ok || !reflect.DeepEqual(got, c.subj) {
				t.Fatalf("parse %+v ok=%v, want %+v", got, ok, c.subj)
			}
		})
	}
	// Kapsanan türler istemcinin AI_KINDS kümesi.
	if len(aiSubjectKinds) != 13 { // v0.10.1435 — +metric_panel
		t.Fatalf("tür kümesi %d (aiSubject.ts AI_KINDS 13)", len(aiSubjectKinds))
	}
}

// v0.10.1435 — metric_panel kodeği: seri adı 80 rune'a kırpılır, ≤3 seri,
// kodek 400 rune'u aşarsa sondaki seriler düşer (konum + pencere her zaman
// sığar); kırpılmış biçim yeniden ayrıştırılır (aiSubject.test.ts aynası).
func TestAiSubjectMetricPanelFit(t *testing.T) {
	ns := func(n int64) time.Time { return time.Unix(0, n) }
	long := strings.Repeat("s", 100) + "ş"
	s := aiSubject{Kind: "metric_panel", ID: "d." + strings.Repeat("a", 64) + "." + strings.Repeat("b", 64),
		From: ns(1790000000000000000), To: ns(1790003600000000000), BrushFrom: ns(1790000600000000000), BrushTo: ns(1790001200000000000),
		Series: []string{long, long, long, "x"}, Cmp: true}
	p := formatAiSubject(s)
	if len(p) > chatExplainSubjectMax {
		t.Fatalf("kodek %d > %d", len(p), chatExplainSubjectMax)
	}
	got, ok := parseAiSubject(p)
	if !ok || got.ID != s.ID || !got.Cmp || len(got.Series) == 0 || len(got.Series) > 3 {
		t.Fatalf("kırpılmış kodek ayrıştırılamadı: %+v %v (%s)", got, ok, p)
	}
	for _, v := range got.Series {
		if len([]rune(v)) != metricPanelSeriesRunes {
			t.Fatalf("seri kırpılmadı: %d rune", len([]rune(v)))
		}
	}
	if formatAiSubject(got) != p {
		t.Fatal("biçim idempotent değil")
	}
}

func TestAiSubjectIDEncodingAndLenience(t *testing.T) {
	for _, id := range []string{"a b", "100%", "a/b", "çökme", "a+b", "a&b=c"} {
		p := formatAiSubject(aiSubject{Kind: "exception", ID: id})
		if got, ok := parseAiSubject(p); !ok || got.ID != id {
			t.Errorf("%q → %q → %+v %v", id, p, got, ok)
		}
	}
	// decodeURIComponent gibi: '+' boşluk DEĞİL.
	if got, _ := parseAiSubject("exception:a+b"); got.ID != "a+b" {
		t.Errorf("'+' çözümü %q", got.ID)
	}
	// charts: bilinmeyen kapsam reddedilmez → all; endpoint: bilinmeyen fiil düşer, küçük harf büyür.
	if got, ok := parseAiSubject("charts:checkout:1000:2000:weird"); !ok || got.Scope != "all" {
		t.Errorf("charts kapsam %+v %v", got, ok)
	}
	if got, ok := parseAiSubject("endpoint:svc:%2Fcart:1000:2000:BREW"); !ok || got.Method != "" {
		t.Errorf("endpoint bilinmeyen fiil %+v %v", got, ok)
	}
	if got, _ := parseAiSubject("endpoint:svc:%2Fcart:1000:2000:post"); got.Method != "POST" {
		t.Errorf("endpoint fiil %q", got.Method)
	}
	// log-spike: '*' = tüm servisler, eksik seviye → all.
	if got, ok := parseAiSubject("log-spike:*:1000:2000:auto:prod:x"); !ok || got.ID != "" || got.Level != "all" {
		t.Errorf("log-spike %+v %v", got, ok)
	}
}

func TestAiSubjectRejects(t *testing.T) {
	for _, raw := range []string{
		"", "metric:foo", "trace", "trace:", "trace:abc:def",
		"span:trace-1", "span:trace-1:", "span:trace-1:span-2:x",
		"service-health:checkout", "service-health:checkout:1000", "service-health:checkout:a:b",
		"service-health:checkout:2000:1000", "service-health:checkout:1000:1000", "service-health:checkout:-5:1000",
		"trace:%E0%A4%A", "trace:%E0%A4", // bozuk yüzde dizisi / geçersiz UTF-8
		"endpoint:svc:%2Fcart:1000", "endpoint:svc:%2Fcart:1000:2000:GET:x", "endpoint:svc:%2Fcart:2000:1000",
		"endpoint:svc:%2Fcart:1000:1000", "endpoint:svc:%2Fcart:0:1000", "endpoint:%zz:%2Fcart:1000:2000", "endpoint:svc::1000:2000",
		"log:0123:svc:::ca29aa39", "log:1700:svc::XYZ:ca29aa39", "log:1700::pod::ca29aa39", "log:1700:svc:pod::ca29", "log:1700:svc:pod:",
		"log-spike:svc:1000:2000:bogus:prod:x", "log-spike:svc:2000:1000:sel:prod:x",
		// v0.10.1435 — metric_panel: konum biçimi, brush pencere içinde, ≤3 seri, 8. parça yalnız cmp.
		"metric_panel:m:1000:5000:0:0", "metric_panel:q.A:1000:5000:0:0:", "metric_panel:x.E:1000:5000:0:0:",
		"metric_panel:d.a.b.c:1000:5000:0:0:", "metric_panel:d.a%2Fb.c:1000:5000:0:0:", "metric_panel:m:5000:1000:0:0:",
		"metric_panel:m:1000:5000:500:3000:", "metric_panel:m:1000:5000:2000:6000:", "metric_panel:m:1000:5000:3000:2000:",
		"metric_panel:m:1000:5000:0:3000:", "metric_panel:m:1000:5000:0:0:a,b,c,d", "metric_panel:m:1000:5000:0:0:a,,b",
		"metric_panel:m:1000:5000:0:0::x", "metric_panel:m:1000:5000:0:0::cmp:x", "metric_panel:m:1000:5000:0:0:%0A",
	} {
		if got, ok := parseAiSubject(raw); ok {
			t.Errorf("%q kabul edildi: %+v", raw, got)
		}
	}
}

// Çekmece kademesi yalnız kanıt kurulabilen türleri görür (v0.9.482 davranışı).
func TestParseDrawerSubjectFiltersToEvidenceKinds(t *testing.T) {
	if _, ok := parseDrawerSubject("problem:p-42"); ok {
		t.Error("problem çekmece kanıt türü değil")
	}
	if got, ok := parseDrawerSubject("span:trace-1:span-2"); !ok || got.SpanID != "span-2" {
		t.Errorf("span %+v %v", got, ok)
	}
}
