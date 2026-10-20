package api

// chat_operation_exception_explain_test.go — v0.10.1399 (operator-reported):
// "Mesela bir operasyonu soruyorum ama bana kafka timeout vs diyor ama
// exceptions açıp explain ettirdiğimde daha mantıklı güzel cevap veriyor.
// Chatten de sorduğumda böyle bakıp buna cevap verebilir."
//
// "<OP> operasyonu neden hata veriyor?" → ön-getirmenin Exceptions grubu
// sohbet içinde Exceptions açıklamasının BİREBİR çekirdeğiyle anlatılır
// (aynı girdi, aynı prompt, aynı önbellek anahtarı); örnek trace kırılımının
// yan hatası (log yayınının PRODUCER timeout'u) cevabı domine etmez.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestOpErrorWhyAsk(t *testing.T) {
	for _, c := range []struct {
		q    string
		want bool
	}{
		{"ORDERS_GET_X operasyonu neden hata veriyor?", true},
		{"ORDERS_GET_X niye hata alıyor", true},
		{"ORDERS_GET_X hata veriyo", true},
		{"ORDERS_GET_X operasyonu hata veriyor", true},
		{"ORDERS_GET_X neden hatalı dönüyor", true},
		{"why is ORDERS_GET_X failing", true},
		{"why does ORDERS_GET_X return errors", true},
		{"ORDERS_GET_X exception neden", true},
		// Hata-neden değil → bugünkü yol.
		{"ORDERS_GET_X operasyonu neden yavaş", false},
		{"ORDERS_GET_X p99 neden yüksek hata", false},
		{"ORDERS_GET_X gecikme hatası neden", false},
		{"ORDERS_GET_X hata oranı neden arttı", false},
		{"ORDERS_GET_X kaç hata var", false},
		{"ORDERS_GET_X hata logları neden", false},
		{"ORDERS_GET_X operasyonunun topolojisi neden hata", false},
		{"ORDERS_GET_X operasyonunda sorun var mı", false},
		{"ORDERS_GET_X hataları", false},
		{"ORDERS_GET_X", false},
		{"", false},
	} {
		if got := opErrorWhyAsk(c.q); got != c.want {
			t.Errorf("opErrorWhyAsk(%q) = %v, want %v", c.q, got, c.want)
		}
	}
}

func TestPickExplainGroup(t *testing.T) {
	g := func(fp, pri, state string, lastHour, occ uint64, lastSeen int64) exGroupLine {
		return exGroupLine{Fingerprint: fp, Code: "C-" + fp, Priority: pri, State: state, LastHour: lastHour, Occurrences: occ, LastSeen: lastSeen}
	}
	for _, c := range []struct {
		name   string
		in     []exGroupLine
		top    string
		others []string
		ok     bool
	}{
		{"boş", nil, "", nil, false},
		{"yalnız ignored/resolved", []exGroupLine{g("a1", "P1", "ignored", 9, 9, 9), g("a2", "P1", "resolved", 9, 9, 9)}, "", nil, false},
		{"P1 en yeniyi yener", []exGroupLine{g("new", "P3", "new", 50, 50, 100), g("old", "P1", "acknowledged", 1, 10, 10)}, "old", []string{"new"}, true},
		{"aynı öncelik: son saat", []exGroupLine{g("b1", "P2", "new", 5, 900, 100), g("b2", "P2", "new", 40, 10, 50)}, "b2", []string{"b1"}, true},
		{"aynı son saat: toplam", []exGroupLine{g("c1", "P2", "new", 0, 10, 100), g("c2", "P2", "new", 0, 99, 50)}, "c2", []string{"c1"}, true},
		{"aynı toplam: son görülme", []exGroupLine{g("d1", "", "new", 0, 10, 50), g("d2", "", "new", 0, 10, 100)}, "d2", []string{"d1"}, true},
		{"tam eşitlik: parmak izi", []exGroupLine{g("e2", "P1", "new", 1, 1, 1), g("e1", "P1", "new", 1, 1, 1)}, "e1", []string{"e2"}, true},
		{"resolved düşer, regressed kalır", []exGroupLine{g("f1", "P1", "resolved", 99, 99, 99), g("f2", "P2", "regressed", 1, 1, 1)}, "f2", nil, true},
		{"parmak izsiz düşer", []exGroupLine{g("", "P1", "new", 99, 99, 99), g("h1", "P3", "new", 1, 1, 1)}, "h1", nil, true},
	} {
		top, others, ok := pickExplainGroup(c.in)
		if ok != c.ok || top.Fingerprint != c.top {
			t.Errorf("%s: top=%q ok=%v, want %q %v", c.name, top.Fingerprint, ok, c.top, c.ok)
			continue
		}
		var got []string
		for _, o := range others {
			got = append(got, o.Fingerprint)
		}
		if strings.Join(got, ",") != strings.Join(c.others, ",") {
			t.Errorf("%s: others=%v, want %v", c.name, got, c.others)
		}
	}
	// Girdi dilimi yerinde sıralanmaz (ön-getirme paketi kartta aynı kalır).
	in := []exGroupLine{g("x1", "P3", "new", 0, 1, 1), g("x2", "P1", "new", 0, 1, 1)}
	_, _, _ = pickExplainGroup(in)
	if in[0].Fingerprint != "x1" {
		t.Fatal("pickExplainGroup girdiyi yerinde değiştirdi")
	}
}

