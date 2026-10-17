package mcp

import (
	"context"
	"testing"
)

// v0.10.1302 — ⟦full|simple⟧ işaretleri: iki varyant, işaretsiz metin aynen,
// bozuk işaret olduğu gibi kalır (mcptools kapısı yakalar), boş kompakt
// metin tam açıklamaya düşer.
func TestRenderToolsetMarkers(t *testing.T) {
	cases := []struct{ in, full, simple string }{
		{"düz metin any|error", "düz metin any|error", "düz metin any|error"},
		{"Log oku, any|warn|error.⟦ Lucene: search_logs.|⟧", "Log oku, any|warn|error. Lucene: search_logs.", "Log oku, any|warn|error."},
		{"⟦search_logs|query_logs⟧ SATIR verir", "search_logs SATIR verir", "query_logs SATIR verir"},
		{"a ⟦x|y⟧ b ⟦p|q⟧ c", "a x b p c", "a y b q c"},
		{"bozuk ⟦x⟧ işaret", "bozuk ⟦x⟧ işaret", "bozuk ⟦x⟧ işaret"},
		{"iki ayraç ⟦x|y|z⟧", "iki ayraç ⟦x|y|z⟧", "iki ayraç ⟦x|y|z⟧"},
		{"kapanmamış ⟦x|y", "kapanmamış ⟦x|y", "kapanmamış ⟦x|y"},
	}
	for _, c := range cases {
		if got := RenderToolsetMarkers(c.in, false); got != c.full {
			t.Errorf("full(%q) = %q, want %q", c.in, got, c.full)
		}
		if got := RenderToolsetMarkers(c.in, true); got != c.simple {
			t.Errorf("simple(%q) = %q, want %q", c.in, got, c.simple)
		}
	}
	tl := Tool{Name: "x", Description: "tam", ShortDescription: "kısa⟦ A|⟧"}
	if tl.ChatDescription() != "kısa A" || tl.ChatDescriptionFor(false) != "kısa A" || tl.ChatDescriptionFor(true) != "kısa" {
		t.Fatalf("ChatDescription/For: %q %q", tl.ChatDescription(), tl.ChatDescriptionFor(true))
	}
	if (Tool{Description: "tam ⟦a|b⟧"}).ChatDescriptionFor(true) != "tam ⟦a|b⟧" {
		t.Fatal("boş kompakt metin tam açıklamaya AYNEN düşmeli")
	}
}

// v0.10.1302 — ctx araç seti: işaretsiz ctx full; şema işaretleri kopya
// üzerinde çözülür, kayıt defterinin şeması değişmez.
func TestToolsetContextAndSchemaRender(t *testing.T) {
	bg := context.Background()
	if SimpleToolsetFrom(bg) || !SimpleToolsetFrom(WithSimpleToolset(bg, true)) || SimpleToolsetFrom(WithSimpleToolset(bg, false)) {
		t.Fatal("ctx bayrağı")
	}
	if ToolsetText(bg, "a ⟦x|y⟧") != "a x" || ToolsetText(WithSimpleToolset(bg, true), "a ⟦x|y⟧") != "a y" {
		t.Fatal("ToolsetText")
	}
	plain := map[string]any{"type": "string", "description": "düz"}
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"f": map[string]any{"type": "array", "description": "filters ⟦as in search_traces|from describe_attributes⟧ ({key, op, value|values})"},
		"p": plain,
	}, "required": []any{"f"}}
	got := RenderSchemaToolsetMarkers(schema, true)
	if d := got["properties"].(map[string]any)["f"].(map[string]any)["description"]; d != "filters from describe_attributes ({key, op, value|values})" {
		t.Fatalf("simple şema: %v", d)
	}
	if d := RenderSchemaToolsetMarkers(schema, false)["properties"].(map[string]any)["f"].(map[string]any)["description"]; d != "filters as in search_traces ({key, op, value|values})" {
		t.Fatalf("full şema: %v", d)
	}
	if d := schema["properties"].(map[string]any)["f"].(map[string]any)["description"].(string); d[len(d)-1] != ')' || !contains(d, "⟦") {
		t.Fatal("kaynak şema yerinde değişti")
	}
	if got["properties"].(map[string]any)["p"].(map[string]any)["description"] != "düz" {
		t.Fatal("işaretsiz alt ağaç korunmalı")
	}
	if RenderSchemaToolsetMarkers(nil, true) != nil {
		t.Fatal("nil şema nil kalmalı")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
