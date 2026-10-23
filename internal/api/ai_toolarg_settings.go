package api

// ai_toolarg_settings.go — v0.10.1456 (CoSRE araştırma programı C1, karar 6):
// araç argümanı şema doğrulamasının operatör yüzeyi + sohbet yüzeyinin dikişi.
//
//   GET /api/ai/toolarg-validation  → {chat, sreLoop, mcp, updatedBy, updatedAt, modes}   her rol (viewer GÖRÜR)
//   PUT /api/ai/toolarg-validation  {chat?, sreLoop?}                                     admin · audit settings.ai_toolarg_validation.update
//
// Depo system_settings `ai.toolArgValidation` (chstore/ai_toolarg_settings.go;
// boot + 30 s cfgRefresh her rolde). Varsayılan chat=warn (operatör kararı 3:
// bir hafta `ai.tool.args_validation{mode=warn,result=invalid}` sayacına bak,
// sonra enforce), sreLoop=enforce. mcp alanı bugün yalnız "off" (dış MCP teli
// doğrulanmaz; farklısı 400 — "açtım" yanılsaması yok).
//
// Neden requireCopilot'suz, ai_toolsets.go ile aynı yerde: kurulum ayarı, AI
// sağlayıcısından bağımsız görünür. GET serveCached'siz: atomic okuma, sorgu
// yok. PUT'ta gönderilmeyen alan MEVCUT değeri korur (UI'sı sürüm 6'da gelir;
// tek alanlık curl diğer yüzeyi sıfırlamasın).
//
// Sohbet dikişi chatToolArgScope: serbest döngü (copilot_chat.go) ve exception
// takibi (chat_exception_followup.go) Executor'a `.WithScope(…)` ile takar.
// warn kipinde modele giden HİÇBİR şey değişmez (çağrı koşar, içerik aynı);
// yalnız log + metrik + ai.tool span özniteliği + ai_calls error_msg notu.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

func init() {
	registerRoutesExtra("ai-toolarg-validation", (*Server).registerAIToolArgValidationRoutes)
}

func (s *Server) registerAIToolArgValidationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/toolarg-validation", s.getAIToolArgValidation)
	mux.Handle("PUT /api/ai/toolarg-validation", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putAIToolArgValidation)))
}

// aiToolArgStore — depo dikişi (test sahte depo verir).
type aiToolArgStore interface {
	SaveAIToolArgValidation(ctx context.Context, a chstore.AIToolArgValidation) (chstore.AIToolArgValidation, error)
}

var aiToolArgStoreOf = func(s *Server) aiToolArgStore {
	if s.store == nil {
		return nil
	}
	return s.store
}

var toolArgModes = []string{chstore.ToolArgModeOff, chstore.ToolArgModeWarn, chstore.ToolArgModeEnforce}

func aiToolArgPayload(a chstore.AIToolArgValidation) map[string]any {
	return map[string]any{
		"chat": a.Chat, "sreLoop": a.SreLoop, "mcp": a.MCP,
		"updatedBy": a.UpdatedBy, "updatedAt": a.UpdatedAt, "modes": toolArgModes,
	}
}

func (s *Server) getAIToolArgValidation(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, aiToolArgPayload(chstore.CurrentAIToolArgValidation()))
}

// aiToolArgPut — PUT gövdesi; nil alan = mevcut korunur.
type aiToolArgPut struct {
	Chat    *string `json:"chat"`
	SreLoop *string `json:"sreLoop"`
	MCP     *string `json:"mcp"`
}

// mergeToolArgPut — SAF: istek + mevcut → saklanacak blob ya da 400 metni.
func mergeToolArgPut(cur chstore.AIToolArgValidation, in aiToolArgPut) (chstore.AIToolArgValidation, error) {
	pick := func(field string, v *string, keep string) (string, error) {
		if v == nil {
			return keep, nil
		}
		m := strings.ToLower(strings.TrimSpace(*v))
		if !chstore.ValidToolArgMode(m) {
			return "", fmt.Errorf("%s: geçersiz kip %q (off | warn | enforce)", field, *v)
		}
		return m, nil
	}
	out := cur
	var err error
	if out.Chat, err = pick("chat", in.Chat, cur.Chat); err != nil {
		return cur, err
	}
	if out.SreLoop, err = pick("sreLoop", in.SreLoop, cur.SreLoop); err != nil {
		return cur, err
	}
	if in.MCP != nil && strings.ToLower(strings.TrimSpace(*in.MCP)) != chstore.ToolArgModeOff {
		return cur, fmt.Errorf("mcp: dış MCP teli bu sürümde doğrulanmıyor — yalnız \"off\"")
	}
	return out, nil
}

func (s *Server) putAIToolArgValidation(w http.ResponseWriter, r *http.Request) {
	st := aiToolArgStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu yok")
		return
	}
	var in aiToolArgPut
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	prev := chstore.CurrentAIToolArgValidation()
	next, err := mergeToolArgPut(prev, in)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if c := auth.FromContext(r.Context()); c != nil {
		next.UpdatedBy = c.Email
		if next.UpdatedBy == "" {
			next.UpdatedBy = c.UserID
		}
	}
	next.UpdatedAt = time.Now().UTC()
	saved, err := st.SaveAIToolArgValidation(r.Context(), next)
	if err != nil {
		writeErr(w, err)
		return
	}
	chstore.SetAIToolArgValidation(saved) // sahte depo testleri için de yayınla
	s.audit(r, "settings.ai_toolarg_validation.update", "settings", chstore.AIToolArgValidationKey, aiToolArgAuditDetails(prev, saved))
	writeJSON(w, aiToolArgPayload(saved))
}

