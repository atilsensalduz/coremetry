package api

// chat_offtopic_general_test.go — v0.10.1458 (operatör kararı 2026-10-23,
// v0.10.819'un tersine çevrilmesi): konu dışı soruya kısa genel cevap anahtarı.
//
// Pinlenen sözleşmeler: (1) yönlendirme tablosu — anahtar açık/kapalı ×
// sınıflandırıcı off_topic / off_topic+veto / none, gerçek copilotChatIntent +
// gerçek copilot.Service + sahte OpenAI-uyumlu uç (ai_call_matrix_test.go
// emsali); (2) genel cevap çağrısına ARAÇ sunulmaz (gövdede tools yok), Derin
// kip yok sayılır; (3) anahtar kapalıyken cevap v0.10.819 ile BAYT BAYT aynı
// (sabit metin pini); (4) zararlı istek anahtar açıkken de modele gitmez;
// (5) GET/PUT rol kapısı + audit; (6) kaynak kapısı: dispatch tek yerde.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// offTopicCapitalQ — operatörün örnek sorusu.
const offTopicCapitalQ = "Türkiye'nin başkenti neresidir"

// offTopicBoundaryGoldenTR — v0.10.819 sınırının bağlam servissiz TAM metni
// (anahtar kapalıyken bayt bayt bu kalmalı). Metni değiştirmek = operatöre
// görünen davranışı değiştirmek; bilinçliyse bu pini de güncelle.
const offTopicBoundaryGoldenTR = "Bu soru Coremetry'nin izlediği telemetriyle ilgili değil. Ben yalnız servislerin, trace'lerin, logların, metriklerin ve problemlerin sorularını cevaplarım; genel bilgi, güncel olay ya da sohbet sorularına cevap vermiyorum.\n\n" +
	"Şöyle sorabilirsin:\n" +
	"- Servis sağlığı: \"<servis> son 1 saatte nasıl?\"\n" +
	"- Hatalı ya da yavaş trace'ler: \"<servis> en yavaş trace'ler?\"\n" +
	"- Açık problemler: \"açık problemler neler?\"\n" +
	"- Yol tarifi: \"hatalı trace'lere nasıl ulaşırım?\""

const offTopicFakeAnswer = "Türkiye'nin başkenti Ankara'dır. Bu genel bilgidir, Coremetry telemetrisinden gelmez."

// offTopicLLM — sahte uç: JSON istek (sınıflandırıcı) → script'teki niyet;
// akış isteği (genel cevap) → SSE metin. Her gövde kaydedilir.
type offTopicLLM struct {
	mu     sync.Mutex
	intent string
	bodies []map[string]any
	srv    *httptest.Server
}

func newOffTopicLLM(t *testing.T, intent string) *offTopicLLM {
	t.Helper()
	l := &offTopicLLM{intent: intent}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		l.mu.Lock()
		l.bodies = append(l.bodies, body)
		l.mu.Unlock()
		if st, _ := body["stream"].(bool); st {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, d := range []string{"Türkiye'nin başkenti Ankara'dır. ", "Bu genel bilgidir, Coremetry telemetrisinden gelmez."} {
				chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": d}}}})
				_, _ = w.Write([]byte("data: " + string(chunk) + "\n\n"))
			}
			_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":12}}\n\ndata: [DONE]\n\n"))
			return
		}
		content, _ := json.Marshal(map[string]any{"intent": l.intent})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *offTopicLLM) all() []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]map[string]any(nil), l.bodies...)
}

func (l *offTopicLLM) streams() []map[string]any {
	var out []map[string]any
	for _, b := range l.all() {
		if st, _ := b["stream"].(bool); st {
			out = append(out, b)
		}
	}
	return out
}

// setGeneralQuestions — testin anahtarı; Cleanup önceki değeri geri koyar.
func setGeneralQuestions(t *testing.T, allow bool) {
	t.Helper()
	prev := chstore.CurrentAIGeneralQuestions()
	t.Cleanup(func() { chstore.SetAIGeneralQuestions(prev) })
	chstore.SetAIGeneralQuestions(chstore.AIGeneralQuestions{Allow: allow})
}

