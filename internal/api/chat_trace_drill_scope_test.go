package api

// chat_trace_drill_scope_test.go — v0.10.1399 (operator-reported):
// "Mesela bir operasyonu soruyorum ama bana kafka timeout vs diyor ama
// exceptions açıp explain ettirdiğimde daha mantıklı güzel cevap veriyor."
// Örnek trace kırılımı, kök span'i hatasız bir trace'te log yayınının
// PRODUCER span'indeki Kafka TimeoutException'ını "hatayı aynen aktar"
// kuralıyla operasyonun hatası gibi veriyordu. drillFailureScope hatanın
// kapsamını etiketler; yan yayın kapsamında ek KURAL satırı gelir.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func dsSpan(id, parent, kind, status string, attrs map[string]string) chstore.SpanRow {
	return chstore.SpanRow{TraceID: "t1", SpanID: id, ParentSpanID: parent, Kind: kind, StatusCode: status, ServiceName: "orders-api", Name: id, Attributes: attrs}
}

func TestDrillFailureScope(t *testing.T) {
	kafka := map[string]string{"messaging.system": "kafka", "messaging.destination.name": "app-logs"}
	for _, c := range []struct {
		name   string
		spans  []chstore.SpanRow
		rootID string
		want   string
	}{
		{"hatasız", []chstore.SpanRow{dsSpan("r", "", "SERVER", "ok", nil), dsSpan("p", "r", "PRODUCER", "", kafka)}, "r", ""},
		{"yalnız PRODUCER hatalı, kök hatasız", []chstore.SpanRow{dsSpan("r", "", "SERVER", "ok", nil), dsSpan("p", "r", "PRODUCER", "error", kafka)}, "r", drillScopeSidePublish},
		{"messaging CLIENT hatalı, kök hatasız", []chstore.SpanRow{dsSpan("r", "", "SERVER", "", nil), dsSpan("c", "r", "CLIENT", "error", kafka)}, "r", drillScopeSidePublish},
		{"kök hatalı (yayın da hatalı)", []chstore.SpanRow{dsSpan("r", "", "SERVER", "error", nil), dsSpan("p", "r", "PRODUCER", "error", kafka)}, "r", drillScopeOpPath},
		{"alt SERVER hatalı", []chstore.SpanRow{dsSpan("r", "", "SERVER", "", nil), dsSpan("s", "r", "SERVER", "error", nil)}, "r", drillScopeOpPath},
		{"karışık: HTTP CLIENT + PRODUCER, kök hatasız", []chstore.SpanRow{dsSpan("r", "", "SERVER", "", nil), dsSpan("h", "r", "CLIENT", "error", map[string]string{"server.address": "db.example.test"}), dsSpan("p", "r", "PRODUCER", "error", kafka)}, "r", ""},
		{"kök kimliği yok: ebeveynsiz span kök sayılır", []chstore.SpanRow{dsSpan("r", "", "INTERNAL", "error", nil), dsSpan("p", "r", "PRODUCER", "error", kafka)}, "", drillScopeOpPath},
		{"kök kimliği yok: yetim ebeveyn kök sayılır", []chstore.SpanRow{dsSpan("r", "missing", "INTERNAL", "", nil), dsSpan("p", "r", "producer", "error", nil)}, "", drillScopeSidePublish},
	} {
		if got := drillFailureScope(c.spans, c.rootID); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDrillScopeRenderAndRule(t *testing.T) {
	d := tdProdDrill(tdTrace1, "en yavaş hatalı")
	plain := renderTraceDrillTR(d)
	if strings.Contains(plain, "hata kapsamı") {
		t.Fatal("kapsamsız kırılımda etiket yazıldı (golden bayt bayt eski olmalı)")
	}
	d.Scope = drillScopeSidePublish
	got := renderTraceDrillTR(d)
	if !strings.Contains(got, "Read timed out) [hata kapsamı: "+drillScopeSidePublish+"]; bu operasyonun") {
		t.Fatalf("etiket hata demetinin hemen ardında olmalı: %s", got)
	}
	if strings.Contains(composeDrillEvidenceTR([]traceDrill{tdProdDrill(tdTrace1, "en yavaş")}, ""), drillSidePublishRuleTR) {
		t.Fatal("yan yayın yokken ek KURAL yazıldı")
	}
	op := tdProdDrill(tdTrace2, "en yavaş")
	op.Scope = drillScopeOpPath
	if strings.Contains(composeDrillEvidenceTR([]traceDrill{op}, ""), drillSidePublishRuleTR) {
		t.Fatal("operasyon yolu kapsamında ek KURAL yazıldı")
	}
	txt := composeDrillEvidenceTR([]traceDrill{op, d}, "")
	if !strings.HasSuffix(txt, drillSidePublishRuleTR) || !strings.Contains(drillSidePublishRuleTR, "sebebi DEĞİLDİR") {
		t.Fatalf("yan yayın KURAL satırı sonda olmalı: %s", txt)
	}
}
