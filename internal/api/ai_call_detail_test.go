package api

import (
	"encoding/json"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1470 — /ai çekmecesinin okuduğu GET /api/ai/calls/{id} yanıtı: satır
// alanları düz kalır (gömülü işaretçi; eski istemci aynı şekli görür) ve
// traceQueryable süreç bayrağını taşır.
func TestAICallDetailShape(t *testing.T) {
	c := &chstore.AICall{ID: "c1", Surface: "sre-narrate", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736"}
	for _, q := range []bool{true, false} {
		raw, err := json.Marshal(aiCallDetailOf(c, q))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if m["id"] != "c1" || m["surface"] != "sre-narrate" || m["traceId"] != c.TraceID {
			t.Fatalf("satır alanları düz değil: %s", raw)
		}
		if v, ok := m["traceQueryable"].(bool); !ok || v != q {
			t.Fatalf("traceQueryable=%v bekleniyordu: %s", q, raw)
		}
		plain, _ := json.Marshal(c)
		var pm map[string]any
		_ = json.Unmarshal(plain, &pm)
		if len(m) != len(pm)+1 {
			t.Fatalf("tek ek alan bekleniyordu: %d vs %d", len(m), len(pm))
		}
	}
}
