package tracechain

// tracechain_test.go — v0.10.1379 — operator context: exception explain lacked
// the downstream HTTP 500 that the trace explain (v0.10.1371) now shows.
//
// Saf HATA ZİNCİRİ çekirdeği internal/api'den buraya taşındı (anomaly api'yi
// import edemez). Pinler:
//   - PARİTE: testdata/parity_v0.10.1379.json, taşıma ÖNCESİ api kodunun
//     (traceErrorChainOf / Block / peerOf / spanFailure / drillCallLabel /
//     fmtDrillMs / spanDurNs / drillKindRank) aynı fikstürlerle ürettiği
//     çıktıdır — bayt bayt eşit kalmalı (api'nin explain_trace_chain_test.go
//     ve chat_trace_drill_test.go testleri de değişmeden yeşil).
//   - Kaynak kuralı, uzun zincir kısaltması, blok tavanı, çit kaçışı,
//     exception satırı, SpanException biçimleri, PrependUnique.
// Hostlar example.test (sentetik).

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// gcSpan — msec ofsetli parite fikstür span'i.
func gcSpan(id, parent, svc, name, kind string, startMs, durMs int64, errStatus bool) chstore.SpanRow {
	const base = int64(1_791_000_000_000_000_000)
	sp := chstore.SpanRow{SpanID: id, ParentSpanID: parent, ServiceName: svc, Name: name, Kind: kind,
		StartTime: base + startMs*1_000_000, EndTime: base + (startMs+durMs)*1_000_000}
	if errStatus {
		sp.StatusCode = "error"
	}
	return sp
}

func gcExEvent(typ, msg string) []any {
	return []any{map[string]any{"name": "exception", "attributes": map[string]any{"exception.type": typ, "exception.message": msg}}}
}

type gcCase struct {
	name  string
	spans []chstore.SpanRow
	p50   float64
	ex    [4]string // Service, Type, Msg, Source
}

// gcCases — v0.10.1379 parite fikstürleri (taşıma ÖNCESİ api çıktısı altın).
func gcCases() []gcCase {
	var out []gcCase
	// 1. HTTP 500 CLIENT → INTERNAL → SERVER kök; taban + log exception'ı.
	root := gcSpan("r1", "", "orders-api-prod", "POST /orders/submit", "server", 0, 120, true)
	util := gcSpan("u1", "r1", "orders-api-prod", "OrderMapper.map", "internal", 5, 100, true)
	cl := gcSpan("c1", "u1", "orders-api-prod", "POST", "client", 10, 50, true)
	cl.HTTPStatus = 500
	cl.Attributes = map[string]string{"url.full": "https://osb.example.test/esb/IdentityQuery?wsdl=1#frag", "server.address": "osb.example.test", "http.request.method": "POST"}
	out = append(out, gcCase{"http500", []chstore.SpanRow{root, util, cl}, 6.6,
		[4]string{"orders-api-prod", "java.lang.NullPointerException", `Cannot invoke "String.trim()" because "resp.getName()" is null`, "log"}})
	// 2. DB CLIENT hatası (kolon + db.name), status mesajı; exception yok.
	r2 := gcSpan("r2", "", "billing-svc", "GET /invoices", "server", 0, 900, true)
	db := gcSpan("d2", "r2", "billing-svc", "SELECT INVOICES", "client", 3, 850, true)
	db.DBSystem = "oracle"
	db.StatusMessage = "ORA-00942: table or view does not exist"
	db.Attributes = map[string]string{"db.name": "BILLDB"}
	out = append(out, gcCase{"db", []chstore.SpanRow{r2, db}, 0, [4]string{}})
	// 3. PRODUCER (peer.service kolonu) + span event exception'ı (log yedeğini yener).
	r3 := gcSpan("r3", "", "ledger-prod", "consume payments", "consumer", 0, 2500, true)
	pr := gcSpan("p3", "r3", "ledger-prod", "payments.retry publish", "producer", 100, 2300, true)
	pr.PeerService = "kafka-broker"
	pr.Attributes = map[string]string{"messaging.system": "kafka"}
	pr.Events = gcExEvent("org.apache.kafka.common.errors.TimeoutException", "Topic payments.retry not present in metadata after 2000 ms.")
	out = append(out, gcCase{"producer-event", []chstore.SpanRow{r3, pr}, 0, [4]string{"ledger-prod", "x.Y", "z", "log"}})
	// 4. Uzun zincir (12) + 3 zincir dışı yaprak.
	long := []chstore.SpanRow{gcSpan("h00", "", "svc", "root", "server", 0, 1000, true)}
	for i := 1; i < 12; i++ {
		long = append(long, gcSpan(fmt.Sprintf("h%02d", i), fmt.Sprintf("h%02d", i-1), "svc", fmt.Sprintf("op%d", i), "internal", int64(i), 900, true))
	}
	for i := 0; i < 3; i++ {
		long = append(long, gcSpan(fmt.Sprintf("x%d", i), "h00", "svc", "side", "client", int64(20+i), 3, true))
	}
	out = append(out, gcCase{"long", long, 0, [4]string{"svc", "a.b.C", "a.b.C: already prefixed", "log"}})
	// 5. Yetim tepe; kod http.status_code attribute'unda, url.full sorgulu; Tempo-gibi JSON event dizesi.
	or := gcSpan("o5", "gone000000000000", "gw-prod", "GET", "client", 0, 7, true)
	or.Attributes = map[string]string{"http.status_code": "503", "url.full": "http://inventory.example.test:8080/v2/stock?sku=1"}
	or.Events = `[{"name":"exception","attributes":{"exception.type":"java.io.IOException","exception.message":"Connection reset"}}]`
	out = append(out, gcCase{"orphan", []chstore.SpanRow{or}, 0, [4]string{}})
	// 6. Sağlıklı → "".
	out = append(out, gcCase{"healthy", []chstore.SpanRow{gcSpan("ok", "", "svc", "GET /", "server", 0, 5, false)}, 0, [4]string{"svc", "T", "m", "log"}})
	// 7. Çit + uzun ad + 1 sn üstü süre + tipsiz mesaj.
	r7 := gcSpan("r7", "", "svc", "GET /```x```", "server", 0, 61_000, true)
	c7 := gcSpan("c7", "r7", "svc", strings.Repeat("ç", 300), "client", 1, 60_500, true)
	c7.Attributes = map[string]string{"server.address": "slow.example.test", "http.response.status_code": "504"}
	out = append(out, gcCase{"fence-long", []chstore.SpanRow{r7, c7}, 12.34, [4]string{"", "", "yalnız mesaj\nikinci satır", ""}})
	// 8. Eşit derinlik: CLIENT kazanır, eşitlikte en erken; hatasız ara span.
	r8 := gcSpan("r8", "", "a", "GET /x", "server", 0, 100, false)
	i8 := gcSpan("i8", "r8", "a", "work", "internal", 1, 10, true)
	c8b := gcSpan("c8b", "r8", "a", "GET", "client", 60, 10, true)
	c8 := gcSpan("c8", "r8", "a", "GET", "client", 50, 10, true)
	c8.Attributes = map[string]string{"peer.service": "auth", "url.full": "https://auth.example.test/"}
	out = append(out, gcCase{"tie", []chstore.SpanRow{r8, i8, c8b, c8}, 0, [4]string{}})
	return out
}

