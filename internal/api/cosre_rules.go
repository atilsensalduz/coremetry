package api

// cosre_rules.go — v0.10.1289 (CoSRE kurallar / hafıza: "bunu hatırla").
//
//   GET    /api/ai/rules        → {rules, rev, limits}   her rol (viewer GÖRÜR)
//   POST   /api/ai/rules        {text, scope}            editor+ · audit cosre_rule.create
//   PUT    /api/ai/rules/{id}   {text, scope}            editor+ · audit cosre_rule.update
//   DELETE /api/ai/rules/{id}                            editor+ · audit cosre_rule.delete
//
// Depo system_settings.cosre_rules ({rules, rev}; chstore/cosre_rules.go,
// saf kurallar internal/ai/cosrerules). Yazım oku-değiştir-yaz: tek blob,
// iki editörün eşzamanlı yazımı birbirini SESSİZCE ezmesin diye (1) 5 s'lik
// Redis kilidi coremetry:lock:cosre-rules altında taze okuma, (2) If-Match:
// <rev> başlığı varsa ve saklanan rev farklıysa 409 (istemci listeyi yeniler).
// If-Match yoksa (sohbetin "Kaydet" kartı — salt ekleme) kilit + tavanlar yeter.
//
// Neden /api/copilot/ altında DEĞİL ve requireCopilot'suz: kurallar AI
// yapılandırılmamışken de yönetilebilir (Ayarlar › CoSRE › Kurallar) ve
// okunabilir; registerRoutesExtra ile kendi dosyasında (api.go büyümez).
// GET serveCached DEĞİL: küçük tek satırlık ayar okuması ve yazımdan hemen
// sonra aynı listeyi göstermeli (30 s'lik önbellek "kaydettim ama yok"
// üretirdi); sıcak yol değil (ayar paneli + modal).
//
// Operatör direktifi ("oylara çok güvenmeyelim"): kural İNSAN yazımıdır,
// yazanı ve tarihi görünür; oy / kalite puanı burada hiç okunmaz.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/cosrerules"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("cosre-rules", (*Server).registerCosreRulesRoutes) }

func (s *Server) registerCosreRulesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/rules", s.getCosreRules)
	mux.Handle("POST /api/ai/rules", auth.RequireAnyRole(editorRoles, http.HandlerFunc(s.createCosreRule)))
	mux.Handle("PUT /api/ai/rules/{id}", auth.RequireAnyRole(editorRoles, http.HandlerFunc(s.updateCosreRule)))
	mux.Handle("DELETE /api/ai/rules/{id}", auth.RequireAnyRole(editorRoles, http.HandlerFunc(s.deleteCosreRule)))
}

const (
	cosreRulesLockKey  = "coremetry:lock:cosre-rules"
	cosreRulesLockTTL  = 5 * time.Second
	cosreRulesLockWait = 2 * time.Second
)

// cosreRulesStore — depo dikişi (test sahte depo verir).
type cosreRulesStore interface {
	GetCosreRules(ctx context.Context) (cosrerules.Doc, error)
	SaveCosreRules(ctx context.Context, d cosrerules.Doc) error
}

var cosreRulesStoreOf = func(s *Server) cosreRulesStore {
	if s.store == nil {
		return nil
	}
	return s.store
}

// cosreRulesMu — aynı pod içindeki yazımları sıralar (Redis kilidi pod'lar arası).
var cosreRulesMu sync.Mutex

type cosreRulesLimits struct {
	MaxRules    int `json:"maxRules"`
	MaxPerScope int `json:"maxPerScope"`
	MinText     int `json:"minText"`
	MaxText     int `json:"maxText"`
}

type cosreRulesResponse struct {
	Rules  []cosrerules.Rule `json:"rules"`
	Rev    uint64            `json:"rev"`
	Limits cosreRulesLimits  `json:"limits"`
}

func cosreRulesBody(d cosrerules.Doc) cosreRulesResponse {
	rules := d.Rules
	if rules == nil {
		rules = []cosrerules.Rule{}
	}
	return cosreRulesResponse{Rules: rules, Rev: d.Rev, Limits: cosreRulesLimits{
		MaxRules: cosrerules.MaxRules, MaxPerScope: cosrerules.MaxPerScope,
		MinText: cosrerules.MinTextRunes, MaxText: cosrerules.MaxTextRunes,
	}}
}

// parseIfMatchRev — SAF: `If-Match: 7` ya da `"7"` / `W/"7"`. Boş → yok.
func parseIfMatchRev(h string) (uint64, bool, error) {
	h = strings.TrimSpace(h)
	if h == "" || h == "*" {
		return 0, false, nil
	}
	h = strings.TrimPrefix(h, "W/")
	h = strings.Trim(h, `"`)
	n, err := strconv.ParseUint(h, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("If-Match bir rev sayısı olmalı")
	}
	return n, true, nil
}

