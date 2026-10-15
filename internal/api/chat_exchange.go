package api

// chat_exchange.go — v0.10.1153 (operatör, prod): "/ai (AI insights) her
// CoSRE etkileşimini göstermiyor".
//
// Kök neden: /ai yalnız ai_calls satırlarını okuyor ve o tabloya yalnız
// MODEL ÇAĞRILARI yazılıyordu. Bir sohbet turunun kendisi hiçbir yerde
// kaydedilmiyordu:
//   - LLM'siz cevaplar (/help, bilinmeyen komut, kapsam "bunu mu demek
//     istedin", /wiki kapı mesajları, "Wikide bulunamadı", guided şablonları,
//     netleştirme, önbellekten gelen trace anlatımı …) HİÇ satır üretmiyordu;
//   - yardımcı çağrılar (niyet sınıflandırıcısı, wiki sayfa seçimi) bilerek
//     exchange'siz yazılıyordu (geri bildirim JOIN'i ikiye katlanmasın diye) —
//     /ai'da turla ilişkisiz satırlar olarak görünüyordu;
//   - kademe, rota, Derin bayrağı ve sonucu hiçbir satır taşımıyordu.
//
// Çözüm (yeni tablo/kolon YOK, mevcut kaydedici): her tur, kademesi ne olursa
// olsun, sonunda ai_calls'a TEK bir etkileşim satırı yazar
// (copilot.RecordExchange — RecordUsage'ın ikizi; aynı Recorder, aynı 4 KB
// tavan, 90 g TTL). Yüzey aisurface.TurnLabel (kademe/rota/derin), exchange
// kimliği aisurface.TurnExchangeID(kök). Turun model çağrıları kökle
// gruplanır: cevap çağrısı kökü AYNEN, yardımcı/işaret satırları "kök:yüzey"
// taşır. /api/ai/exchanges (ai_exchanges.go) bunları birleştirir.
//
// Kayıt merkezî: copilotChat'in tek defer'i — yeni bir kademe eklemek kaydı
// unutamaz (her erken dönüş de yazar). Soru ve cevap metni emit akışından
// okunur (tap); kademe chatSpan'dan.

