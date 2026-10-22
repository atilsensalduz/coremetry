package api

// ai_toolsets.go — v0.10.1434 (CoSRE araştırma programı PR 1;
// docs/audits/cosre-investigation-model-audit.md §6.1): AI araç setlerinin
// (toolset) operatör yüzeyi.
//
//   GET /api/ai/toolsets  → {toolsets:[{name,source,required,enabled,health,tools}],
//                            disabled, updatedBy, updatedAt, rev}       her rol (viewer GÖRÜR)
//   PUT /api/ai/toolsets  {disabled:[…]}                                 admin · audit settings.ai_toolsets.update
//
// Depo system_settings `ai.toolsets` (chstore/ai_toolsets.go; boot + 30 s
// cfgRefresh her rolde). Toolset kataloğu ve sağlık türetimi
// mcptools/toolset.go + toolset_health.go'da; bu dosya yalnız HTTP yüzeyi.
//
// Neden requireCopilot'suz ve /api/copilot/ altında DEĞİL: cosre_rules.go
// emsali — araç setleri AI sağlayıcısı yapılandırılmamışken de yönetilebilir
// ve görünür (dış MCP istemcileri sağlayıcıdan bağımsız çağırır). Neden
// admin (editor değil): kapatma tüm kullanıcıların sohbetini ve tüm dış MCP
// token'larını etkileyen kurulum ayarı (Ayarlar admin-only).
//
// PUT doğrulaması (§6.1): bilinmeyen ad DÜŞER (UI'nın eski bir sürümü yeni
// kurulumda zarar vermesin), kapatılamaz set (services / traces / problems /
// meta) 400 — sessizce yok saymak operatöre "kapattım" yanılsaması verirdi.
// Eşzamanlı iki admin: tek küçük blob, son yazan kazanır (cosre kurallarının
// Redis kilidi + If-Match'i burada aşırı; liste ≤12 ad ve her yazım audit'te).
//
// GET serveCached 10 s; anahtar etkin kapalı kümenin FNV özeti + rev
// (v0.5.187: kümenin uzunluğu DEĞİL). Yazımdan sonra bu pod'un atomic'i
// anında değişir → anahtar değişir → "kaydettim ama eski" yok. Sağlık
// türetimi sorgusuz; isteğe bağlı logs yoklaması mcpMemo'da 60 s önbellekli.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func init() { registerRoutesExtra("ai-toolsets", (*Server).registerAIToolsetsRoutes) }

func (s *Server) registerAIToolsetsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/toolsets", s.getAIToolsets)
	mux.Handle("PUT /api/ai/toolsets", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putAIToolsets)))
}

// currentToolsetsDisabled — sohbet kataloğunun (mcpDeps.ToolsetsDisabled) ve dış
// MCP çağrı kapısının (mcp_gate.go) TEK kaynağı: iki yol ayrışamaz.
func currentToolsetsDisabled() map[string]bool { return chstore.CurrentAIToolsets().DisabledSet() }

// aiToolsetsStore — depo dikişi (test sahte depo verir).
type aiToolsetsStore interface {
	SaveAIToolsets(ctx context.Context, a chstore.AIToolsets) (chstore.AIToolsets, error)
}

var aiToolsetsStoreOf = func(s *Server) aiToolsetsStore {
	if s.store == nil {
		return nil
	}
	return s.store
}

// aiToolsetsResponse — GET ve PUT cevabı.
type aiToolsetsResponse struct {
	Toolsets  []mcptools.ToolsetState `json:"toolsets"`
	Disabled  []string                `json:"disabled"`
	UpdatedBy string                  `json:"updatedBy,omitempty"`
	UpdatedAt time.Time               `json:"updatedAt,omitzero"`
	Rev       int64                   `json:"rev"`
}

func aiToolsetsRev(a chstore.AIToolsets) int64 {
	if a.UpdatedAt.IsZero() {
		return 0
	}
	return a.UpdatedAt.UnixMilli()
}

// aiToolsetsCacheKey — SAF: GET'in tüm girdileri (etkin kapalı küme özeti +
// rev). Kapalı küme cevabın enabled alanlarını, rev updatedBy/At'i belirler.
func aiToolsetsCacheKey(a chstore.AIToolsets) string {
	off := map[string]bool{}
	for _, n := range mcptools.SortedDisabledNames(a.DisabledSet()) {
		off[n] = true
	}
	return fmt.Sprintf("ai:toolsets:v1:disabled=%s:rev=%d", excludeKeyDigest(off), aiToolsetsRev(a))
}