func (s *Server) getCosreRules(w http.ResponseWriter, r *http.Request) {
	st := cosreRulesStoreOf(s)
	d := chstore.CurrentCosreRules()
	if st != nil {
		fresh, err := st.GetCosreRules(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		d = fresh
	}
	w.Header().Set("ETag", fmt.Sprintf("%q", strconv.FormatUint(d.Rev, 10)))
	writeJSON(w, cosreRulesBody(d))
}

type cosreRuleInput struct {
	Text  string           `json:"text"`
	Scope cosrerules.Scope `json:"scope"`
}

// cosreRuleActor — SAF olmayan: yazanın görünen adı (Ad Soyad, yoksa e-posta).
func (s *Server) cosreRuleActor(ctx context.Context, c *auth.Claims) (name, email string) {
	if c == nil {
		return "", ""
	}
	if u := s.userByID(ctx, c.UserID); u != nil && strings.TrimSpace(u.FullName) != "" {
		return strings.TrimSpace(u.FullName), c.Email
	}
	return c.Email, c.Email
}

// userByID — /api/auth/me'nin 30 s önbelleğinden (yoksa tek okuma); nil-güvenli.
func (s *Server) userByID(ctx context.Context, id string) *chstore.User {
	if id == "" {
		return nil
	}
	if s.meUsers != nil {
		if u, ok := s.meUsers.get(id, time.Now()); ok {
			return u
		}
	}
	if s.store == nil {
		return nil
	}
	u, err := s.store.GetUserByID(ctx, id)
	if err != nil || u == nil {
		return nil
	}
	if s.meUsers != nil {
		s.meUsers.put(id, u, time.Now())
	}
	return u
}

// cosreRulesStatus — SAF: saf çekirdeğin hatası → HTTP durumu.
func cosreRulesStatus(err error) int {
	switch {
	case errors.Is(err, cosrerules.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, cosrerules.ErrTextLength), errors.Is(err, cosrerules.ErrScope),
		errors.Is(err, cosrerules.ErrCapTotal), errors.Is(err, cosrerules.ErrCapScope),
		errors.Is(err, cosrerules.ErrDuplicate):
		return http.StatusBadRequest
	}
	return 0
}

// lockCosreRules — pod içi mutex + pod'lar arası Redis kilidi (SETNX, 5 s TTL,
// en çok 2 s bekleme). Dönen release kilidi YALNIZ hâlâ bizdeyse siler.
func (s *Server) lockCosreRules(ctx context.Context) (func(), bool) {
	cosreRulesMu.Lock()
	if s.cache == nil {
		return cosreRulesMu.Unlock, true
	}
	token := []byte(newRandID(8))
	deadline := time.Now().Add(cosreRulesLockWait)
	for {
		ok, err := s.cache.SetNX(ctx, cosreRulesLockKey, token, cosreRulesLockTTL)
		if err == nil && ok {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			cosreRulesMu.Unlock()
			return nil, false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return func() {
		rctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if cur, ok, _ := s.cache.Get(rctx, cosreRulesLockKey); ok && string(cur) == string(token) {
			_ = s.cache.Del(rctx, cosreRulesLockKey)
		}
		cosreRulesMu.Unlock()
	}, true
}

// mutateCosreRules — kilit + taze okuma + If-Match + saf değişiklik + yazım +
// süreç-içi güncelleme + audit. Hata cevabını kendisi yazar (ok=false).
func (s *Server) mutateCosreRules(w http.ResponseWriter, r *http.Request, action string,
	fn func(cosrerules.Doc) (cosrerules.Doc, cosrerules.Rule, error)) {
	want, hasIfMatch, err := parseIfMatchRev(r.Header.Get("If-Match"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	st := cosreRulesStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "kural deposu yok")
		return
	}
	release, ok := s.lockCosreRules(r.Context())
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, "kurallar şu an başka bir yazımda — birazdan tekrar dene")
		return
	}
	defer release()
	cur, err := st.GetCosreRules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if hasIfMatch && want != cur.Rev {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "kurallar sen düzenlerken değişti — liste yenilendi, değişikliğini yeniden uygula",
			"rev":   cur.Rev, "rules": cosreRulesBody(cur).Rules,
		})
		return
	}
	next, rule, err := fn(cur)
	if err != nil {
		if code := cosreRulesStatus(err); code != 0 {
			writeJSONError(w, code, err.Error())
			return
		}
		writeErr(w, err)
		return
	}
	if err := st.SaveCosreRules(r.Context(), next); err != nil {
		writeErr(w, err)
		return
	}
	chstore.SetCosreRules(next)
	details, _ := json.Marshal(map[string]any{"id": rule.ID, "scope": rule.Scope, "text": rule.Text})
	s.audit(r, action, "cosre_rule", rule.ID, string(details))
	w.Header().Set("ETag", fmt.Sprintf("%q", strconv.FormatUint(next.Rev, 10)))
	writeJSON(w, map[string]any{"rule": rule, "rev": next.Rev, "rules": cosreRulesBody(next).Rules})
}

func decodeCosreRuleInput(w http.ResponseWriter, r *http.Request) (cosreRuleInput, bool) {
	var in cosreRuleInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return in, false
	}
	return in, true
}

func (s *Server) createCosreRule(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCosreRuleInput(w, r)
	if !ok {
		return
	}
	name, email := s.cosreRuleActor(r.Context(), auth.FromContext(r.Context()))
	now := time.Now().UnixNano()
	s.mutateCosreRules(w, r, "cosre_rule.create", func(d cosrerules.Doc) (cosrerules.Doc, cosrerules.Rule, error) {
		return cosrerules.Add(d, cosrerules.Rule{ID: newRandID(8), Text: in.Text, Scope: in.Scope,
			Author: name, AuthorEmail: email, CreatedAt: now})
	})
}

func (s *Server) updateCosreRule(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCosreRuleInput(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	name, _ := s.cosreRuleActor(r.Context(), auth.FromContext(r.Context()))
	now := time.Now().UnixNano()
	s.mutateCosreRules(w, r, "cosre_rule.update", func(d cosrerules.Doc) (cosrerules.Doc, cosrerules.Rule, error) {
		return cosrerules.Update(d, id, in.Text, in.Scope, name, now)
	})
}

func (s *Server) deleteCosreRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mutateCosreRules(w, r, "cosre_rule.delete", func(d cosrerules.Doc) (cosrerules.Doc, cosrerules.Rule, error) {
		return cosrerules.Remove(d, id)
	})
}