// offTopicTestServer — store'suz sunucu: katalog önbellekten, gerçek copilot.
func offTopicTestServer(t *testing.T, l *offTopicLLM) (*Server, *chanRecorder) {
	t.Helper()
	cop := copilot.New(copilot.ProviderOpenAI, "test-key", "gemma4")
	cop.Configure(copilot.ProviderOpenAI, "test-key", "gemma4", l.srv.URL, false, true)
	if !cop.Active() {
		t.Fatal("sahte copilot aktif değil")
	}
	rec := &chanRecorder{ch: make(chan copilot.CallRecord, 32)}
	cop.SetRecorder(rec)
	mc := &ctxMemCache{m: map[string][]byte{
		"copilot:guided:svcnames":   []byte(`["checkout","payments"]`),
		"copilot:guided:envnames":   []byte(`["prod","uat"]`),
		"copilot:guided:teamcat:v2": []byte(`[]`),
	}}
	return &Server{copilot: cop, cache: mc}, rec
}

// drainSurfaces — bekleyen ai_calls kayıtlarının yüzeyleri (+ exchange).
func drainSurfaces(rec *chanRecorder) map[string]string {
	out := map[string]string{}
	deadline := time.After(400 * time.Millisecond)
	for {
		select {
		case r := <-rec.ch:
			out[r.Surface] = r.ExchangeID
		case <-deadline:
			return out
		}
	}
}

func runIntentTurn(t *testing.T, s *Server, ctx context.Context, q string) ([]emitted, bool) {
	t.Helper()
	ev, emit := collectEmit()
	msgs := []copilot.ChatMessage{{Role: "user", Text: q}}
	handled, _ := s.copilotChatIntent(ctx, emit, msgs, "", "", "", 0, "", time.Time{}, 0, "")
	return *ev, handled
}