// aiToolArgAuditDetails — SAF: önce → sonra.
func aiToolArgAuditDetails(prev, next chstore.AIToolArgValidation) string {
	b, _ := json.Marshal(map[string]any{
		"before": map[string]string{"chat": prev.Chat, "sreLoop": prev.SreLoop, "mcp": prev.MCP},
		"after":  map[string]string{"chat": next.Chat, "sreLoop": next.SreLoop, "mcp": next.MCP},
	})
	return string(b)
}

// ── sohbet yüzeyi ────────────────────────────────────────────────────

// chatToolArgWarnNoteMax — ai_calls error_msg notunun araç tavanı.
const chatToolArgWarnNoteMax = 4

// chatToolArgObserver — bir sohbet alışverişinin doğrulama gözlemleri: ai.tool
// span özniteliği (bir sonraki span'a) + ai_calls notu. Executor senkron
// (Observe → Span aynı goroutine'de, arka arkaya); mutex savunma.
type chatToolArgObserver struct {
	mode    string
	mu      sync.Mutex
	pending map[string]bool // ad → args_valid (span açılınca tüketilir)
	invalid []string        // "tool(alan:kural,…)" — not için, sıralı benzersiz
	seen    map[string]bool
}

func (o *chatToolArgObserver) observe(name, _ string, issues []mcp.ArgIssue) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pending == nil {
		o.pending, o.seen = map[string]bool{}, map[string]bool{}
	}
	o.pending[name] = len(issues) == 0
	if len(issues) == 0 {
		return
	}
	entry := name + "(" + agenttools.IssuesSummary(issues) + ")"
	if !o.seen[entry] {
		o.seen[entry] = true
		o.invalid = append(o.invalid, entry)
	}
}

// wrapSpan — Executor'ın Span kancasını sarar: doğrulanmış çağrının span'ına
// coremetry.ai.tool.args_valid basılır (doğrulanmayan — kip off / dış araç —
// öznitelik taşımaz).
func (o *chatToolArgObserver) wrapSpan(span func(context.Context, string, bool) (context.Context, func(int, bool))) func(context.Context, string, bool) (context.Context, func(int, bool)) {
	if span == nil {
		return nil
	}
	return func(ctx context.Context, name string, ext bool) (context.Context, func(int, bool)) {
		ctx, end := span(ctx, name, ext)
		o.mu.Lock()
		valid, ok := o.pending[name]
		delete(o.pending, name)
		o.mu.Unlock()
		if ok {
			trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("coremetry.ai.tool.args_valid", valid))
		}
		return ctx, end
	}
}

// note — SAF görünüm: ai_calls error_msg'ine eklenecek not; sorun yoksa "".
func (o *chatToolArgObserver) note() string {
	if o == nil {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return chatToolArgNote(o.mode, o.invalid)
}

// chatToolArgNote — SAF: "tool_args warn: a(x:type); b(y:required) +2".
func chatToolArgNote(mode string, invalid []string) string {
	if len(invalid) == 0 {
		return ""
	}
	list := append([]string(nil), invalid...)
	sort.Strings(list)
	extra := ""
	if len(list) > chatToolArgWarnNoteMax {
		extra = fmt.Sprintf(" +%d", len(list)-chatToolArgWarnNoteMax)
		list = list[:chatToolArgWarnNoteMax]
	}
	return "tool_args " + mode + ": " + strings.Join(list, "; ") + extra
}

// withToolArgNote — SAF: ai_calls error_msg'ine doğrulama notu (status ok
// satırında degradedReason'ın yanına; hata satırında dokunulmaz).
func withToolArgNote(status, errMsg, note string) string {
	if note == "" || status != "ok" {
		return errMsg
	}
	if errMsg == "" {
		return note
	}
	return errMsg + " · " + note
}

// applyChatToolArgScope — sohbet yüzeyinin SchemaScope'u (kip ayardan,
// şemalar yalnız YERLİ araçlardan — dış MCP aracı doğrulanmaz) + span
// sarmalayıcısı. Kip off ise (nil, nil): hooks değişmez, Executor bugünkü
// Unrestricted kapsamla koşar (bayt bayt eski yol).
func applyChatToolArgScope(hooks *agenttools.Hooks, schemas map[string]map[string]any) (*chatToolArgObserver, *agenttools.SchemaScope) {
	mode := chstore.CurrentAIToolArgValidation().Chat
	if mode != agenttools.ArgModeWarn && mode != agenttools.ArgModeEnforce {
		return nil, nil
	}
	obs := &chatToolArgObserver{mode: mode}
	sc := agenttools.NewSchemaScope(schemas, mode, nil)
	sc.Observe = obs.observe
	hooks.Span = obs.wrapSpan(hooks.Span)
	return obs, sc
}