// gcRec — parite kaydı (taşıma öncesi dökümle AYNI şekil).
type gcRec struct {
	Name, Block string
	Origin      string
	Hops        []string
	Other       int
	Peer        []string
	Fail        []string
	Labels      []string
}

func TestParityWithPreMoveAPI(t *testing.T) {
	raw, err := os.ReadFile("testdata/parity_v0.10.1379.json")
	if err != nil {
		t.Fatal(err)
	}
	var want []gcRec
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	cases := gcCases()
	if len(want) != len(cases) {
		t.Fatalf("altın %d kayıt, fikstür %d", len(want), len(cases))
	}
	for i, c := range cases {
		ch := Of(c.spans)
		if c.p50 > 0 {
			ch.BaseP50Ms, ch.BaseOK = c.p50, true
		}
		got := gcRec{Name: c.name, Block: ch.Block(Exception{Service: c.ex[0], Type: c.ex[1], Msg: c.ex[2], Source: c.ex[3]}),
			Origin: ch.Origin.Span.SpanID, Other: ch.OtherLeaf}
		for _, h := range ch.Hops {
			got.Hops = append(got.Hops, h.Span.SpanID)
		}
		for _, sp := range c.spans {
			p := PeerOf(sp)
			f := SpanFailure(sp)
			got.Peer = append(got.Peer, p.Kind+"|"+p.Target+"|"+p.Path)
			got.Fail = append(got.Fail, fmt.Sprintf("%s|%d|%s|%s", f.Status, f.HTTPCode, f.ExType, f.ExMsg))
			got.Labels = append(got.Labels, CallLabel(sp.Name, p)+"|"+FmtMs(float64(SpanDurNs(sp))/1e6)+"|"+fmt.Sprint(KindRank(sp.Kind)))
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("%s: parite bozuldu\n got: %+v\nwant: %+v", c.name, got, want[i])
		}
	}
}

