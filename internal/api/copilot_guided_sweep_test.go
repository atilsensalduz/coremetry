package api

// v0.10.1276 — CoSRE adım 7: servis sağlık taraması (pod / operasyon / fonksiyon + sorunluların logları)

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// operatorSweepAsk — operatörün AYNEN cümlesi (2026-10-17).
const operatorSweepAsk = "Servisin tüm podlarını operationlarını fonksiyonlarını da tarasın genel sağlığı için sorunlu olanların loglarına baksın ve kullanıcıyı bu anlamda da assist etsin."

func TestHasSweepSignal(t *testing.T) {
	for _, c := range []struct {
		msg  string
		want bool
	}{
		{operatorSweepAsk, true},
		{"Servisin tüm podlarını operationlarını fonksiyonlarını da tarasın genel sağlığı için sorunlu olanların loglarına baksın", true},
		{"checkout servisini tara", true},
		{"checkout'u kapsamlı incele", true},
		{"scan checkout-service", true},
		{"checkout pod ve operasyonlarına bak", true},   // iki aile
		{"checkout fonksiyon ve endpoint durumu", true}, // iki aile
		{"checkout genel sağlığı nasıl", true},          // genel + sağlık
		{"checkout genel durumu", true},                 // genel + durum
		{"checkout pod'ları nasıl", false},              // tek aile → pod_health
		{"checkout tarafında hata var mı", false},       // taraf* tarama DEĞİL
		{"checkout sağlığı nasıl", false},               // düz sağlık (standart tarama, Deep yok)
		{"checkout neden yavaş", false},
		{"genel kültür sorusu", false},                     // genel tek başına yetmez
		{"checkout servisinin operasyonları nasıl", false}, // tek aile
	} {
		if got := hasSweepSignal(guidedTokens(normalizeGuidedMsg(c.msg))); got != c.want {
			t.Errorf("hasSweepSignal(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

// TestRouterSweepBeforePod — operatörün cümlesi pod/log sinyali de taşır;
// tarama dalı ÖNCE gelmeli (servis sağlığı + Deep), pod_health/log_errors DEĞİL.
func TestRouterSweepBeforePod(t *testing.T) {
	r := routeGuidedIntent(operatorSweepAsk, guidedTestServices, guidedTestEnvs, nil, "checkout-service")
	if r.Intent != guidedServiceHealth || r.Service != "checkout-service" || !r.Deep {
		t.Fatalf("operatör cümlesi → %+v; servis sağlığı (Deep) bekleniyordu", r)
	}
	r = routeGuidedIntent("checkout-service tüm podlarını ve operasyonlarını tara", guidedTestServices, guidedTestEnvs, nil, "")
	if r.Intent != guidedServiceHealth || r.Service != "checkout-service" || !r.Deep {
		t.Fatalf("açık servisli tarama → %+v", r)
	}
	// Tek aile pod sorusu eskisi gibi pod_health.
	if r := routeGuidedIntent("checkout-service pod'ları nasıl", guidedTestServices, guidedTestEnvs, nil, ""); r.Intent != guidedPodHealth || r.Deep {
		t.Fatalf("pod sorusu → %+v; pod_health bekleniyordu", r)
	}
	// Düz sağlık sorusu: servis sağlığı, Deep YOK (standart tarama).
	if r := routeGuidedIntent("checkout-service sağlığı nasıl", guidedTestServices, guidedTestEnvs, nil, ""); r.Intent != guidedServiceHealth || r.Deep {
		t.Fatalf("düz sağlık → %+v", r)
	}
	if got := serviceIntentFor(normalizeGuidedMsg(operatorSweepAsk), guidedTokens(normalizeGuidedMsg(operatorSweepAsk))); got != guidedServiceHealth {
		t.Fatalf("serviceIntentFor(operatör) = %q; servis sağlığı bekleniyordu (router sırası aynası)", got)
	}
}

func TestMergeSweepVerdict(t *testing.T) {
	sw := mcptools.SweepResult{OK: true, Verdict: mcptools.SweepVerdict{Next: "Önce 1 numaralı bulguya bakın (pod p-1)."},
		Findings: []mcptools.SweepFinding{
			{Rank: 1, Kind: "pod", Name: "p-1", Level: "crit", Reasons: []string{"son sonlanma OOMKilled"}},
			{Rank: 2, Kind: "operation", Name: "GET /x", Level: "warn", Reasons: []string{"p99 300 ms, baseline'dan +%50"}},
		}}
	l, r, n := mergeSweepVerdict("Sağlıklı", []string{"hata oranı %0.10, p99 50 ms — baseline içinde"}, "Şu an aksiyon gerekmiyor.", sw, true)
	if l != "Sorunlu" || !strings.Contains(n, "1 numaralı") || len(r) != 2 || !strings.HasPrefix(r[0], "tarama: pod p-1") {
		t.Fatalf("kritik bulgu sağlıklıyı ezmeli: %s %v %q", l, r, n)
	}
	// Servis zaten Sorunlu → etiket aynı, adım servisinki, gerekçe EKLENİR.
	l, r, n = mergeSweepVerdict("Sorunlu", []string{"hata oranı %9"}, "Hatalı trace'lere bakın.", sw, true)
	if l != "Sorunlu" || n != "Hatalı trace'lere bakın." || len(r) != 3 || r[0] != "hata oranı %9" {
		t.Fatalf("kötü servis hükmü korunmalı: %s %v %q", l, r, n)
	}
	// Tarama okunamadı → aynen.
	l, r, n = mergeSweepVerdict("Dikkat", []string{"x"}, "y", sw, false)
	if l != "Dikkat" || len(r) != 1 || n != "y" {
		t.Fatalf("ok=false hükmü değiştirmemeli: %s %v %q", l, r, n)
	}
	// Bulgusuz tarama → aynen.
	if l, _, _ := mergeSweepVerdict("Sağlıklı", nil, "z", mcptools.SweepResult{OK: true}, true); l != "Sağlıklı" {
		t.Fatalf("bulgusuz tarama etiketi değiştirmemeli: %s", l)
	}
}

func TestRenderSweepEvidenceTR(t *testing.T) {
	sw := mcptools.SweepResult{OK: true,
		Scanned: mcptools.SweepScanned{Pods: 4, Operations: 12, Functions: 3, FunctionsSource: "rollup", JVMPods: 2},
		Findings: []mcptools.SweepFinding{{Rank: 1, Kind: "pod", Name: "p-1", Cluster: "c1", Namespace: "ns", Level: "crit",
			Reasons: []string{"son sonlanma OOMKilled"}, Logs: &mcptools.SweepLogs{State: "ok", Summary: "3 ERROR+ log; ilki: boom"}}},
		Sections: []mcptools.SweepSection{{Name: "pods", State: "ok", Summary: "4 pod"}, {Name: "functions", State: "empty", Summary: "fonksiyon verisi yok"},
			{Name: "jvm", State: "error", Summary: "JVM metriği okunamadı"}}}
	out := renderSweepEvidenceTR(sw)
	for _, want := range []string{
		"TARAMA (deterministik eşik, model değil): 4 pod, 12 operasyon, 3 fonksiyon (rollup)",
		"sorunlular: 1) pod p-1 [crit].",
		"1) pod p-1 (c1/ns) — crit: son sonlanma OOMKilled",
		"loglar: 3 ERROR+ log; ilki: boom",
		"- fonksiyonlar: bakıldı, veri yok — fonksiyon verisi yok",
		"- JVM: OKUNAMADI (error, okunamadı ≠ yok)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kanıtta yok: %q\n%s", want, out)
		}
	}
}

// TestWithBlockSeqEvBlock — guided bileşik blok TEK sıralayıcıdan `block` olur.
func TestWithBlockSeqEvBlock(t *testing.T) {
	var got []string
	var seq blocks.Sequencer
	emit := withBlockSeq(func(kind string, p any) {
		got = append(got, kind)
		if kind == "block" {
			m := p.(map[string]any)
			if m["type"] != blocks.TypeTable {
				t.Errorf("blok türü %v", m["type"])
			}
		}
	}, &seq)
	emit("ev_block", mcptools.EvidenceBlock{Type: blocks.TypeTable, Payload: map[string]any{"title": "x"}})
	if len(got) != 1 || got[0] != "block" {
		t.Fatalf("ev_block → %v", got)
	}
}

// TestEmitGuidedCompositeResult — step-result çip listesi + blok aynı adım
// kimliğine bağlı (ev<i>, ev<i>.<k>).
func TestEmitGuidedCompositeResult(t *testing.T) {
	content := `{"tool":"service_health_sweep","ok":true,"evidences":[` +
		`{"hop":"red","as":"query_traces","request":{"service":"checkout"},"source":"clickhouse","queryKind":"sql","query":"q1","window":{"fromMs":1700000000000,"toMs":1700003600000}},` +
		`{"hop":"pods","as":"pod","request":{"cluster":"c1","pod":"p-1","service":"checkout"},"source":"thanos","queryKind":"promql","query":"q2","window":{"fromMs":1700000000000,"toMs":1700003600000}}],` +
		`"data":{"blocks":[{"hop":"red","type":"table","payload":{"title":"özet","columns":[],"rows":[]}},{"hop":"pods","type":"table","payload":{"title":"pod","columns":[],"rows":[]}}]}}`
	var kinds []string
	var res map[string]any
	var refs []string
	emit := func(kind string, p any) {
		kinds = append(kinds, kind)
		switch kind {
		case "step-result":
			res = p.(map[string]any)
		case "ev_block":
			b := p.(mcptools.EvidenceBlock)
			refs = append(refs, b.Payload.(map[string]any)["evidenceRef"].(string))
		}
	}
	emitGuidedCompositeResult(emit, 7, mcptools.ServiceHealthSweepName, content)
	if len(kinds) != 3 || kinds[0] != "step-result" {
		t.Fatalf("olaylar %v", kinds)
	}
	if _, ok := res["evidenceList"]; !ok {
		t.Fatalf("step-result evidenceList taşımalı: %v", res)
	}
	if strings.Join(refs, ",") != "ev7,ev7.1" {
		t.Fatalf("blok ref'leri %v", refs)
	}
	// Kimliksiz çağrı hiçbir şey yayınlamaz.
	kinds = nil
	emitGuidedCompositeResult(emit, 0, mcptools.ServiceHealthSweepName, content)
	if len(kinds) != 0 {
		t.Fatalf("i=0 → %v", kinds)
	}
}

// TestApplyScreenContextSweep — adım 5'in ekran kapsamı yeni araca da iner:
// servis (zorunlu), env ve window (enum) aracın GERÇEK şemasından.
func TestApplyScreenContextSweep(t *testing.T) {
	var schema map[string]any
	for _, tool := range mcptools.ToolList(mcptools.Deps{}) {
		if tool.Name == mcptools.ServiceHealthSweepName {
			schema = tool.InputSchema
		}
	}
	if schema == nil {
		t.Fatal("service_health_sweep katalogda yok")
	}
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	out, ap := applyScreenContext(mcptools.ServiceHealthSweepName, json.RawMessage(`{}`), schema,
		toolScreenScope{Service: "checkout", Env: "prod", RangeS: 3600}, now)
	m := argMap(t, out)
	if m["service"] != "checkout" || m["env"] != "prod" || m["window"] != "1h" {
		t.Fatalf("ekran kapsamı inmedi: %s %+v", out, ap)
	}
	if ap.Service != "checkout" || ap.Env != "prod" || ap.RangeS != 3600 {
		t.Fatalf("künye eksik: %+v", ap)
	}
	if _, blind := mcptools.EnvBlindReason(mcptools.ServiceHealthSweepName); blind {
		t.Fatal("tarama env-kör değil (RED + problemler env'i uygular)")
	}
}
