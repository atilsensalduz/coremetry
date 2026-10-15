package chstore

import (
	"strings"
	"testing"
)

// CoSRE öğrenme Faz 1 — INSERT kolon listesi ile argüman sayısı ve paralel
// tool dizileri hizalı kalmalı (yeni kolon eklenip arg unutulursa kırmızı).
func TestCosreInteractionArgsAlignWithColumns(t *testing.T) {
	cols := strings.Count(cosreInteractionsInsertSQL[strings.Index(cosreInteractionsInsertSQL, "(")+1:], ",") + 1
	r := CosreInteraction{Tools: []CosreTool{{Name: "a", OK: true}, {Name: "b", Error: "x"}}}
	args := cosreInteractionArgs(r)
	if len(args) != cols {
		t.Fatalf("kolon %d, argüman %d", cols, len(args))
	}
	for k := 16; k < 24; k++ {
		if l := lenOf(args[k]); l != 2 {
			t.Errorf("arg %d dizi boyu %d", k, l)
		}
	}
	if args[30] != "ok" {
		t.Errorf("boş status varsayılanı: %v", args[30])
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
