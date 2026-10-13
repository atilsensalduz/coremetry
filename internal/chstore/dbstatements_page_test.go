package chstore

// dbstatements_page_test.go — v0.10.1162: bir veritabanının TÜM ifadeleri
// (sayfalı + aranabilir). Saf parçalar: SQL şekli, sayfa sınırları, ham yol
// sayfalaması.

import (
	"strings"
	"testing"
)

func TestDBStatementsMVSQL(t *testing.T) {
	legacy := slowQueriesGlobalMVSQL("WHERE x")
	if !strings.Contains(legacy, "GROUP BY service_name, stmt_hash\n\t\tORDER BY total_ms DESC\n\t\tLIMIT ?\n") || strings.Contains(legacy, "OFFSET") {
		t.Fatalf("eski tek-sayfa SQL değişti:\n%s", legacy)
	}
	for _, withSearch := range []bool{false, true} {
		sql := dbStatementsMVSQL("WHERE x", withSearch)
		if got := strings.Contains(sql, "HAVING positionCaseInsensitiveUTF8(sample_stmt, ?) > 0"); got != withSearch {
			t.Errorf("withSearch=%v: HAVING=%v", withSearch, got)
		}
		want := 2 // limit, offset
		if withSearch {
			want = 3
		}
		if n := strings.Count(sql, "?"); n != want {
			t.Errorf("withSearch=%v: %d bağ, beklenen %d", withSearch, n, want)
		}
		for _, frag := range []string{"ORDER BY total_ms DESC, stmt_hash, service_name", "LIMIT ? OFFSET ?", "max_execution_time"} {
			if !strings.Contains(sql, frag) {
				t.Errorf("withSearch=%v: %q yok", withSearch, frag)
			}
		}
	}
}

func TestClampDBStatementsPage(t *testing.T) {
	for _, c := range []struct{ l, o, wl, wo int }{
		{0, 0, 100, 0}, {50, 100, 50, 100}, {9999, -5, 100, 0}, {200, 999999, 200, dbStatementsMaxOffset},
	} {
		if l, o := ClampDBStatementsPage(c.l, c.o); l != c.wl || o != c.wo {
			t.Errorf("Clamp(%d,%d) = %d,%d; beklenen %d,%d", c.l, c.o, l, o, c.wl, c.wo)
		}
	}
}

func TestPageDBStatements(t *testing.T) {
	mk := func(norm, sample string) SlowQueryRow {
		var r SlowQueryRow
		r.Statement, r.SampleStatement = norm, sample
		return r
	}
	rows := []SlowQueryRow{
		mk("SELECT * FROM orders WHERE id = ?", "SELECT * FROM orders WHERE id = 7"),
		mk("UPDATE accounts SET bal = ?", "UPDATE accounts SET bal = 1"),
		mk("SELECT * FROM Orders_Archive", "SELECT * FROM Orders_Archive"),
	}
	p := pageDBStatements(rows, "", 2, 0)
	if len(p.Rows) != 2 || !p.HasMore {
		t.Fatalf("ilk sayfa: %d satır hasMore=%v", len(p.Rows), p.HasMore)
	}
	if p = pageDBStatements(rows, "", 2, 2); len(p.Rows) != 1 || p.HasMore {
		t.Fatalf("son sayfa: %d satır hasMore=%v", len(p.Rows), p.HasMore)
	}
	if p = pageDBStatements(rows, " ORDERS ", 10, 0); len(p.Rows) != 2 || p.HasMore {
		t.Fatalf("arama büyük/küçük harfsiz olmalı: %d satır", len(p.Rows))
	}
	if p = pageDBStatements(rows, "", 10, 50); p.Rows == nil || len(p.Rows) != 0 {
		t.Fatalf("sınır dışı offset boş (nil değil) dilim dönmeli: %#v", p.Rows)
	}
	if len(rows) != 3 || rows[1].Statement != "UPDATE accounts SET bal = ?" {
		t.Fatal("girdi dilimi değişmemeli")
	}
}