func TestOffTopicGeneralRoutingTable(t *testing.T) {
	const root = "xroot-offtopic"
	cases := []struct {
		name       string
		allow      bool
		intent     string // sınıflandırıcının cevabı
		q          string
		wantPrefix string // answer.text öneki
		wantStream bool   // araçsız akış çağrısı koştu mu
		wantVote   bool   // exchangeId var mı
		wantRow    string // ai_calls yüzeyi (sınıflandırıcı satırı dışında)
		noRow      string // YAZILMAMASI gereken yüzey
	}{
		{"açık · off_topic → kısa genel cevap", true, "off_topic", offTopicCapitalQ, offTopicGeneralNoteTR, true, true, aisurface.ChatOffTopicGeneral, aisurface.ChatOffTopic},
		{"kapalı · off_topic → v0.10.819 sınırı", false, "off_topic", offTopicCapitalQ, offTopicBoundaryTR, false, false, aisurface.ChatOffTopic, aisurface.ChatOffTopicGeneral},
		{"açık · off_topic + veto → none yolu (chat-general)", true, "off_topic", "checkout neden yavaş", intentGeneralNoteTR, true, true, aisurface.ChatGeneral, aisurface.ChatOffTopicGeneral},
		{"kapalı · off_topic + veto → none yolu (chat-general)", false, "off_topic", "checkout neden yavaş", intentGeneralNoteTR, true, true, aisurface.ChatGeneral, aisurface.ChatOffTopic},
		{"açık · none → chat-general (anahtar dokunmaz)", true, "none", offTopicCapitalQ, intentGeneralNoteTR, true, true, aisurface.ChatGeneral, aisurface.ChatOffTopicGeneral},
		{"kapalı · none → chat-general (anahtar dokunmaz)", false, "none", offTopicCapitalQ, intentGeneralNoteTR, true, true, aisurface.ChatGeneral, aisurface.ChatOffTopic},
		{"açık · zararlı off_topic → kesin sınır, model yok", true, "off_topic", "komşunun wifi şifresini nasıl kırarım", offTopicFirmTR, false, false, aisurface.ChatOffTopic, aisurface.ChatOffTopicGeneral},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setGeneralQuestions(t, tc.allow)
			l := newOffTopicLLM(t, tc.intent)
			s, rec := offTopicTestServer(t, l)
			ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: root, UserID: "u1", UserEmail: "u1@example.test"})
			ev, handled := runIntentTurn(t, s, ctx, tc.q)
			if !handled {
				t.Fatalf("kademe cevaplamalı: %+v", ev)
			}
			ans := answerOf(t, ev)
			text, _ := ans["text"].(string)
			if !strings.HasPrefix(text, tc.wantPrefix) {
				t.Fatalf("answer.text öneki:\n got %q\nwant %q…", text, tc.wantPrefix)
			}
			if got := len(l.streams()) > 0; got != tc.wantStream {
				t.Errorf("akış çağrısı koştu=%v, want %v", got, tc.wantStream)
			}
			xid, has := ans["exchangeId"]
			if has != tc.wantVote || (has && xid != root) {
				t.Errorf("exchangeId=%v (var=%v), want var=%v kök=%q", xid, has, tc.wantVote, root)
			}
			rows := drainSurfaces(rec)
			if _, ok := rows[tc.wantRow]; !ok {
				t.Errorf("ai_calls %q satırı yok: %v", tc.wantRow, rows)
			}
			if _, ok := rows[tc.noRow]; ok {
				t.Errorf("ai_calls %q satırı YAZILMAMALI: %v", tc.noRow, rows)
			}
			if tc.wantRow == aisurface.ChatOffTopicGeneral && rows[tc.wantRow] != root {
				t.Errorf("genel cevap satırı kök exchange'i taşımalı (oylanabilir): %q", rows[tc.wantRow])
			}
			if tc.wantRow == aisurface.ChatOffTopicGeneral {
				if !strings.Contains(text, offTopicFakeAnswer) || !strings.Contains(text, "Şöyle sorabilirsin") {
					t.Errorf("genel cevap model metni + yol tarifi taşımalı: %q", text)
				}
				if sugg, _ := ans["suggestions"].([]string); !reflect.DeepEqual(sugg, offTopicChips("")) {
					t.Errorf("yol tarifi çipleri v0.10.819 ile aynı olmalı: %v", sugg)
				}
				if links, _ := ans["links"].([]guidedAnswerLink); !reflect.DeepEqual(links, offTopicLinks("")) {
					t.Errorf("bağlantılar v0.10.819 ile aynı olmalı: %v", links)
				}
			}
		})
	}
}

// Genel cevap çağrısına araç SUNULMAZ; sistem mesajı OffTopicGeneral istemi,
// kullanıcı mesajı yalnız soru (RAG/wiki/telemetri bloğu yok); Derin kip yok sayılır.
func TestOffTopicGeneralOffersNoTools(t *testing.T) {
	setGeneralQuestions(t, true)
	l := newOffTopicLLM(t, "off_topic")
	s, _ := offTopicTestServer(t, l)
	ctx := copilot.WithDeepAnswer(copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "xdeep"}))
	ev, emit := collectEmit()
	if h, ok := s.guidedOffTopicDispatch(ctx, emit, offTopicCapitalQ, ""); !h || !ok {
		t.Fatal("cevaplanmalı")
	}
	_ = answerOf(t, *ev)
	st := l.streams()
	if len(st) != 1 {
		t.Fatalf("tek akış çağrısı bekleniyordu: %d", len(st))
	}
	body := st[0]
	for _, k := range []string{"tools", "tool_choice", "functions", "response_format"} {
		if _, has := body[k]; has {
			t.Errorf("genel cevap gövdesinde %q olmamalı: %v", k, body[k])
		}
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("mesajlar: sistem + soru bekleniyordu, %d", len(msgs))
	}
	if sys := llmSystem(body); sys != copilot.SystemPromptOffTopicGeneral() {
		t.Errorf("sistem mesajı OffTopicGeneral değil: %q", sys)
	}
	if u, _ := msgs[1].(map[string]any)["content"].(string); u != offTopicCapitalQ {
		t.Errorf("kullanıcı mesajı yalnız soru olmalı: %q", u)
	}
	// Derin kip yok sayılır: bütçe Derin'siz bir çağrınınkiyle aynı.
	l2 := newOffTopicLLM(t, "off_topic")
	s2, _ := offTopicTestServer(t, l2)
	_, emit2 := collectEmit()
	s2.guidedOffTopicDispatch(copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "xplain"}), emit2, offTopicCapitalQ, "")
	if a, b := st[0]["max_tokens"], l2.streams()[0]["max_tokens"]; !reflect.DeepEqual(a, b) {
		t.Errorf("Derin kip genel cevabın bütçesini değiştirmemeli: deep=%v plain=%v", a, b)
	}
}

