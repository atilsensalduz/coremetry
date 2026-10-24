package chstore

// dbstatements_page_test.go — v0.10.1162: bir veritabanının TÜM ifadeleri
// (sayfalı + aranabilir). Saf parçalar: SQL şekli, sayfa sınırları, ham yol
// sayfalaması.

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestDBStatementsMVSQL(t *testing.T) {
	legacy := slowQueriesGlobalMVSQL("WHERE x")
	if !strings.Contains(legacy, "GROUP BY service_name, stmt_hash\n\t\tORDER BY total_ms DESC\n\t\tLIMIT ?\n") || strings.Contains(legacy, "OFFSET") {
		t.Fatalf("eski tek-sayfa SQL değişti:\n%s", legacy)
	}
	for _, withSearch := range []bool{false, true} {
		sql := dbStatementsMVSQL("WHERE x", withSearch, DefaultDBStatementsSort)
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

// v0.10.1525 — sunucu sırası (DataTable serverSort): beyaz liste, doğal yön,
// SQL'e yalnız takma ad girer, ham yol aynı anahtarla sıralar.
func TestDBStatementsSort(t *testing.T) {
	for _, c := range []struct {
		key, dir string
		want     DBStatementsSort
	}{
		{"", "", DefaultDBStatementsSort},
		{"totalMs; DROP TABLE spans", "asc", DefaultDBStatementsSort},
		{"count", "", DBStatementsSort{Key: "count", Desc: true}},
		{"count", "asc", DBStatementsSort{Key: "count"}},
		{"service", "", DBStatementsSort{Key: "service"}},
		{"p99Ms", "desc", DBStatementsSort{Key: "p99Ms", Desc: true}},
	} {
		if got := NormalizeDBStatementsSort(c.key, c.dir); got != c.want {
			t.Errorf("Normalize(%q,%q) = %+v; beklenen %+v", c.key, c.dir, got, c.want)
		}
	}
	sql := dbStatementsMVSQL("WHERE x", false, DBStatementsSort{Key: "count"})
	if !strings.Contains(sql, "ORDER BY cnt ASC, stmt_hash, service_name") {
		t.Fatalf("sıra SQL'e yansımadı:\n%s", sql)
	}
	if sql := dbStatementsMVSQL("WHERE x", false, DBStatementsSort{Key: "evil"}); !strings.Contains(sql, "ORDER BY total_ms DESC, stmt_hash") {
		t.Fatalf("bilinmeyen anahtar varsayılana düşmeli:\n%s", sql)
	}
	// Her beyaz liste ifadesi MV okumasının SELECT'inde bir takma ad.
	head := slowQueriesMVHead("WHERE x")
	for k, col := range dbStatementsSortCols {
		for _, part := range strings.Fields(strings.ReplaceAll(col, "/", " ")) {
			if !strings.Contains(head, "AS "+part) && part != "service_name" {
				t.Errorf("%s → %q MV okumasında takma ad değil", k, part)
			}
		}
	}
	mk := func(svc, hash string, cnt int, total float64) SlowQueryRow {
		var r SlowQueryRow
		r.Service, r.StmtHash, r.Count, r.TotalMs = svc, hash, cnt, total
		return r
	}
	rows := []SlowQueryRow{mk("b", "1", 5, 10), mk("a", "2", 50, 1), mk("c", "3", 5, 99)}
	sortDBStatementRows(rows, DBStatementsSort{Key: "count", Desc: true})
	if rows[0].StmtHash != "2" || rows[1].StmtHash != "1" || rows[2].StmtHash != "3" {
		t.Fatalf("ham yol sırası (count desc, bağda kimlik): %+v", rows)
	}
	sortDBStatementRows(rows, DefaultDBStatementsSort)
	if rows[0].StmtHash != "3" {
		t.Fatalf("varsayılan toplam süre azalan: %+v", rows)
	}
}

// v0.10.1525 — FE beyaz listesi (SlowQueries.tsx STMT_SORT_KEYS) sunucununkiyle
// birebir: FE'nin gönderip sunucunun tanımadığı anahtar sessizce toplam
// süreye düşer ve başlık oku YALAN söyler.
func TestDBStatementsSortKeysMatchFrontend(t *testing.T) {
	src, err := os.ReadFile("../../frontend/src/pages/SlowQueries.tsx")
	if err != nil {
		t.Skipf("frontend kaynağı yok: %v", err)
	}
	m := regexp.MustCompile(`STMT_SORT_KEYS = \[([^\]]*)\]`).FindSubmatch(src)
	if m == nil {
		t.Fatal("STMT_SORT_KEYS bulunamadı")
	}
	var fe []string
	for _, q := range regexp.MustCompile(`'([A-Za-z0-9]+)'`).FindAllSubmatch(m[1], -1) {
		fe = append(fe, string(q[1]))
	}
	var be []string
	for k := range dbStatementsSortCols {
		if k != "p95Ms" && k != "maxMs" { // sunucuda var, sayfada kolon yok
			be = append(be, k)
		}
	}
	sort.Strings(fe)
	sort.Strings(be)
	if strings.Join(fe, ",") != strings.Join(be, ",") {
		t.Fatalf("FE %v ≠ sunucu %v", fe, be)
	}
}
