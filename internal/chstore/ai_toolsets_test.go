package chstore

// ai_toolsets_test.go — v0.10.1434 (CoSRE araştırma PR 1): system_settings
// `ai.toolsets` blobunun saf kuralları — biçimsel normalize, round-trip, bozuk
// blobun son değeri koruması, boş kümenin nil olması (sıcak yolda süzgeç yok).

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestNormalizeAIToolsets(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{nil, []string{}},
		{[]string{" Logs ", "logs", "", "k8s"}, []string{"k8s", "logs"}},
		{[]string{"devops-code", "devops-wiki", "devops-code"}, []string{"devops-code", "devops-wiki"}},
		{[]string{"x123456789012345678901234567890123456789012345678901"}, []string{}}, // > 48 karakter düşer
	}
	for _, c := range cases {
		got := NormalizeAIToolsets(AIToolsets{Disabled: c.in}).Disabled
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Normalize(%q) = %q, beklenen %q", c.in, got, c.want)
		}
	}
	many := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		many = append(many, string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if n := len(NormalizeAIToolsets(AIToolsets{Disabled: many}).Disabled); n != aiToolsetsMaxNames {
		t.Errorf("tavan %d, beklenen %d", n, aiToolsetsMaxNames)
	}
}

func TestAIToolsetsRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	in := AIToolsets{Disabled: []string{"logs", "devops-code"}, UpdatedBy: "admin@example.test", UpdatedAt: at}
	raw, err := json.Marshal(NormalizeAIToolsets(in))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := parseAIToolsets(raw)
	if !ok || !reflect.DeepEqual(got.Disabled, []string{"devops-code", "logs"}) || got.UpdatedBy != in.UpdatedBy || !got.UpdatedAt.Equal(at) {
		t.Fatalf("round-trip: %+v ok=%v", got, ok)
	}
	if set := got.DisabledSet(); !set["logs"] || !set["devops-code"] || len(set) != 2 {
		t.Fatalf("DisabledSet: %v", set)
	}
	// Boş blob → varsayılan (hepsi açık), boş küme nil.
	def, ok := parseAIToolsets(nil)
	if !ok || def.Disabled == nil || len(def.Disabled) != 0 || def.DisabledSet() != nil {
		t.Fatalf("boş blob varsayılanı: %+v ok=%v", def, ok)
	}
	// JSON'da disabled hiçbir zaman null değil; updatedAt sıfırsa yazılmaz.
	b, _ := json.Marshal(def)
	if string(b) != `{"disabled":[]}` {
		t.Fatalf("varsayılan JSON: %s", b)
	}
	// Bozuk blob → ok=false (Load son değeri korur, Get hata döner).
	if _, ok := parseAIToolsets([]byte("{not json")); ok {
		t.Fatal("bozuk blob ok=true döndü")
	}
}

func TestCurrentAIToolsetsPublish(t *testing.T) {
	prev := CurrentAIToolsets()
	t.Cleanup(func() { SetAIToolsets(prev) })
	SetAIToolsets(AIToolsets{Disabled: []string{"LOGS", "logs"}})
	if got := CurrentAIToolsets().Disabled; !reflect.DeepEqual(got, []string{"logs"}) {
		t.Fatalf("yayınlanan normalize edilmeli: %q", got)
	}
	SetAIToolsets(AIToolsets{})
	if CurrentAIToolsets().DisabledSet() != nil {
		t.Fatal("boş ayar nil küme vermeli")
	}
}
