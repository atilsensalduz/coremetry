package api

// v0.10.1478 — RED kaynağı: denetim satırı yalnız değişince; anomali
// paketinin aile kataloğu api/red_family.go ile aynı (iki kopya ayrışmasın).

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestAnomalyREDSourceAuditDetail(t *testing.T) {
	prev := chstore.DefaultAnomalySensitivity()
	next := prev
	if _, ok := anomalyREDSourceAuditDetail(prev, next); ok {
		t.Fatal("değişiklik yokken ek denetim satırı olmamalı")
	}
	next.REDSource = chstore.AnomalyREDSourceSpan
	d, ok := anomalyREDSourceAuditDetail(prev, next)
	if !ok || !strings.Contains(d, `"from":{"redSource":"auto"`) || !strings.Contains(d, `"to":{"redSource":"span"`) {
		t.Fatalf("geri alma denetimde görünmeli: %v %s", ok, d)
	}
	env := prev
	env.REDSourceEnv = "prod-eu-a"
	if d, ok := anomalyREDSourceAuditDetail(prev, env); !ok || !strings.Contains(d, `"redSourceEnv":"prod-eu-a"`) {
		t.Fatalf("ortam değişikliği: %v %s", ok, d)
	}
	// Eski blob (alan yok) ile auto aynı şey: ek satır yok.
	if _, ok := anomalyREDSourceAuditDetail(chstore.AnomalySensitivityConfig{}, prev); ok {
		t.Fatal("boş alan = auto; değişiklik sayılmamalı")
	}
}

func TestAnomalyREDFamiliesMatchAPI(t *testing.T) {
	pins := anomaly.REDFamilyPins()
	if len(pins) != len(redFamilies) {
		t.Fatalf("aile sayısı: anomaly %d, api %d", len(pins), len(redFamilies))
	}
	for i, f := range redFamilies {
		p := pins[i]
		if p.ID != f.ID || p.Probe != f.Probe || p.Names != f.names() || p.Unit != f.Unit || p.ServerErr != f.ServerErr || !reflect.DeepEqual(p.StatusLabels, f.StatusLabels) {
			t.Errorf("aile %d ayrıştı:\nanomaly %+v\napi     %+v", i, p, f)
		}
	}
}
