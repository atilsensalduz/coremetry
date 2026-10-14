package api

// ai_conversation_link.go — v0.10.1168 — CoSRE konuşması bir probleme ya da
// servise bağlanır; problem detay sayfası o probleme bağlı sohbetleri listeler.
//
// Şema YOK: bağ mevcut ai_conversations blob'unda (aiChatBlob.Link). Elle
// bağlanmadıysa konuşmanın bağlam görüntüsünden (conv.context) türer:
// problemId → problem, yoksa service → servis. Elle bağ (Manual) bağlamı ezer
// ve her upsert'te taşınır; boş istekle kaldırılınca türetmeye döner.
//   - PUT /api/ai/conversations/{id}/link {kind,id} — yalnız sahibi.
//   - GET /api/problems/{id}/conversations — çağıranın KENDİ konuşmalarından
//     o probleme bağlı olanlar (konuşmalar kişisel; başkasınınki paylaşım
//     bağlantısıyla açılır).
// Denetim kaydı yok: kişisel durum (upsertAIConversation ile aynı gerekçe).

import (
	"encoding/json"
	"net/http"
	"strings"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("ai-conversation-link", (*Server).registerAIConversationLinkRoutes) }

func (s *Server) registerAIConversationLinkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/ai/conversations/{id}/link", auth.RequireAnyRole(aiChatShareRoles, s.linkAIConversation))
	mux.HandleFunc("GET /api/problems/{id}/conversations", auth.RequireAnyRole(aiChatShareRoles, s.problemConversations))
}

const (
	aiChatLinkProblem = "problem"
	aiChatLinkService = "service"
	aiChatLinkMaxID   = 200
)

// aiChatLink — konuşmanın bağı. Manual = elle bağlandı (bağlam ezemez).
type aiChatLink struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Manual bool   `json:"manual,omitempty"`
}

// normalizeChatLink — SAF: geçerli tür + kırpılmış kimlik; değilse nil.
func normalizeChatLink(kind, id string) *aiChatLink {
	kind, id = strings.TrimSpace(kind), strings.TrimSpace(id)
	if (kind != aiChatLinkProblem && kind != aiChatLinkService) || id == "" {
		return nil
	}
	return &aiChatLink{Kind: kind, ID: clampChatRunes(id, aiChatLinkMaxID)}
}

// resolveChatLink — SAF: elle bağ varsa o; yoksa bağlamdan (problem önce).
func resolveChatLink(carried *aiChatLink, ctx *agentctx.PageContext) *aiChatLink {
	if carried != nil && carried.Manual {
		return carried
	}
	if ctx == nil {
		return nil
	}
	if l := normalizeChatLink(aiChatLinkProblem, ctx.ProblemID); l != nil {
		return l
	}
	return normalizeChatLink(aiChatLinkService, ctx.Service)
}

// carriedChatLink — SAF: mevcut blob'daki bağ (yoksa / okunamazsa nil).
func carriedChatLink(existingBlob string) *aiChatLink {
	if existingBlob == "" {
		return nil
	}
	var b struct {
		Link *aiChatLink `json:"link"`
	}
	if json.Unmarshal([]byte(existingBlob), &b) != nil || b.Link == nil {
		return nil
	}
	if l := normalizeChatLink(b.Link.Kind, b.Link.ID); l != nil {
		l.Manual = b.Link.Manual
		return l
	}
	return nil
}

func (s *Server) linkAIConversation(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := s.aiChatOwner(w, r)
	if !ok {
		return
	}
	var body struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid body")
		return
	}
	link := normalizeChatLink(body.Kind, body.ID)
	if link == nil && (strings.TrimSpace(body.Kind) != "" || strings.TrimSpace(body.ID) != "") {
		writeJSONError(w, http.StatusBadRequest, "kind problem|service ve id gerekli (boş = elle bağı kaldır)")
		return
	}
	cur, err := s.aiChatRow(r, strings.TrimSpace(r.PathValue("id")), ownerID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if cur == nil {
		writeJSONError(w, http.StatusNotFound, "conversation not found")
		return
	}
	var blob aiChatBlob
	if err := json.Unmarshal([]byte(cur.QueryString), &blob); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "stored conversation is unreadable: "+err.Error())
		return
	}
	if link != nil {
		link.Manual = true
	}
	blob.Link = resolveChatLink(link, blob.Context)
	raw, err := json.Marshal(blob)
	if err != nil {
		writeErr(w, err)
		return
	}
	next := *cur
	next.QueryString = string(raw)
	if err := s.store.UpsertSavedView(r.Context(), next); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"id": cur.ID, "link": blob.Link})
}

// conversationsLinkedTo — SAF: özetlerden o hedefe bağlı olanlar (sıra korunur).
func conversationsLinkedTo(in []aiConversationSummary, kind, id string) []aiConversationSummary {
	out := []aiConversationSummary{}
	for _, c := range in {
		if c.Link != nil && c.Link.Kind == kind && c.Link.ID == id {
			out = append(out, c)
		}
	}
	return out
}

func (s *Server) problemConversations(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := s.aiChatOwner(w, r)
	if !ok {
		return
	}
	pid := strings.TrimSpace(r.PathValue("id"))
	metas, err := s.store.ListSavedViewMeta(r.Context(), ownerID, aiChatPage, aiChatListLimit)
	if err != nil {
		writeErr(w, err)
		return
	}
	all := make([]aiConversationSummary, 0, len(metas))
	for _, m := range metas {
		all = append(all, metaToSummary(m))
	}
	writeJSON(w, conversationsLinkedTo(all, aiChatLinkProblem, pid))
}