func (s *Server) aiToolsetsPayload(ctx context.Context, a chstore.AIToolsets) aiToolsetsResponse {
	raw := a.DisabledSet()
	return aiToolsetsResponse{
		Toolsets:  mcptools.ToolsetStates(ctx, s.mcpDeps(), raw, true),
		Disabled:  mcptools.SortedDisabledNames(raw),
		UpdatedBy: a.UpdatedBy,
		UpdatedAt: a.UpdatedAt,
		Rev:       aiToolsetsRev(a),
	}
}

func (s *Server) getAIToolsets(w http.ResponseWriter, r *http.Request) {
	a := chstore.CurrentAIToolsets()
	s.serveCached(w, r, aiToolsetsCacheKey(a), 10*time.Second, func(ctx context.Context) (any, error) {
		return s.aiToolsetsPayload(ctx, a), nil
	})
}

// validateToolsetsPut — SAF: istenen kapalı ad listesi → saklanacak liste.
// Bilinmeyen ad düşer (ignored'a), kapatılamaz set hata. Sonuç sıralı,
// tekrarsız.
func validateToolsetsPut(in []string) (disabled, ignored []string, err error) {
	seen := map[string]bool{}
	var locked []string
	for _, n := range in {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		if _, ok := mcptools.ToolsetOf(n); !ok {
			ignored = append(ignored, n)
			continue
		}
		if mcptools.ToolsetLocked(n) {
			locked = append(locked, n)
			continue
		}
		disabled = append(disabled, n)
	}
	if len(locked) > 0 {
		sort.Strings(locked)
		return nil, nil, fmt.Errorf("kapatılamaz araç seti: %s (zorunlu çekirdek telemetri / meta)", strings.Join(locked, ", "))
	}
	sort.Strings(disabled)
	sort.Strings(ignored)
	if disabled == nil {
		disabled = []string{}
	}
	return disabled, ignored, nil
}

func (s *Server) putAIToolsets(w http.ResponseWriter, r *http.Request) {
	st := aiToolsetsStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu yok")
		return
	}
	var in struct {
		Disabled []string `json:"disabled"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	disabled, ignored, err := validateToolsetsPut(in.Disabled)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	prev := chstore.CurrentAIToolsets()
	by := ""
	if c := auth.FromContext(r.Context()); c != nil {
		by = c.Email
		if by == "" {
			by = c.UserID
		}
	}
	saved, err := st.SaveAIToolsets(r.Context(), chstore.AIToolsets{Disabled: disabled, UpdatedBy: by, UpdatedAt: time.Now().UTC()})
	if err != nil {
		writeErr(w, err)
		return
	}
	// Bu pod hemen; diğer pod'lar cfgRefresh (30 s) ile yakınsar. Depo
	// SaveAIToolsets zaten yayınlar; sahte depo testleri için de yayınla.
	chstore.SetAIToolsets(saved)
	s.audit(r, "settings.ai_toolsets.update", "settings", chstore.AIToolsetsKey, aiToolsetsAuditDetails(prev.Disabled, saved.Disabled, ignored))
	out := s.aiToolsetsPayload(r.Context(), saved)
	writeJSON(w, map[string]any{
		"toolsets": out.Toolsets, "disabled": out.Disabled, "updatedBy": out.UpdatedBy,
		"updatedAt": out.UpdatedAt, "rev": out.Rev, "ignored": nonNil(ignored),
	})
}

// aiToolsetsAuditDetails — SAF: önce → sonra (+ düşen bilinmeyen adlar).
func aiToolsetsAuditDetails(prev, next, ignored []string) string {
	b, _ := json.Marshal(map[string]any{"before": nonNil(prev), "after": nonNil(next), "ignored": nonNil(ignored)})
	return string(b)
}

// toolsetGateError — SAF: dış MCP tools/call kapısının toolset kararı
// (mcp_gate.go mcpGateDecide, rol kontrolünden SONRA, rate bütçesinden ÖNCE).
// Adlandırılmış ret (mcp.Denied → JSON-RPC ErrForbidden; model "bu kurulumda
// kapalı" diyebilsin, boşuna yeniden denemesin); sızdırmaz: kimin / ne zaman
// kapattığı ya da kurulum ayrıntısı metne girmez. Kaynak/istem (resource,
// prompt) yolları zorunlu setlerin verisini okur → kapatılamaz, dokunulmaz.
func toolsetGateError(kind, name string, raw map[string]bool) error {
	if kind != "tool" {
		return nil
	}
	ts, off := mcptools.ToolDisabledBy(name, raw)
	if !off {
		return nil
	}
	return mcp.Denied("tool %q is unavailable: toolset %q is disabled by the operator on this install — do not retry; answer from other tools or tell the user", name, ts)
}
