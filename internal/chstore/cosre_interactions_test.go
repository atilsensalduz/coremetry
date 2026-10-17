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
// v0.10.1302 — toolset kolonu aynı sözleşmeyle: dört bayrak bileşimi; açıkken
// en son arg turun araç seti.
func TestCosreInteractionArgsAlignWithColumns(t *testing.T) {
	r := CosreInteraction{Toolset: "simple", Tools: []CosreTool{{Name: "a", OK: true, Evidence: "metrics:12"}, {Name: "b", Error: "x"}}}
	for _, c := range []cosreInteractionCols{{}, {Evidence: true}, {Toolset: true}, {Evidence: true, Toolset: true}} {
		sql := cosreInteractionsInsertSQL(c)
		cols := strings.Count(sql[strings.Index(sql, "(")+1:], ",") + 1
		args := cosreInteractionArgs(r, c)
		if len(args) != cols {
			t.Fatalf("%+v: kolon %d, argüman %d", c, cols, len(args))
		}
		if strings.Contains(sql, "tool_evidence") != c.Evidence {
			t.Errorf("%+v: tool_evidence kolonu beklenmedik durumda", c)
		}
		if strings.Contains(sql, ", toolset") != c.Toolset {
			t.Errorf("%+v: toolset kolonu beklenmedik durumda", c)
		}
		for k := 16; k < 24; k++ {
			if l := lenOf(args[k]); l != 2 {
				t.Errorf("%+v: arg %d dizi boyu %d", c, k, l)
			}
		}
		if args[30] != "ok" {
			t.Errorf("boş status varsayılanı: %v", args[30])
		}
		if c.Toolset && args[len(args)-1] != "simple" {
			t.Errorf("%+v: toolset son arg olmalı: %v", c, args[len(args)-1])
		}
		if c.Evidence {
			at := len(args) - 1
			if c.Toolset {
				at--
			}
			last, _ := args[at].([]string)
			if len(last) != 2 || last[0] != "metrics:12" || last[1] != "" {
				t.Errorf("tool_evidence paralel dizi değil: %v", args[at])
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
