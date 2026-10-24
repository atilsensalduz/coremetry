package mcptools

// v0.10.1494 (CoSRE 2d) — list_deployments satırının git kaynağı EK alandır:
// rollout satırında Origin yoksa (Deps.RolloutOrigin nil / Argo kapalı) JSON
// bayt bayt eski; varsa origin + origin_confidence. Araç şeması/açıklaması
// değişmez (altın katalog + 74 pini ayrı testlerde).

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
)

func TestListDeploymentsOriginAdditive(t *testing.T) {
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	ro := chstore.RolloutRow{Rollout: rollout.Rollout{ClusterID: "c-1", Namespace: "shop", Workload: "checkout", StartedAt: at, ImageTag: "1.4.1", PrevImageTag: "1.4.2"}}
	plain := mergeChanges(nil, []chstore.RolloutRow{ro}, "checkout", true, 10)
	b, _ := json.Marshal(plain)
	if strings.Contains(string(b), "origin") {
		t.Fatalf("origin yokken alan yok: %s", b)
	}
	ro.Origin = &rollout.RolloutOrigin{Kind: rollout.OriginRollback, Confidence: 70}
	rows := mergeChanges(nil, []chstore.RolloutRow{ro}, "checkout", true, 10)
	b, _ = json.Marshal(rows)
	if rows[0].Origin != "rollback" || rows[0].OriginConfidence != 70 || !strings.Contains(string(b), `"origin":"rollback","origin_confidence":70`) {
		t.Fatalf("origin: %s", b)
	}
}