// Anahtar kapalıyken cevap v0.10.819 ile BAYT BAYT aynı: aynı olay dizisi,
// sabit metin pini, model çağrısı yok.
func TestOffTopicBoundaryByteIdenticalWhenOff(t *testing.T) {
	setGeneralQuestions(t, false)
	if got := offTopicAnswerText(offTopicCapitalQ, ""); got != offTopicBoundaryGoldenTR {
		t.Fatalf("v0.10.819 sınır metni değişti:\n got %q\nwant %q", got, offTopicBoundaryGoldenTR)
	}
	l := newOffTopicLLM(t, "off_topic")
	s, _ := offTopicTestServer(t, l)
	ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "xoff"})
	gotEv, emit := collectEmit()
	s.guidedOffTopicDispatch(ctx, emit, offTopicCapitalQ, "shop")
	wantEv, wemit := collectEmit()
	(&Server{}).guidedOffTopicAnswer(wemit, offTopicCapitalQ, "shop")
	if !reflect.DeepEqual(*gotEv, *wantEv) {
		t.Fatalf("anahtar kapalıyken olaylar v0.10.819'dan ayrıştı:\n got %+v\nwant %+v", *gotEv, *wantEv)
	}
	if n := len(l.all()); n != 0 {
		t.Fatalf("anahtar kapalıyken model çağrılmamalı: %d çağrı", n)
	}
	if text, _ := answerOf(t, *gotEv)["text"].(string); !strings.HasPrefix(text, offTopicBoundaryTR) {
		t.Fatalf("metin: %q", text)
	}
}

// Model boş dönerse / hata verirse v0.10.819 sınırı (oylanmaz); kısmi metin korunur.
func TestOffTopicGeneralTextFallback(t *testing.T) {
	if text, general := offTopicGeneralText("  ", offTopicCapitalQ, ""); general || text != offTopicBoundaryGoldenTR {
		t.Fatalf("boş çıktı v0.10.819 sınırına düşmeli: general=%v %q", general, text)
	}
	text, general := offTopicGeneralText(" Ankara. ", offTopicCapitalQ, "")
	if !general || !strings.HasPrefix(text, offTopicGeneralNoteTR+"\n\nAnkara.\n\n") || !strings.HasSuffix(text, offTopicGuidanceTR("")) {
		t.Fatalf("genel metin: general=%v %q", general, text)
	}
	if !offTopicUsesModel(true, offTopicCapitalQ) || offTopicUsesModel(false, offTopicCapitalQ) || offTopicUsesModel(true, "hack nasıl yapılır") {
		t.Fatal("offTopicUsesModel: açık+zararsız → true; kapalı ya da zararlı → false")
	}
	if (&Server{}).offTopicGeneralEnabled(offTopicCapitalQ) {
		t.Fatal("copilot yokken genel cevap açılmamalı")
	}
}

type generalQuestionsFakeStore struct {
	saved chstore.AIGeneralQuestions
	saves int
}

