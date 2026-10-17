package chstore

// v0.10.1289 — CoSRE kuralları blobu: boş → varsayılan, bozuk → reddedilir
// (yükleme son değeri korur, yazım üstüne boş belge yazmaz), geçersiz satır düşer.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/cosrerules"
)

func TestParseCosreRules(t *testing.T) {
	if d, ok := parseCosreRules(nil); !ok || d.Rules == nil || len(d.Rules) != 0 || d.Rev != 0 {
		t.Fatalf("boş blob: %+v %v", d, ok)
	}
	if _, ok := parseCosreRules([]byte(`{"rules":[`)); ok {
		t.Fatal("bozuk blob ok=false olmalı")
	}
	d, ok := parseCosreRules([]byte(`{"rev":4,"rules":[{"id":"a","text":"geçerli bir kural metni","scope":{"kind":"service","value":"checkout"}},{"id":"b","text":"x"}]}`))
	if !ok || d.Rev != 4 || len(d.Rules) != 1 || d.Rules[0].Scope.Kind != cosrerules.ScopeService {
		t.Fatalf("normalize: %+v", d)
	}
	SetCosreRules(d)
	if cur := CurrentCosreRules(); cur.Rev != 4 || len(cur.Rules) != 1 {
		t.Fatalf("Current: %+v", cur)
	}
	SetCosreRules(DefaultCosreRules())
}
