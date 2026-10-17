package modelcaps

import "testing"

// v0.10.1302 — CoSRE: modele göre araç seti. Otomatik kural adda okunabilir
// boyuta bakar: ≤12B simple, aksi hâlde (ve bilinmeyende) full.
func TestDefaultToolsetByModelName(t *testing.T) {
	cases := []struct {
		model  string
		params float64
		want   string
	}{
		{"qwen3.5-2b", 2, ToolsetSimple},
		{"qwen3.5-2b-instruct", 2, ToolsetSimple},
		{"gemma-3-4b-it", 4, ToolsetSimple},
		{"Qwen3-8B", 8, ToolsetSimple},
		{"meta-llama/Llama-3.1-8B-Instruct", 8, ToolsetSimple},
		{"deepseek-r1:14b", 14, ToolsetFull},
		{"phi3:3.8b", 3.8, ToolsetSimple},
		{"Qwen2.5-0.5B", 0.5, ToolsetSimple},
		{"gemma-3-12b-it", 12, ToolsetSimple}, // eşik dahil
		{"gemma4-31b", 31, ToolsetFull},
		{"gemma3:27b", 27, ToolsetFull},
		{"qwen3-30b-a3b", 30, ToolsetFull}, // en soldaki = toplam
		{"mixtral-8x7b", 56, ToolsetFull},  // MoE çarpılır
		{"claude-sonnet-5", 0, ToolsetFull},
		{"gpt-5", 0, ToolsetFull},
		{"gpt-4o-mini", 0, ToolsetFull},
		{"gemma4", 0, ToolsetFull},
		{"gemma2b", 0, ToolsetFull},          // sınırsız → bilinmiyor
		{"llama-7bit-quant", 0, ToolsetFull}, // "7bit" boyut değil
		{"", 0, ToolsetFull},
	}
	for _, c := range cases {
		if got := ParamsB(c.model); got != c.params {
			t.Errorf("ParamsB(%q) = %v, want %v", c.model, got, c.params)
		}
		if got := DefaultToolset(c.model); got != c.want {
			t.Errorf("DefaultToolset(%q) = %q, want %q", c.model, got, c.want)
		}
	}
}

func TestResolveAndValidToolset(t *testing.T) {
	for _, s := range []string{"", "simple", "full"} {
		if !ValidToolset(s) {
			t.Errorf("%q geçerli olmalı", s)
		}
	}
	for _, s := range []string{"x", "auto", "Simple"} {
		if ValidToolset(s) {
			t.Errorf("%q geçersiz olmalı", s)
		}
	}
	if ResolveToolset("full", "qwen3.5-2b") != ToolsetFull || ResolveToolset("simple", "claude-sonnet-5") != ToolsetSimple {
		t.Error("açık ayar otomatik kurala baskın olmalı")
	}
	if ResolveToolset("", "qwen3.5-2b") != ToolsetSimple || ResolveToolset("", "gemma4-31b") != ToolsetFull {
		t.Error("boş ayar otomatik kurala düşmeli")
	}
}
