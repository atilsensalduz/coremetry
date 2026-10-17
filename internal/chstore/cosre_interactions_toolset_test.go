package chstore

import (
	"strings"
	"testing"
)

// v0.10.1302 — CoSRE: modele göre araç seti. cosre_interactions.toolset
// düşük hacimli tabloya ADD COLUMN IF NOT EXISTS ile gelir (alters dilimi,
// kapalı küme → LowCardinality); kolon varken ikinci boot göndermez. Probe
// tek system.columns sorgusuyla iki kolonu da okur; ALTER listesi ile probe
// listesi aynı adları taşır.
func TestCosreInteractionsToolsetMigration(t *testing.T) {
	var mine []string
	for _, q := range migrateDDLSlice(t, "alters") {
		if tbl, col, ok := ddlAddsColumn(q); ok && tbl == "cosre_interactions" && col == "toolset" {
			mine = append(mine, q)
		}
	}
	if len(mine) != 1 {
		t.Fatalf("alters diliminde %d toolset ALTER'ı (beklenen 1)", len(mine))
	}
	if !strings.Contains(mine[0], "LowCardinality(String) DEFAULT ''") {
		t.Errorf("tip/DEFAULT: %s", mine[0])
	}
	if send, _ := planAlterDDL(mine, map[string]bool{}); len(send) != 1 {
		t.Error("kolon yokken ALTER gitmeli")
	}
	if send, skipped := planAlterDDL(mine, map[string]bool{"cosre_interactions.toolset": true}); len(send) != 0 || skipped != 1 {
		t.Error("kolon varken ALTER gitmemeli (idempotent)")
	}
	src := readFileT(t, "cosre_interactions.go")
	for _, col := range cosreInteractionsOptionalCols {
		if !strings.Contains(src, "'"+col+"'") {
			t.Errorf("probe sorgusu %s kolonunu okumuyor", col)
		}
	}
}