func TestExceptionFollowupFactsTopAndOthers(t *testing.T) {
	p := &opPrefetch{Operation: "ORDERS_GET_X", Exceptions: &exceptionGroupsEvidence{Groups: []exGroupLine{
		{Fingerprint: "ora:00000000000000a1", Code: "ORD-100", Oracle: true, Priority: "P3", State: "new", LastSeen: 300}, // en yeni
		{Fingerprint: "ora:00000000000000b2", Code: "ORD-200", Oracle: true, Priority: "P1", State: "new", LastSeen: 100}, // en önemli
		{Fingerprint: "0000000000000c3", Type: "java.lang.IllegalStateException", Priority: "P2", State: "new", LastSeen: 200},
	}}}
	f := p.exceptionFollowupFacts()
	if len(f) != 3 || f[0].Code != "ORD-200" || f[1].Code != "java.lang.IllegalStateException" || f[2].Code != "ORD-100" {
		t.Fatalf("olgular en önemli önce olmalı: %+v", f)
	}
	chips := followupChips(p.followupFacts(), nil, followupMax)
	if len(chips) == 0 || chips[0].Kind != fuKindExceptionGroup || chips[0].Text != "ORD-200 grubunu açıkla" {
		t.Fatalf("çip en önemli grubu söylemeli (sohbet içi açıklamayla aynı): %+v", chips)
	}
	n := 0
	for _, c := range chips {
		if c.Kind == fuKindExceptionGroup {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("serbest döngü çiplerinde tür başına bir grup çipi: %+v", chips)
	}
}

// opExplainProvider — OpenAI-uyumlu sahte uç; isteğin sistem/kullanıcı
// mesajlarını yakalar ve deltaları SSE ile akıtır.
type opExplainProvider struct {
	mu     sync.Mutex
	system string
	user   string
	calls  int
	deltas []string
	fail   bool
}

func newOpExplainServer(t *testing.T, p *opExplainProvider) *Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(body, &m)
		p.mu.Lock()
		p.calls++
		for _, msg := range m.Messages {
			txt := fmt.Sprint(msg.Content)
			switch msg.Role {
			case "system":
				p.system = txt
			case "user":
				p.user = txt
			}
		}
		fail := p.fail
		p.mu.Unlock()
		if fail {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"model kotası doldu"}}`))
			return
		}
		if !m.Stream {
			out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
				"message": map[string]any{"content": strings.Join(p.deltas, "")}, "finish_reason": "stop"}}})
			_, _ = w.Write(out)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, d := range p.deltas {
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": d}}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	cop := copilot.New(copilot.ProviderOpenAI, "test-key", "gemma4")
	cop.Configure(copilot.ProviderOpenAI, "test-key", "gemma4", srv.URL, false, true)
	if !cop.Active() {
		t.Fatal("sahte copilot aktif değil")
	}
	return &Server{copilot: cop}
}

// opExplainFixture — operasyonun üç grubu (en yeni ≠ en önemli) + sahte girdi.
func opExplainFixture(t *testing.T, seamErr error) (*opPrefetch, *[]string) {
	t.Helper()
	var asked []string
	exceptionInputSeam = func(_ context.Context, fp string, _ *time.Location) (*chstore.ExceptionGroup, anomaly.ExceptionExplainInput, error) {
		asked = append(asked, fp)
		if seamErr != nil {
			return nil, anomaly.ExceptionExplainInput{}, seamErr
		}
		return &chstore.ExceptionGroup{Fingerprint: fp, Service: "orders-api", Type: "ORD-200", Message: "ORDERS_GET_X"},
			anomaly.ExceptionExplainInput{User: "EXCEPTION KANITI " + fp + ": çağıran span orders-api SELECT … FROM orders (db.example.test)"}, nil
	}
	t.Cleanup(func() { exceptionInputSeam = nil })
	p := &opPrefetch{Operation: "ORDERS_GET_X", errorAsk: true, TracesHref: "/traces?op=ORDERS_GET_X",
		Exceptions: &exceptionGroupsEvidence{Groups: []exGroupLine{
			{Fingerprint: "ora:00000000000000a1", Code: "ORD-100", Oracle: true, Priority: "P3", State: "new", LastSeen: 300,
				Href: "/problems?exc=ora%3A00000000000000a1", ExplainHref: "/problems?exc=ora%3A00000000000000a1&ai=exception"},
			{Fingerprint: "ora:00000000000000b2", Code: "ORD-200", Oracle: true, Priority: "P1", State: "new", LastSeen: 100,
				Href: "/problems?exc=ora%3A00000000000000b2", ExplainHref: "/problems?exc=ora%3A00000000000000b2&ai=exception"},
		}},
		Drill: "EN YAVAŞ TRACE KRİTİK YOLU … PRODUCER çağrısında: send kafka/app-logs (org.apache.kafka.common.errors.TimeoutException)",
	}
	return p, &asked
}

type opExplainEvent struct {
	kind string
	data any
}

func TestOpExceptionExplainStreams(t *testing.T) {
	prov := &opExplainProvider{deltas: []string{"**Hata ne demek** ORD-200 ", "SAXParseException."}}
	s := newOpExplainServer(t, prov)
	mc := newMemCache()
	s.cache = mc
	p, asked := opExplainFixture(t, nil)
	var evs []opExplainEvent
	emit := withStepIDs(func(k string, v any) { evs = append(evs, opExplainEvent{k, v}) })
	ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "x-1"})
	handled, ok := s.opExceptionExplainInChat(ctx, emit, p, time.UTC)
	if !handled || !ok {
		t.Fatalf("handled=%v ok=%v", handled, ok)
	}
	if len(*asked) != 1 || (*asked)[0] != "ora:00000000000000b2" {
		t.Fatalf("açıklanan grup en önemli (P1) olmalı, en yeni değil: %v", *asked)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.kind)
	}
	if got := strings.Join(kinds, ","); got != "step,step-result,delta,delta,answer" {
		t.Fatalf("olay sırası %s", got)
	}
	if st, _ := json.Marshal(evs[0].data); !strings.Contains(string(st), opExceptionExplainTool) {
		t.Fatalf("adım çipi %s", st)
	}
	// Aynı prompt çifti: Oracle grubu → Oracle sistem prompt'u, kullanıcı = in.User (sarmalayıcısız).
	if prov.system != copilot.SystemPromptOracleException() {
		t.Fatal("sistem prompt'u Exceptions açıklamasınınki değil")
	}
	if !strings.HasPrefix(prov.user, "EXCEPTION KANITI ora:00000000000000b2") {
		t.Fatalf("kullanıcı mesajı girdinin kendisi olmalı: %q", prov.user)
	}
	ans := evs[len(evs)-1].data.(map[string]any)
	text := ans["text"].(string)
	if !strings.HasPrefix(text, "**Hata ne demek** ORD-200 SAXParseException.") ||
		!strings.HasSuffix(text, "Kaynak: Exceptions grubu ORD-200 · ORDERS_GET_X · Exceptions açıklamasıyla aynı kanıt") {
		t.Fatalf("cevap metni %q", text)
	}
	if strings.Contains(text, "TimeoutException") || strings.Contains(text, "EN YAVAŞ TRACE") || strings.Contains(prov.user, "EN YAVAŞ TRACE") {
		t.Fatal("örnek trace kırılımı cevaba/girdiye sızdı")
	}
	if ans["exchangeId"] != "x-1" {
		t.Fatalf("exchangeId %v", ans["exchangeId"])
	}
	links, _ := ans["links"].([]guidedAnswerLink)
	var hrefs []string
	for _, l := range links {
		hrefs = append(hrefs, l.Href)
	}
	// v0.10.1400 — grup linki çıplak grup sayfası (açıklama zaten bu turda;
	// &ai=exception sohbete ikinci bir açıklama turu açardı).
	if len(hrefs) < 2 || hrefs[0] != "/problems?exc=ora%3A00000000000000b2" || hrefs[1] != "/traces?op=ORDERS_GET_X" {
		t.Fatalf("grup + trace linkleri: %v", hrefs)
	}
	fb, _ := json.Marshal(ans["followups"])
	if !strings.Contains(string(fb), "ORD-100 grubunu açıkla") || strings.Contains(string(fb), "ORD-200 grubunu") {
		t.Fatalf("çipler DİĞER grupları önermeli: %s", fb)
	}
	// Önbellek: ✨ düğmesinin formülüyle (explainCacheKey(system, in.User, "")) yazıldı.
	key := explainCacheKey(copilot.SystemPromptOracleException(), prov.user, "")
	if _, hit, _ := mc.Get(context.Background(), key); !hit {
		t.Fatal("cevap Exceptions açıklamasının anahtarına yazılmadı")
	}
	// İkinci tur: isabet → LLM yok, cached etiketi, saklanan exchangeId.
	calls := prov.calls
	evs = nil
	if h, o := s.opExceptionExplainInChat(copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "x-2"}), emit, p, time.UTC); !h || !o {
		t.Fatal("isabet turu işlenmedi")
	}
	ans = evs[len(evs)-1].data.(map[string]any)
	if prov.calls != calls || ans["cached"] != true || ans["exchangeId"] != "x-1" {
		t.Fatalf("isabet: calls %d→%d cached=%v xid=%v", calls, prov.calls, ans["cached"], ans["exchangeId"])
	}
	// Yüzey etiketi: kayıtlı, evalset Exception ailesi, çağrı yerinde sabit.
	if evalSurfaceFromLabel(aisurface.ExplainExceptionChat) != "Exception" || aisurface.ExplainExceptionChat != "explain-exception:chat" {
		t.Fatal("explain-exception:chat Exception ailesine çözülmüyor")
	}
	// v0.10.1400 — akış gövdesi paylaşılan çekirdekte (chat_explain_core.go).
	src := funcBody(readRepoFile(t, "chat_operation_exception_explain.go"), "opExceptionExplainInChat")
	// v0.10.1406 — + fresh=false (operasyon sorusu Yeniden üret niyeti taşımaz).
	if !strings.Contains(src, "s.streamExplainCore(ctx, emit, aisurface.ExplainExceptionChat, system, in.User, explainCacheKey(system, in.User, \"\"), false)") {
		t.Fatal("akış explain-exception:chat yüzeyinde, sarmalayıcısız in.User ve düğmenin anahtarıyla çekirdekten geçmeli")
	}
	if !strings.Contains(funcBody(readRepoFile(t, "chat_explain_core.go"), "streamExplainCore"), "s.copilotStreamSurface(ctx, surface, system, user,") {
		t.Fatal("çekirdek copilotStreamSurface üzerinden akmalı (/ai atfı)")
	}
}

func TestOpExceptionExplainStreamErrorIsHandled(t *testing.T) {
	prov := &opExplainProvider{fail: true}
	s := newOpExplainServer(t, prov)
	p, _ := opExplainFixture(t, nil)
	var kinds []string
	handled, ok := s.opExceptionExplainInChat(context.Background(), func(k string, _ any) { kinds = append(kinds, k) }, p, time.UTC)
	if !handled || ok || kinds[len(kinds)-1] != "error" {
		t.Fatalf("model hatası tur içinde söylenmeli: handled=%v ok=%v %v", handled, ok, kinds)
	}
}

func TestOpExceptionExplainFallsThrough(t *testing.T) {
	prov := &opExplainProvider{deltas: []string{"x"}}
	s := newOpExplainServer(t, prov)
	base, _ := opExplainFixture(t, nil)
	for _, c := range []struct {
		name  string
		mut   func(p *opPrefetch)
		srv   *Server
		steps int
	}{
		{"hata-neden değil", func(p *opPrefetch) { p.errorAsk = false }, s, 0},
		{"Exceptions bakılmadı", func(p *opPrefetch) { p.Exceptions = nil }, s, 0},
		{"Exceptions okunamadı", func(p *opPrefetch) {
			p.Exceptions = &exceptionGroupsEvidence{Err: "timeout", Groups: base.Exceptions.Groups}
		}, s, 0},
		{"grup yok", func(p *opPrefetch) { p.Exceptions = &exceptionGroupsEvidence{Groups: []exGroupLine{}} }, s, 0},
		{"yalnız resolved", func(p *opPrefetch) {
			p.Exceptions = &exceptionGroupsEvidence{Groups: []exGroupLine{{Fingerprint: "ora:00000000000000a1", Code: "ORD-100", State: "resolved"}}}
		}, s, 0},
		{"copilot yok", func(*opPrefetch) {}, &Server{}, 0},
	} {
		p := *base
		c.mut(&p)
		var kinds []string
		handled, ok := c.srv.opExceptionExplainInChat(context.Background(), func(k string, _ any) { kinds = append(kinds, k) }, &p, time.UTC)
		if handled || ok || len(kinds) != c.steps {
			t.Errorf("%s: handled=%v ok=%v olaylar=%v", c.name, handled, ok, kinds)
		}
	}
	if h, _ := (*Server)(nil).opExceptionExplainInChat(context.Background(), func(string, any) {}, base, time.UTC); h {
		t.Fatal("nil sunucu işlendi")
	}
	if h, _ := s.opExceptionExplainInChat(context.Background(), func(string, any) {}, nil, time.UTC); h {
		t.Fatal("nil paket işlendi")
	}
	if prov.calls != 0 {
		t.Fatalf("düşen yollarda LLM çağrıldı: %d", prov.calls)
	}
	// Girdi kurulamadı (grup okunamadı): adım çipi hatayı söyler, cevap YOK, düşer.
	p, _ := opExplainFixture(t, errors.New("grup okunamadı"))
	var kinds []string
	handled, ok := s.opExceptionExplainInChat(context.Background(), withStepIDs(func(k string, _ any) { kinds = append(kinds, k) }), p, time.UTC)
	if handled || ok || strings.Join(kinds, ",") != "step,step-result" || prov.calls != 0 {
		t.Fatalf("seam hatası: handled=%v ok=%v %v calls=%d", handled, ok, kinds, prov.calls)
	}
}

func TestOpPrefetchSkipsDrillOnErrorAskWithGroups(t *testing.T) {
	groups := &exceptionGroupsEvidence{Groups: []exGroupLine{{Fingerprint: "ora:00000000000000b2", Code: "ORD-200", State: "new"}}}
	for _, c := range []struct {
		name string
		t    opTarget
		p    *opPrefetch
		want bool
	}{
		{"hata-neden + grup", opTarget{ErrorAsk: true}, &opPrefetch{Exceptions: groups}, true},
		{"hata-neden değil", opTarget{}, &opPrefetch{Exceptions: groups}, false},
		{"grup yok", opTarget{ErrorAsk: true}, &opPrefetch{Exceptions: &exceptionGroupsEvidence{Groups: []exGroupLine{}}}, false},
		{"Exceptions okunamadı", opTarget{ErrorAsk: true}, &opPrefetch{Exceptions: &exceptionGroupsEvidence{Err: "x", Groups: groups.Groups}}, false},
		{"bakılmadı", opTarget{ErrorAsk: true}, &opPrefetch{}, false},
		{"yalnız ignored", opTarget{ErrorAsk: true}, &opPrefetch{Exceptions: &exceptionGroupsEvidence{Groups: []exGroupLine{{Fingerprint: "ora:00000000000000b2", State: "ignored"}}}}, false},
		{"nil paket", opTarget{ErrorAsk: true}, nil, false},
	} {
		if got := opDrillSkippedForErrorAsk(c.t, c.p); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	src := readRepoFile(t, "chat_operation_prefetch.go")
	if !strings.Contains(funcBody(src, "endpointDrillPhase"), "len(picks) > 0 && !opDrillSkippedForErrorAsk(t, p)") {
		t.Fatal("kırılım hata-neden + grup durumunda atlanmıyor")
	}
	chat := funcBody(src, "chatOperationPrefetch")
	i, j := strings.Index(chat, "t.ErrorAsk = opErrorWhyAsk(raw)"), strings.Index(chat, "return s.opPrefetchForTarget(pctx, emit, t, kind, rangeS, from, to), false")
	if i < 0 || j < 0 || i > j {
		t.Fatal("sohbet ön-getirmesi ErrorAsk'ı hedefe seam'den ÖNCE yazmalı")
	}
	if !strings.Contains(funcBody(src, "opPrefetchForTarget"), "errorAsk: t.ErrorAsk") {
		t.Fatal("paket hedefin ErrorAsk'ını taşımıyor")
	}
	// Çekmece endpoint kanıtı ErrorAsk'ı hiç yazmaz (false → bayt bayt eski).
	if strings.Contains(readRepoFile(t, "copilot_drawer.go"), "ErrorAsk") {
		t.Fatal("çekmece endpoint kanıtı ErrorAsk yazmamalı")
	}
}

// Kademe sırası: gate ön-getirmeden SONRA, guided'dan ÖNCE; handled → döner.
func TestOpExceptionExplainTierPinned(t *testing.T) {
	b := readRepoFile(t, "copilot_chat.go")
	src := stripGoCommentsAPI(b)
	iPre := strings.Index(src, "s.chatOperationPrefetch(")
	iGate := strings.Index(src, "s.opExceptionExplainInChat(ctx, emit, opPre,")
	iGuided := strings.Index(src, tierMarkers[0].marker)
	if iPre < 0 || iGate < 0 || iGuided < 0 || !(iPre < iGate && iGate < iGuided) {
		t.Fatalf("sıra: ön-getirme=%d kapı=%d guided=%d", iPre, iGate, iGuided)
	}
	if !strings.Contains(src[iPre:iGate], "if opPre != nil && opPre.errorAsk {") {
		t.Fatal("kapı yalnız hata-neden sorusunda açılmalı")
	}
	cond := src[iGate:]
	cond = cond[:strings.Index(cond, "{")]
	blk := tierBlock(src, iGate)
	if !strings.Contains(cond, "handled") || !strings.Contains(blk, `cspan.tier("operation", eok)`) || !strings.Contains(blk, "return") {
		t.Fatalf("kapı handled'da dönmüyor: %s %s", cond, blk)
	}
}
