package chstore

// ai_toolarg_settings_test.go — v0.10.1456 (C1): system_settings
// `ai.toolArgValidation` blobunun saf kuralları — varsayılan (chat warn,
// sreLoop enforce, mcp off), alan başına normalize, bozuk blobun son değeri
// koruması.

import (
	"testing"
	"time"
)

func TestAIToolArgValidationDefaults(t *testing.T) {
	d := DefaultAIToolArgValidation()
	if d.Chat != "warn" || d.SreLoop != "enforce" || d.MCP != "off" {
		t.Fatalf("default = %+v (operatör kararı 3: chat warn; karar 6: sreLoop enforce)", d)
	}
	aiToolArgCur.Store(nil)
	if CurrentAIToolArgValidation() != d {
		t.Fatal("yüklenmemişken varsayılan dönmeli")
	}
	if a, ok := parseAIToolArgValidation(nil); !ok || a != d {
		t.Fatalf("boş blob = %+v %v", a, ok)
	}
}

func TestNormalizeAIToolArgValidation(t *testing.T) {
	cases := []struct {
		in   AIToolArgValidation
		want AIToolArgValidation
	}{
		{AIToolArgValidation{}, DefaultAIToolArgValidation()},
		{AIToolArgValidation{Chat: " ENFORCE ", SreLoop: "off", MCP: "enforce"}, AIToolArgValidation{Chat: "enforce", SreLoop: "off", MCP: "off"}},
		{AIToolArgValidation{Chat: "bogus", SreLoop: "warn"}, AIToolArgValidation{Chat: "warn", SreLoop: "warn", MCP: "off"}},
	}
	for _, c := range cases {
		if got := NormalizeAIToolArgValidation(c.in); got != c.want {
			t.Errorf("Normalize(%+v) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestAIToolArgValidationParse(t *testing.T) {
	at := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	a, ok := parseAIToolArgValidation([]byte(`{"chat":"enforce","sreLoop":"enforce","mcp":"off","updatedBy":"admin@example.test","updatedAt":"2026-10-23T09:00:00Z"}`))
	if !ok || a.Chat != "enforce" || a.UpdatedBy != "admin@example.test" || !a.UpdatedAt.Equal(at) {
		t.Fatalf("parse = %+v %v", a, ok)
	}
	if _, ok := parseAIToolArgValidation([]byte(`{not json`)); ok {
		t.Fatal("bozuk blob ok=false olmalı")
	}
	SetAIToolArgValidation(AIToolArgValidation{Chat: "off"})
	t.Cleanup(func() { aiToolArgCur.Store(nil) })
	if c := CurrentAIToolArgValidation(); c.Chat != "off" || c.SreLoop != "enforce" {
		t.Fatalf("set → %+v", c)
	}
}
