package chstore

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// db_span_identity_test.go — v0.10.1525: trace span → Databases kimliği.
// İki sözleşme pinlenir: (1) instance/db_name zinciri db_summary_5m ve
// db_summary_1m DDL'iyle BİREBİR (zincir ayrışırsa Trace'teki link boş bir
// /database sayfasına gider), (2) ifade kimliği spans.db_stmt_hash'in Go
// ikizi DBStmtHash'ten — ikinci normalleştirici yok.

var instanceCoalesceRE = regexp.MustCompile(`(?s)coalesce\(\s*nullIf\(peer_service, ''\),(.*?)nullIf\(service_name, ''\),\s*'unknown'\s*\)\s*AS instance`)
var attrKeyRE = regexp.MustCompile(`indexOf\(attr_keys, '([^']+)'\)`)

func TestDBInstanceChainMatchesMVDDL(t *testing.T) {
	for _, mv := range []string{"db_summary_5m", "db_summary_1m"} {
		ddl := canonicalMVDDL(mv)
		m := instanceCoalesceRE.FindStringSubmatch(ddl)
		if m == nil {
			t.Fatalf("%s: instance coalesce zinciri bulunamadı (DDL şekli değişti mi?)", mv)
		}
		var keys []string
		for _, k := range attrKeyRE.FindAllStringSubmatch(m[1], -1) {
			keys = append(keys, k[1])
		}
		if strings.Join(keys, ",") != strings.Join(dbInstanceAttrChain, ",") {
			t.Fatalf("%s instance zinciri %v, Go aynası %v — ayrıştı", mv, keys, dbInstanceAttrChain)
		}
		if !strings.Contains(ddl, "coalesce(nullIf(attr_values[indexOf(attr_keys, 'db.name')], ''), 'default') AS db_name") {
			t.Fatalf("%s: db_name zinciri (db.name → 'default') değişti", mv)
		}
	}
}

func TestDBSpanIdentity(t *testing.T) {
	stmt := "SELECT * FROM orders WHERE id = 42 AND name = 'x'"
	want := strconv.FormatUint(DBStmtHash(stmt), 10)
	cases := []struct {
		name                       string
		sp                         SpanRow
		ok                         bool
		sys, inst, db, stmtHashExp string
	}{
		{"db olmayan span", SpanRow{ServiceName: "orders-api", Attributes: map[string]string{"http.route": "/x"}}, false, "", "", "", ""},
		{"peer_service kazanır", SpanRow{DBSystem: "postgresql", PeerService: "pg-main", ServiceName: "orders-api", DBStatement: stmt,
			Attributes: map[string]string{"server.address": "db.example.test", "db.name": "orders"}}, true, "postgresql", "pg-main", "orders", want},
		{"server.address ikinci", SpanRow{DBSystem: "oracle", ServiceName: "orders-api",
			Attributes: map[string]string{"server.address": "db.example.test", "net.peer.name": "n", "db.name": "KXZONE"}}, true, "oracle", "db.example.test", "KXZONE", ""},
		{"db.host, db.name yok → default", SpanRow{DBSystem: "mysql", ServiceName: "orders-api",
			Attributes: map[string]string{"db.host": "h1"}}, true, "mysql", "h1", "default", ""},
		{"db.name instance olur", SpanRow{DBSystem: "mysql", ServiceName: "orders-api",
			Attributes: map[string]string{"db.name": "orders"}}, true, "mysql", "orders", "orders", ""},
		{"son çare servis", SpanRow{DBSystem: "redis", ServiceName: "orders-api", Attributes: map[string]string{}}, true, "redis", "orders-api", "default", ""},
		{"hiçbiri → unknown", SpanRow{DBSystem: "redis", Attributes: map[string]string{}}, true, "redis", "unknown", "default", ""},
		// Tempo kaynaklı span: kolon alanları boş, ingest alias zinciri.
		{"tempo: db.system.name + db.query.text", SpanRow{ServiceName: "orders-api",
			Attributes: map[string]string{"db.system.name": "postgresql", "db.system": "pg-old", "db.query.text": stmt, "db.statement": "other"}},
			true, "postgresql", "orders-api", "default", want},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sys, inst, db, h, ok := DBSpanIdentity(c.sp)
			if ok != c.ok || sys != c.sys || inst != c.inst || db != c.db || h != c.stmtHashExp {
				t.Fatalf("got (%q,%q,%q,%q,%v) want (%q,%q,%q,%q,%v)", sys, inst, db, h, ok, c.sys, c.inst, c.db, c.stmtHashExp, c.ok)
			}
		})
	}
}

// Literal farkı aynı sınıfa düşer — MV'nin gruplama sözleşmesi.
func TestDBSpanIdentityHashFoldsLiterals(t *testing.T) {
	a := SpanRow{DBSystem: "postgresql", DBStatement: "SELECT 1 FROM t WHERE id = 7"}
	b := SpanRow{DBSystem: "postgresql", DBStatement: "SELECT 1 FROM t WHERE id = 9001"}
	_, _, _, ha, _ := DBSpanIdentity(a)
	_, _, _, hb, _ := DBSpanIdentity(b)
	if ha == "" || ha != hb {
		t.Fatalf("literal farkı ayrı kimlik üretti: %q vs %q", ha, hb)
	}
}

func TestStampDBSpanIdentity(t *testing.T) {
	spans := []SpanRow{
		{ServiceName: "orders-api", Attributes: map[string]string{}},
		{DBSystem: "postgresql", ServiceName: "orders-api", DBStatement: "SELECT 1", Attributes: map[string]string{"db.name": "orders"}},
	}
	StampDBSpanIdentity(spans)
	if spans[0].DBInstance != "" || spans[0].DBStmtHash != "" {
		t.Fatalf("DB olmayan span damgalandı: %+v", spans[0])
	}
	if spans[1].DBInstance != "orders" || spans[1].DBName != "orders" || spans[1].DBStmtHash == "" {
		t.Fatalf("DB span damgası eksik: %+v", spans[1])
	}
}