func TestOfOriginRule(t *testing.T) {
	cs := gcCases()
	byName := map[string]gcCase{}
	for _, c := range cs {
		byName[c.name] = c
	}
	// HTTP 500 CLIENT → INTERNAL → SERVER: kaynak CLIENT, zincir derinden köke.
	c := Of(byName["http500"].spans)
	if !c.OK || c.Origin.Span.SpanID != "c1" || len(c.Hops) != 3 || c.Hops[2].Span.SpanID != "r1" || c.Origin.Depth != 2 {
		t.Fatalf("http500 zinciri: %+v", c)
	}
	if c.Origin.Fail.HTTPCode != 500 || c.Origin.Peer.Target != "osb.example.test" || c.Origin.Peer.Path != "/esb/IdentityQuery" {
		t.Fatalf("kaynak hedef/kod: %+v %+v", c.Origin.Fail, c.Origin.Peer)
	}
	// Eşit derinlik: CLIENT, sonra en erken başlayan.
	if got := Of(byName["tie"].spans).Origin.Span.SpanID; got != "c8" {
		t.Errorf("eşitlik kuralı: %s", got)
	}
	// Hatasız / boş → zincir yok, blok "".
	if h := Of(byName["healthy"].spans); h.OK || h.Block(Exception{Type: "T"}) != "" {
		t.Errorf("sağlıklı trace zincir üretmemeli")
	}
	if Of(nil).OK {
		t.Error("boş trace")
	}
	// Yaprak yok (döngü) → en derin hatalı span; döngü MaxHops'ta durur.
	a := gcSpan("a", "b", "svc", "x", "internal", 0, 1, true)
	b := gcSpan("b", "a", "svc", "y", "internal", 0, 1, true)
	if cc := Of([]chstore.SpanRow{a, b}); cc.OK && len(cc.Hops) > MaxHops {
		t.Errorf("döngü sınırsız yürüdü: %d", len(cc.Hops))
	}
}

func TestBlockBoundsAndShape(t *testing.T) {
	cs := gcCases()
	long := Of(cs[3].spans).Block(Exception{})
	if !strings.HasPrefix(long, Header) || !strings.Contains(long, "… 4 ara halka atlandı") || strings.Count(long, "\n") != 10 {
		t.Errorf("uzun zincir:\n%s", long)
	}
	fence := Of(cs[6].spans).Block(Exception{Msg: "m"})
	if strings.Contains(fence, "```") || len([]rune(fence)) > BlockMaxRunes+1 {
		t.Errorf("çit kaçışı / tavan:\n%s", fence)
	}
	for _, ln := range strings.Split(fence, "\n")[1:] {
		if n := len([]rune(ln)); n > HopMaxRunes+40 {
			t.Errorf("halka satırı tavanı aşıldı (%d): %s", n, ln)
		}
	}
	// Taşma: çok uzun halkalar → blok BlockMaxRunes'ta kesilir.
	var spans []chstore.SpanRow
	for i := 0; i < 10; i++ {
		parent := ""
		if i > 0 {
			parent = fmt.Sprintf("s%02d", i-1)
		}
		sp := gcSpan(fmt.Sprintf("s%02d", i), parent, strings.Repeat("svc", 40), strings.Repeat("ad", 200), "internal", int64(i), 5, true)
		sp.StatusMessage = strings.Repeat("m", 400)
		spans = append(spans, sp)
	}
	if n := len([]rune(Of(spans).Block(Exception{Type: "a.B", Msg: strings.Repeat("x", 500)}))); n > BlockMaxRunes+1 {
		t.Errorf("blok tavanı: %d", n)
	}
}

func TestExceptionLine(t *testing.T) {
	for _, tc := range []struct {
		ex   Exception
		want string
	}{
		{Exception{}, ""},
		{Exception{Service: "orders-api-prod", Type: "a.B", Msg: "boom", Source: "exception grubu"},
			"Zincirin ardından kaydedilen exception: orders-api-prod · a.B: boom (exception grubu)"},
		{Exception{Type: "a.B", Msg: "a.B: boom"}, "Zincirin ardından kaydedilen exception: a.B: boom"},
		{Exception{Msg: "çok\nsatır"}, "Zincirin ardından kaydedilen exception: çok satır"},
		{Exception{Service: "s", Type: "a.B"}, "Zincirin ardından kaydedilen exception: s · a.B"},
	} {
		if got := tc.ex.Line(); got != tc.want {
			t.Errorf("%+v\n got: %q\nwant: %q", tc.ex, got, tc.want)
		}
	}
}

func TestSpanExceptionShapes(t *testing.T) {
	type tempoEv struct {
		Name       string            `json:"name"`
		Attributes map[string]string `json:"attributes"`
	}
	for _, tc := range []struct {
		name         string
		ev           any
		wantT, wantM string
	}{
		{"nil", nil, "", ""},
		{"CH []any", gcExEvent("a.B", " m "), "a.B", "m"},
		{"JSON dizesi", `[{"name":"log"},{"name":"exception","attributes":{"exception.message":"x"}}]`, "", "x"},
		{"struct dilimi", []tempoEv{{Name: "exception", Attributes: map[string]string{"exception.type": "c.D"}}}, "c.D", ""},
		{"bozuk JSON", "{nope", "", ""},
		{"exception yok", []any{map[string]any{"name": "log"}}, "", ""},
	} {
		if ty, m := SpanException(tc.ev); ty != tc.wantT || m != tc.wantM {
			t.Errorf("%s: (%q,%q)", tc.name, ty, m)
		}
	}
}

func TestPrependUnique(t *testing.T) {
	if got := strings.Join(PrependUnique("b", []string{"a", "b", "c"}), ","); got != "b,a,c" {
		t.Errorf("got %s", got)
	}
	if got := strings.Join(PrependUnique("", []string{"a"}), ","); got != "a" {
		t.Errorf("boş id: %s", got)
	}
}