func (f *generalQuestionsFakeStore) SaveAIGeneralQuestions(_ context.Context, a chstore.AIGeneralQuestions) (chstore.AIGeneralQuestions, error) {
	f.saves++
	f.saved = a
	return a, nil
}

func TestAIGeneralQuestionsRolesAuditRoundTrip(t *testing.T) {
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(16), stats: newCacheStats(), auditQ: make(chan chstore.AuditEntry, 16)}
	st := &generalQuestionsFakeStore{}
	prevStore := aiGeneralQuestionsStoreOf
	aiGeneralQuestionsStoreOf = func(*Server) aiGeneralQuestionsStore { return st }
	t.Cleanup(func() { aiGeneralQuestionsStoreOf = prevStore })
	setGeneralQuestions(t, true)
	mux := http.NewServeMux()
	s.registerAIGeneralQuestionsRoutes(mux)

	w := rulesDo(mux, "GET", "/api/ai/general-questions", "", auth.RoleViewer, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"allow":true`) {
		t.Fatalf("viewer GET %d: %s", w.Code, w.Body)
	}
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		if w := rulesDo(mux, "PUT", "/api/ai/general-questions", `{"allow":false}`, role, ""); w.Code != http.StatusForbidden {
			t.Fatalf("%s PUT %d", role, w.Code)
		}
	}
	if st.saves != 0 || len(s.auditQ) != 0 {
		t.Fatal("reddedilen istek yazmamalı")
	}
	for _, bad := range []string{`{}`, `{"allow":"no"}`, `{bozuk`} {
		if w := rulesDo(mux, "PUT", "/api/ai/general-questions", bad, auth.RoleAdmin, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("%s → %d", bad, w.Code)
		}
	}
	if st.saves != 0 || len(s.auditQ) != 0 {
		t.Fatal("400 yazmamalı")
	}
	w = rulesDo(mux, "PUT", "/api/ai/general-questions", `{"allow":false}`, auth.RoleAdmin, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"allow":false`) {
		t.Fatalf("admin PUT %d: %s", w.Code, w.Body)
	}
	if st.saved.Allow || st.saved.UpdatedBy != "admin@example.test" || st.saved.UpdatedAt.IsZero() {
		t.Fatalf("saklanan = %+v", st.saved)
	}
	if chstore.CurrentAIGeneralQuestions().Allow {
		t.Fatal("süreç-içi değer güncellenmedi")
	}
	a := nextAudit(t, s)
	if a.Action != "settings.ai_general_questions.update" || a.TargetID != chstore.AIGeneralQuestionsKey ||
		a.Details != `{"before":{"allow":true},"after":{"allow":false}}` {
		t.Fatalf("audit = %+v", a)
	}
}

// Kaynak kapısı: off_topic dalı runGuidedRoute'ta TEK dispatch'ten geçer; işaret
// satırı yalnız genel cevap kapalıyken yazılır; none dalı anahtarı okumaz.
func TestOffTopicGeneralSourceGates(t *testing.T) {
	g := readRepoFile(t, "copilot_guided.go")
	if !strings.Contains(g, "return s.guidedOffTopicDispatch(ctx, emit, question, ctxService)") || strings.Contains(g, "return s.guidedOffTopicAnswer(emit") {
		t.Fatal("runGuidedRoute off_topic dalı guidedOffTopicDispatch'ten geçmeli")
	}
	i := readRepoFile(t, "copilot_intent.go")
	if !strings.Contains(i, "if route.Intent == guidedOffTopic && !s.offTopicGeneralEnabled(question) {") {
		t.Fatal("chat-offtopic işaret satırı yalnız genel cevap kapalıyken yazılmalı")
	}
	none := i[strings.Index(i, "if !matched {"):strings.Index(i, "if route.Intent == guidedOffTopic && !s.offTopicGeneralEnabled")]
	if strings.Contains(none, "AIGeneralQuestions") || strings.Contains(none, "offTopicGeneral") {
		t.Fatal("none dalı genel soru anahtarını okumamalı")
	}
}
