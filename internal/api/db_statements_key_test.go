package api

// v0.10.1525 — /api/databases/statements sunucu sırası: sıra önbellek
// anahtarının girdisi (v0.5.187 — her girdi anahtarı değiştirir).

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestDBStatementsKeySort(t *testing.T) {
	from := time.Date(2026, 10, 24, 11, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	base := dbStatementsKey(from, to, "postgresql", "orders", "", 200, 0, chstore.DefaultDBStatementsSort)
	for name, k := range map[string]string{
		"sort key": dbStatementsKey(from, to, "postgresql", "orders", "", 200, 0, chstore.DBStatementsSort{Key: "count", Desc: true}),
		"sort dir": dbStatementsKey(from, to, "postgresql", "orders", "", 200, 0, chstore.DBStatementsSort{Key: "totalMs"}),
		"offset":   dbStatementsKey(from, to, "postgresql", "orders", "", 200, 200, chstore.DefaultDBStatementsSort),
		"q":        dbStatementsKey(from, to, "postgresql", "orders", "select", 200, 0, chstore.DefaultDBStatementsSort),
	} {
		if k == base {
			t.Errorf("%s anahtarı değiştirmedi: %s", name, k)
		}
	}
}
