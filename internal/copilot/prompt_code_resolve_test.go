package copilot

import (
	"strings"
	"testing"
)

// v0.10.1210 — iş kodu / kod arama eki: model-bağımsız kurallar araç
// adlarını birebir anar; kod arama satırları yalnız istenince.
func TestCodeResolveChatAddendum(t *testing.T) {
	base := CodeResolveChatAddendum(false)
	for _, want := range []string{"resolve_business_code", "ambiguous=true", "TAHMİN ETME", "UYDURMA", "error_rate_pct"} {
		if !strings.Contains(base, want) {
			t.Errorf("ek %q içermiyor", want)
		}
	}
	if strings.Contains(base, "search_code") {
		t.Error("search_code sunulmuyorsa kod arama satırları yok")
	}
	full := CodeResolveChatAddendum(true)
	for _, want := range []string{"search_code(service, query)", "read_source_code(service, file, line)", "AYNEN"} {
		if !strings.Contains(full, want) {
			t.Errorf("kod arama eki %q içermiyor", want)
		}
	}
	for _, l := range strings.Split(full, "\n") {
		if len([]rune(l)) > 220 {
			t.Errorf("kural satırı kısa olmalı (küçük model): %d karakter", len([]rune(l)))
		}
	}
}
