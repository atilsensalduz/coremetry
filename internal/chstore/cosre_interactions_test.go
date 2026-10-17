package chstore

import (
	"strings"
	"testing"
)

// CoSRE öğrenme Faz 1 — INSERT kolon listesi ile argüman sayısı ve paralel
// tool dizileri hizalı kalmalı (yeni kolon eklenip arg unutulursa kırmızı).
// v0.10.1280 — CoSRE adım 8: kalıcılık + geri bildirim → ai_calls.
// tool_evidence kolonu iki-boot sözleşmesiyle: bayrak kapalıyken INSERT
// kolonu ANMAZ, açıkken son arg paralel kanıt özeti dizisidir.
func TestCosreInteractionArgsAlignWithColumns(t *testing.T) {
	r := CosreInteraction{Tools: []CosreTool{{Name: "a", OK: true, Evidence: "metrics:12"}, {Name: "b", Error: "x"}}}
	for _, ev := range []bool{false, true} {
		sql := cosreInteractionsInsertSQL(ev)
		cols := strings.Count(sql[strings.Index(sql, "(")+1:], ",") + 1
		args := cosreInteractionArgs(r, ev)
		if len(args) != cols {
			t.Fatalf("ev=%v: kolon %d, argüman %d", ev, cols, len(args))
		}
		if strings.Contains(sql, "tool_evidence") != ev {
			t.Errorf("ev=%v: tool_evidence kolonu beklenmedik durumda", ev)
		}
		for k := 16; k < 24; k++ {
			if l := lenOf(args[k]); l != 2 {
				t.Errorf("ev=%v: arg %d dizi boyu %d", ev, k, l)
			}
		}
		if args[30] != "ok" {
			t.Errorf("boş status varsayılanı: %v", args[30])
		}
		if ev {
			last, _ := args[len(args)-1].([]string)
			if len(last) != 2 || last[0] != "metrics:12" || last[1] != "" {
				t.Errorf("tool_evidence paralel dizi değil: %v", args[len(args)-1])
			}
		}
	}
}

func lenOf(v any) int {
	switch a := v.(type) {
	case []string:
		return len(a)
	case []uint32:
		return len(a)
	case []uint8:
		return len(a)
	}
	return -1
}