import (
	"context"
	"strings"
	"sync"
	"time"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// chatExchange — bir CoSRE turunun etkileşim durumu. tap birden çok
// kademeden (ve kurtarma sarmalayıcısından) çağrılabilir → kilitli.
type chatExchange struct {
	mu       sync.Mutex
	root     string
	question string
	deep     bool
	started  time.Time
	route    string
	answer   string
	errMsg   string

	// CoSRE öğrenme Faz 1 (cosre_interactions.go) — turun etkileşim satırı.
	conversation string
	turn         uint16
	attempt      string
	page         *agentctx.PageContext
	ctxService   string
	ctxTrace     string
	tools        []chstore.CosreTool
	toolAt       map[int]int       // step i → tools indeksi
	stepAt       map[int]time.Time // step i → çip zamanı (süre için)
	firstOut     time.Time         // ilk görünür çıktı (delta/answer) → ttft
	model        string
	modelCalls   uint16
	inTok        uint32
	outTok       uint32
}

// setInteraction — istek bağlamı (konuşma, tur no, sayfa, istemci denemesi).
func (x *chatExchange) setInteraction(conversation string, turn int, attempt string, page *agentctx.PageContext, service, trace string) {
	if x == nil {
		return
	}
	if turn > 65535 {
		turn = 65535
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.conversation, x.turn, x.attempt, x.page = conversation, uint16(turn), normalizeChatAttempt(attempt), page
	x.ctxService, x.ctxTrace = service, trace
}

// chatUserTurns — SAF: istekteki kullanıcı mesajı sayısı (tur no, 1'den).
func chatUserTurns(msgs []copilot.ChatMessage) int {
	n := 0
	for _, m := range msgs {
		if m.Role == "user" {
			n++
		}
	}
	return n
}

// normalizeChatAttempt — SAF: istemci beyanı kapalı kümeye (LC kolonu).
func normalizeChatAttempt(a string) string {
	switch a {
	case "retry", "regenerate", "edit":
		return a
	}
	return ""
}

// noteModelCall — copilot.CallSink: turdaki her model çağrısı (son model kazanır).
func (x *chatExchange) noteModelCall(model string, in, out uint32) {
	if x == nil {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if model != "" {
		x.model = model
	}
	if x.modelCalls < 65535 {
		x.modelCalls++
	}
	x.inTok += in
	x.outTok += out
}

// noteStep — tool çipi (step olayı, kimlik damgalandıktan SONRA). Etiket
// çipleri (tool yok) tool değildir.
func (x *chatExchange) noteStep(v any, now time.Time) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	tool, _ := m["tool"].(string)
	i, hasI := eventInt(m["i"])
	if tool == "" || !hasI {
		return
	}
	args, _ := m["args"].(string)
	origin, _ := m["origin"].(string)
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.toolAt == nil {
		x.toolAt, x.stepAt = map[int]int{}, map[int]time.Time{}
	}
	x.toolAt[i] = len(x.tools)
	x.stepAt[i] = now
	x.tools = append(x.tools, chstore.CosreTool{Name: tool, Origin: origin, Args: clipBytes(args, cosreToolArgsMax), OK: true})
}

// noteStepResult — çipin kanıtı/hatası (step-result olayı).
func (x *chatExchange) noteStepResult(v any, now time.Time) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	i, hasI := eventInt(m["i"])
	if !hasI {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	idx, found := x.toolAt[i]
	if !found {
		return
	}
	t := &x.tools[idx]
	okv, _ := m["ok"].(bool)
	preview, _ := m["preview"].(string)
	t.OK = okv
	if b, has := eventInt(m["bytes"]); has && b >= 0 {
		t.Bytes = uint32(b)
	}
	if okv {
		t.Output = clipBytes(preview, cosreToolOutputMax)
	} else {
		t.Error = clipBytes(preview, cosreToolErrMax)
	}
	if sk, _ := m["skipped"].(bool); sk {
		t.OK, t.Error = false, "skipped"
	}
	if d, has := eventInt(m["durationMs"]); has && d >= 0 {
		t.Ms = uint32(d)
	} else if at, has := x.stepAt[i]; has {
		t.Ms = uint32(now.Sub(at).Milliseconds())
	}
}

// eventInt — SAF: SSE yükündeki sayı (int / int64 / float64).
func eventInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

type chatExchangeKey struct{}

func newChatExchange(root, question string, deep bool, started time.Time) *chatExchange {
	return &chatExchange{root: root, question: strings.TrimSpace(question), deep: deep, started: started}
}

func withChatExchange(ctx context.Context, x *chatExchange) context.Context {
	return context.WithValue(ctx, chatExchangeKey{}, x)
}

// chatExchangeFrom — ctx'teki tur; sohbet dışında nil (nil-güvenli yöntemler).
func chatExchangeFrom(ctx context.Context) *chatExchange {
	if ctx == nil {
		return nil
	}
	x, _ := ctx.Value(chatExchangeKey{}).(*chatExchange)
	return x
}

// noteRoute — guided rotası (runGuidedRoute girişi). İlk rota kalır: kurtarma
// / takip yolları ikinci bir rota çalıştırsa da turun yönlendirmesi ilkidir.
func (x *chatExchange) noteRoute(route string) {
	if x == nil || route == "" {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.route == "" {
		x.route = route
	}
}

// tap — emit sarmalayıcısı: "answer" metnini (son cevap kazanır) ve "error"
// olayını yakalar, olayı DEĞİŞTİRMEDEN iletir.
func (x *chatExchange) tap(emit func(string, any)) func(string, any) {
	return func(kind string, v any) {
		now := time.Now()
		switch kind {
		case "answer":
			x.mu.Lock()
			x.answer = eventField(v, "text")
			if x.firstOut.IsZero() {
				x.firstOut = now
			}
			x.mu.Unlock()
		case "delta":
			x.mu.Lock()
			if x.firstOut.IsZero() {
				x.firstOut = now
			}
			x.mu.Unlock()
		case "error":
			x.mu.Lock()
			x.errMsg = eventField(v, "error")
			x.mu.Unlock()
		case "step-result":
			x.noteStepResult(v, now)
		}
		emit(kind, v)
		if kind == "step" {
			// kimlik (i) iç sarmalayıcıda (withStepIDs) YERİNDE damgalanır.
			x.noteStep(v, now)
		}
	}
}

// eventField — SAF: SSE yükünden dize alan (yükler map[string]any ya da
// map[string]string).
func eventField(v any, key string) string {
	switch m := v.(type) {
	case map[string]any:
		s, _ := m[key].(string)
		return s
	case map[string]string:
		return m[key]
	}
	return ""
}

// chatExchangeOutcome — SAF: turun sonucu. Kademe başarısızsa (chatSpan.err)
// ya da akışa hata olayı düştüyse "error"; cevap yokken istek iptal olduysa
// (istemci gitti / alışveriş tavanı) yine "error" — sessiz kayıp sayılmaz.
func chatExchangeOutcome(tierErr error, emittedErr string, answered bool, ctxErr error) (status, msg string) {
	switch {
	case emittedErr != "":
		return "error", emittedErr
	case tierErr != nil:
		return "error", tierErr.Error()
	case !answered && ctxErr != nil:
		return "error", ctxErr.Error()
	}
	return "ok", ""
}

// recordChatExchange — turun etkileşim satırı (copilotChat'in defer'i).
// ctx alışverişin ctx'i (kullanıcı meta'sı, açık/derin profil); iptal edilmiş
// olabilir — kayıt kendi sınırlı ctx'iyle yazılır (copilot.emitRecord).
func (s *Server) recordChatExchange(ctx context.Context, x *chatExchange, cs *chatSpan) {
	if s == nil || s.copilot == nil || x == nil {
		return
	}
	tier, tierErr := aisurface.TierLoop, error(nil)
	if cs != nil {
		tier, tierErr = cs.tierV, cs.err
	}
	x.mu.Lock()
	answer, emittedErr, route := x.answer, x.errMsg, x.route
	x.mu.Unlock()
	status, msg := chatExchangeOutcome(tierErr, emittedErr, answer != "", ctx.Err())
	m := copilot.MetaFromContext(ctx)
	m.Surface = aisurface.TurnLabel(tier, route, x.deep)
	m.ExchangeID = aisurface.TurnExchangeID(x.root)
	m.Observe, m.Shield, m.PromptLogOverride = nil, nil, ""
	s.copilot.RecordExchange(copilot.WithMeta(ctx, m), x.started, status, msg, x.question, answer)
	s.enqueueCosreInteraction(buildCosreInteraction(x, tier, m, status, msg, time.Now()))
}

// buildCosreInteraction — SAF (x kilidi altında okur): turun etkileşim satırı.
func buildCosreInteraction(x *chatExchange, tier string, m copilot.CallMeta, status, msg string, now time.Time) chstore.CosreInteraction {
	x.mu.Lock()
	defer x.mu.Unlock()
	answer := strings.TrimSpace(x.answer)
	r := chstore.CosreInteraction{
		ExchangeID: x.root, ConversationID: x.conversation, Turn: x.turn,
		CreatedAt: x.started, UserID: m.UserID, UserEmail: m.UserEmail,
		Tier: tier, Surface: m.Surface,
		Service: x.ctxService, TraceID: x.ctxTrace,
		Question:    clipBytes(x.question, cosreQuestionMax),
		Answer:      clipBytes(answer, cosreAnswerMax),
		AnswerChars: uint32(len(answer)),
		Tools:       append([]chstore.CosreTool(nil), x.tools...),
		Model:       x.model, ModelCalls: x.modelCalls, InputTokens: x.inTok, OutputTokens: x.outTok,
		Status: status, ErrorMsg: clipBytes(msg, cosreToolErrMax),
		Attempt: x.attempt, Deep: x.deep,
		EmptyAnswer: answer == "",
		NoEvidence:  answerAdmitsNoEvidence(answer),
	}
	if p := x.page; p != nil {
		r.Page, r.ProblemID, r.ExceptionID = p.Page, p.ProblemID, p.ExceptionID
		if p.Service != "" {
			r.Service = p.Service
		}
		if p.TraceID != "" {
			r.TraceID = p.TraceID
		}
	}
	if !x.started.IsZero() {
		r.TotalMs = uint32(now.Sub(x.started).Milliseconds())
		if !x.firstOut.IsZero() {
			r.TTFTMs = uint32(x.firstOut.Sub(x.started).Milliseconds())
		}
	}
	return r
}
