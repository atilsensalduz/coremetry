package chstore

// v0.10.409 — ai_calls iki-boot sözleşmesi. İnceleme bulgusu: edit
// betiğinin çift koşumu genişletilmiş INSERT bloğunu üç kez ekleyip eski
// kolon dalını ULAŞILMAZ yapmıştı — ilk boot'ta (kolonlar yokken) her
// AI çağrısı kaydı Distributed tarafından reddedilecekti. Her iki dal
// saf yardımcılarla pinlenir: SQL kolon sayısı == arg sayısı.

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestAICallsInsertBranches(t *testing.T) {
	c := AICall{ID: "x", PromptVersion: "v", ErrorClass: "timeout", TTFTMs: 12, StreamFallback: true, ShieldHits: 1, CachedTokens: 77,
		TraceID: "0af7651916cd43dd8448eb211c80319c"}
	// v0.10.807 — ext × cached; v0.10.1463 — × traced (üç ayrı probe, üç
	// bayrak → sekiz kombinasyon; her biri ilk boot'ta tek başına false
	// olabilir).
	sqls := map[string]bool{}
	for _, ext := range []bool{false, true} {
		for _, cached := range []bool{false, true} {
			for _, traced := range []bool{false, true} {
				sql := aiCallsInsertSQL(ext, cached, traced)
				sqls[sql] = true
				inner := sql[strings.Index(sql, "(")+1 : strings.LastIndex(sql, ")")]
				n := len(strings.Split(inner, ","))
				args := aiCallsInsertArgs(c, time.Unix(0, 0).UTC(), ext, cached, traced)
				if n != len(args) {
					t.Fatalf("ext=%v cached=%v traced=%v: SQL %d kolon, %d arg", ext, cached, traced, n, len(args))
				}
				for _, col := range strings.Split(aiCallsExtCols, ", ") {
					if strings.Contains(inner, col) != ext {
						t.Errorf("ext=%v: kolon %q beklenmedik durumda", ext, col)
					}
				}
				if strings.Contains(inner, "cached_tokens") != cached {
					t.Errorf("cached=%v: cached_tokens kolonu beklenmedik durumda", cached)
				}
				if strings.Contains(inner, "trace_id") != traced {
					t.Errorf("traced=%v: trace_id kolonu beklenmedik durumda", traced)
				}
				last := args[len(args)-1]
				switch {
				case traced:
					if last != c.TraceID {
						t.Errorf("trace_id son arg olmalı, %v", last)
					}
				case cached:
					if last != uint32(77) {
						t.Errorf("cached_tokens son arg olmalı, %v", last)
					}
				case ext:
					if args[len(args)-2] != uint8(1) {
						t.Errorf("stream_fallback UInt8(1) olmalı, %v", args[len(args)-2])
					}
				}
			}
		}
	}
	if len(sqls) != 8 {
		t.Fatalf("sekiz dal sekiz ayrı SQL üretmeli, %d", len(sqls))
	}
	// Satır okuması da bayrağa uyar: SELECT kolon sayısı == Scan hedefi.
	for _, cached := range []bool{false, true} {
		for _, traced := range []bool{false, true} {
			sel := aiCallsRowSelect(cached, traced)
			n := len(strings.Split(strings.TrimPrefix(sel, "SELECT "), ","))
			var row AICall
			if got := len(aiCallsRowScan(&row, cached, traced)); got != n {
				t.Errorf("cached=%v traced=%v: SELECT %d kolon, Scan %d hedef", cached, traced, n, got)
			}
			if strings.Contains(sel, "trace_id") != traced {
				t.Errorf("traced=%v: SELECT trace_id beklenmedik durumda", traced)
			}
		}
	}
}

// Probe yalnız INSERT hedefine bakar: yerel parçada olup Distributed'da
// henüz olmayan kolon "var" sayılırsa INSERT kırılır.
func TestAICallsProbeTargetsInsertTable(t *testing.T) {
	b, err := os.ReadFile("ai_calls.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "'ai_calls_local'") {
		t.Fatal("probe yerel parçaya bakmamalı — INSERT hedefi ai_calls")
	}
	if !strings.Contains(src, "table = 'ai_calls' AND name = 'error_class'") {
		t.Fatal("probe `table = 'ai_calls'` yüklemini taşımalı")
	}
	if !strings.Contains(src, "table = 'ai_calls' AND name = 'cached_tokens'") { // v0.10.807
		t.Fatal("cached_tokens probe'u da INSERT hedefine bakmalı")
	}
	if !strings.Contains(src, "table = 'ai_calls' AND name = 'trace_id'") { // v0.10.1463
		t.Fatal("trace_id probe'u da INSERT hedefine bakmalı")
	}
}

// v0.10.423 — E5 nokta okuması da iki-boot sözleşmesine uyar: genişletilmiş
// kolonlar yalnız bayrakla; SQL kolon sayısı == Scan hedefi sayısı.
func TestAICallEvalSelectBranches(t *testing.T) {
	base := aiCallEvalSelect(false)
	ext := aiCallEvalSelect(true)
	if strings.Contains(base, "prompt_version") || !strings.Contains(ext, "prompt_version, profile_id, error_class") {
		t.Fatalf("dallar yanlış: base=%q ext=%q", base, ext)
	}
	for _, q := range []string{base, ext} {
		if !strings.Contains(q, "WHERE exchange_id = ?") || !strings.Contains(q, "LIMIT 1") || !strings.Contains(q, "max_execution_time") {
			t.Fatalf("nokta okuma sınırları eksik: %q", q)
		}
	}
	inner := ext[len("SELECT "):strings.Index(ext, " FROM")]
	if n := len(strings.Split(inner, ",")); n != 11 {
		t.Fatalf("ext 11 kolon (8 + 3) olmalı, %d", n)
	}
}
