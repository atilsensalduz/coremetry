package api

// ai_conversation_link_test.go — v0.10.1168: konuşma bağı saf parçaları.

import (
	"testing"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
)

func TestResolveChatLink(t *testing.T) {
	ctx := &agentctx.PageContext{ProblemID: "p-1", Service: "orders"}
	if l := resolveChatLink(nil, ctx); l == nil || l.Kind != "problem" || l.ID != "p-1" {
		t.Fatalf("bağlamda problem önce: %+v", l)
	}
	if l := resolveChatLink(nil, &agentctx.PageContext{Service: "orders"}); l == nil || l.Kind != "service" || l.ID != "orders" {
		t.Fatalf("problem yoksa servis: %+v", l)
	}
	if resolveChatLink(nil, nil) != nil || resolveChatLink(nil, &agentctx.PageContext{}) != nil {
		t.Fatal("bağlamsız bağ olmaz")
	}
	manual := &aiChatLink{Kind: "service", ID: "billing", Manual: true}
	if l := resolveChatLink(manual, ctx); l != manual {
		t.Fatal("elle bağ bağlamı ezer")
	}
	if normalizeChatLink("trace", "x") != nil || normalizeChatLink("problem", " ") != nil {
		t.Fatal("geçersiz tür / boş kimlik reddedilmeli")
	}
	if l := carriedChatLink(`{"link":{"kind":"problem","id":"p-9","manual":true}}`); l == nil || !l.Manual || l.ID != "p-9" {
		t.Fatalf("taşınan bağ: %+v", l)
	}
	if carriedChatLink(`{"link":{"kind":"x","id":"y"}}`) != nil || carriedChatLink("{") != nil {
		t.Fatal("bozuk bağ taşınmamalı")
	}
}

func TestConversationsLinkedTo(t *testing.T) {
	in := []aiConversationSummary{
		{ID: "a", Link: &aiChatLink{Kind: "problem", ID: "p-1"}},
		{ID: "b", Link: &aiChatLink{Kind: "service", ID: "p-1"}},
		{ID: "c"},
		{ID: "d", Link: &aiChatLink{Kind: "problem", ID: "p-1"}},
	}
	got := conversationsLinkedTo(in, "problem", "p-1")
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "d" {
		t.Fatalf("got %+v", got)
	}
	if out := conversationsLinkedTo(nil, "problem", "p-1"); out == nil || len(out) != 0 {
		t.Fatal("boş liste nil değil [] olmalı (JSON)")
	}
}
