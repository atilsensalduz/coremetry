package provider

// strict_tools_test.go — v0.10.1456 (C1, opsiyonel guided decoding): profil
// StrictToolArgs kapalıyken openai-uyumlu araç gövdesi BAYT BAYT eski; açıkken
// tools[].function.strict=true + nesne düğümlerine additionalProperties:false
// (açıkça verilmiş additionalProperties korunur, katalog haritası değişmez);
// Anthropic gövdesi anahtarı hiç okumaz.

import (
	"encoding/json"
	"strings"
	"testing"
)

func strictTestReq(strict bool) ChatRequest {
	return ChatRequest{
		Model: "qwen3-32b", MaxTokens: 512, System: "sys", StrictTools: strict,
		Messages: []ChatMessage{{Role: "user", Text: "soru"}},
		Tools: []ToolSpec{{Name: "query_metric", Description: "d", InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service": map[string]any{"type": "string"},
				"labels":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"filter":  map[string]any{"type": "object", "properties": map[string]any{"k": map[string]any{"type": "string"}}},
			},
			"required": []string{"service"},
		}}},
	}
}

func TestOpenAIToolsBodyStrictOffUnchanged(t *testing.T) {
	off, _ := json.Marshal(buildOpenAIToolsBody(Config{}, strictTestReq(false), "m", false))
	// Referans: anahtarın hiç olmadığı (v0.10.1455) şekil — function {name, description, parameters}.
	ref := strictTestReq(false)
	want := map[string]any{"name": ref.Tools[0].Name, "description": ref.Tools[0].Description, "parameters": ref.Tools[0].InputSchema}
	wb, _ := json.Marshal(want)
	if !strings.Contains(string(off), `"function":`+string(wb)) {
		t.Fatalf("kapalı gövde değişti:\n%s\nbeklenen function %s", off, wb)
	}
	if strings.Contains(string(off), `"strict"`) || strings.Contains(string(off), `"additionalProperties":false`) {
		t.Fatalf("kapalıyken strict izi: %s", off)
	}
}

func TestOpenAIToolsBodyStrictOn(t *testing.T) {
	req := strictTestReq(true)
	before, _ := json.Marshal(req.Tools[0].InputSchema)
	body := buildOpenAIToolsBody(Config{}, req, "m", false)
	after, _ := json.Marshal(req.Tools[0].InputSchema)
	if string(before) != string(after) {
		t.Fatal("katalog şeması yerinde değiştirildi")
	}
	fn := body["tools"].([]map[string]any)[0]["function"].(map[string]any)
	if fn["strict"] != true {
		t.Fatalf("strict yok: %v", fn)
	}
	p := fn["parameters"].(map[string]any)
	if p["additionalProperties"] != false {
		t.Fatalf("kök additionalProperties = %v", p["additionalProperties"])
	}
	props := p["properties"].(map[string]any)
	if _, ok := props["additionalProperties"]; ok {
		t.Fatal("properties haritasının kendisi ek anahtar aldı")
	}
	if _, ok := props["labels"].(map[string]any)["additionalProperties"].(map[string]any); !ok {
		t.Fatal("açık additionalProperties şeması ezildi")
	}
	if props["filter"].(map[string]any)["additionalProperties"] != false {
		t.Fatal("iç nesne additionalProperties:false almadı")
	}
	if _, ok := props["service"].(map[string]any)["additionalProperties"]; ok {
		t.Fatal("yaprak düğüm additionalProperties aldı")
	}
}

func TestAnthropicToolsBodyIgnoresStrict(t *testing.T) {
	a, _ := json.Marshal(buildAnthropicToolsBody(Config{}, strictTestReq(false)))
	b, _ := json.Marshal(buildAnthropicToolsBody(Config{}, strictTestReq(true)))
	if string(a) != string(b) {
		t.Fatalf("Anthropic gövdesi StrictTools okumamalı:\n%s\n%s", a, b)
	}
}
