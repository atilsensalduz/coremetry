package chstore

// db_span_identity.go — v0.10.1525 (operatör, prod ekran görüntüsü 2026-10-24):
// Trace sayfasındaki bir DB span'inin DATABASE grubu (db.system, db.name,
// db.statement, server.address …) düz metindi; `service.name` servis
// sayfasına gidiyor, veritabanı ve ifade hiçbir yere gitmiyordu.
//
// Link kurmak için span'in, Databases sayfalarının KULLANDIĞI kimliğe
// çevrilmesi gerekiyor — ve o kimlik ön yüzde yeniden türetilirse iki yer
// sessizce ayrışır (spanEntityLinks.ts "NEDEN SUNUCUNUN ÇÖZDÜĞÜ DEĞER"
// gerekçesinin aynısı). Bu yüzden kimlik SUNUCUDA, MV'lerin kendi
// kurallarıyla damgalanır:
//
//   - DBInstance / DBName — db_summary_5m (ve 1m ikizi) `instance` /
//     `db_name` coalesce zincirlerinin Go aynası. /database detayının
//     kimliği ÜÇLÜ (system, instance, dbName — databaseParam.ts); zincir
//     ayrışırsa link boş bir detay sayfasına gider. db_span_identity_test.go
//     zinciri store.go'daki DDL METNİNDEN okuyup karşılaştırır.
//   - DBStmtHash — spans.db_stmt_hash'in Go ikizi (DBStmtHash, dbstmt.go
//     parite sözleşmesi). İKİNCİ BİR NORMALLEŞTİRİCİ YOK: aynı
//     NormalizeDBStatement + xxHash64. Ön yüzde xxHash64 + RE2 semantiği
//     yeniden yazılmaz.
//
// Neden span okumasında DEĞİL de yük katmanında: SpanRow ingest yapısı da
// (repo.go INSERT listesi açık kolonlu, bu alanları anmaz) ve Tempo-önce
// çözümleme (trace_resolve.go) CH'yi hiç görmeyebilir; damga her iki
// kaynaktan gelen span'e AYNI kuralla basılır.

import "strconv"

// dbInstanceAttrChain — db_summary_5m `instance` coalesce'inin attribute
// basamakları (peer_service kolonundan SONRA, service_name'den ÖNCE).
// Sıra yüklü: test DDL'den çıkarıp birebir karşılaştırır.
var dbInstanceAttrChain = []string{"server.address", "net.peer.name", "db.host", "db.name"}

// dbSystemAttrChain / dbStatementAttrChain — ingest alias zincirleri
// (otlp/semconv.go spanAttrAliases "db_system" / "db_statement"). Yalnız
// kolon değeri BOŞ geldiğinde (Tempo kaynaklı span) devreye girer.
var (
	dbSystemAttrChain    = []string{"db.system.name", "db.system"}
	dbStatementAttrChain = []string{"db.query.text", "db.statement"}
)

func firstAttr(attrs map[string]string, keys []string) string {
	for _, k := range keys {
		if v := attrs[k]; v != "" {
			return v
		}
	}
	return ""
}

// DBSpanIdentity — SAF: bir span'in Databases kimliği. ok=false → DB span'i
// değil (db_system boş — MV'lerin boş-olmayan db_system yüklemi).
func DBSpanIdentity(sp SpanRow) (system, instance, dbName, stmtHash string, ok bool) {
	system = sp.DBSystem
	if system == "" {
		system = firstAttr(sp.Attributes, dbSystemAttrChain)
	}
	if system == "" {
		return "", "", "", "", false
	}
	instance = sp.PeerService
	if instance == "" {
		instance = firstAttr(sp.Attributes, dbInstanceAttrChain)
	}
	if instance == "" {
		instance = sp.ServiceName
	}
	if instance == "" {
		instance = "unknown"
	}
	dbName = sp.Attributes["db.name"]
	if dbName == "" {
		dbName = "default"
	}
	stmt := sp.DBStatement
	if stmt == "" {
		stmt = firstAttr(sp.Attributes, dbStatementAttrChain)
	}
	if h := DBStmtHash(stmt); h != 0 {
		stmtHash = strconv.FormatUint(h, 10)
	}
	return system, instance, dbName, stmtHash, true
}

// StampDBSpanIdentity — trace yükündeki her DB span'ine kimliği basar
// (yerinde). DB olmayan span'lere dokunmaz (alanlar omitempty → yük
// büyümez). Maliyet: span başına en çok 8 KiB'lık tek geçiş normalleştirme.
func StampDBSpanIdentity(spans []SpanRow) {
	for i := range spans {
		sys, inst, name, hash, ok := DBSpanIdentity(spans[i])
		if !ok {
			continue
		}
		spans[i].DBSystem = sys
		spans[i].DBInstance = inst
		spans[i].DBName = name
		spans[i].DBStmtHash = hash
	}
}
