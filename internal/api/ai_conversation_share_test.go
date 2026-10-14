package api

// ai_conversation_share_test.go — v0.10.1167: paylaşım sırrı saf parçaları.

import "testing"

func TestAIChatShareHelpers(t *testing.T) {
	if shareTokenMatches("", "") || shareTokenMatches("abc", "") || shareTokenMatches("", "abc") || shareTokenMatches("abc", "abd") {
		t.Fatal("boş / yanlış sır eşleşmemeli")
	}
	if !shareTokenMatches("abc", "abc") {
		t.Fatal("doğru sır eşleşmeli")
	}
	// Upsert tüm satırı değiştirir: paylaşım sırrı taşınmalı.
	if got := carriedShareToken(`{"messages":[],"shareToken":"s3"}`); got != "s3" {
		t.Fatalf("taşınan sır = %q", got)
	}
	for _, in := range []string{"", "{", `{"messages":[]}`} {
		if carriedShareToken(in) != "" {
			t.Fatalf("%q için sır olmamalı", in)
		}
	}
	if p := sharedConversationPath("c1", "t9"); p != "/cosre/shared/c1?t=t9" {
		t.Fatal(p)
	}
}
