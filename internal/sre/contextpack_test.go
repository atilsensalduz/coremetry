package sre

// contextpack_test.go — v0.10.1462 (CoSRE A1): Context Pack sözleşmesi —
// bölüm sırası, legacy kip = tek capBytes kesimi, token tahmini, oy / insan
// işareti korkuluğu.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func packKeys(p ContextPack) string {
	var ks []string
	for _, s := range p.Sections {
		ks = append(ks, s.Key)
	}
	return strings.Join(ks, ",")
}

// TestContextPackSectionsOrder — bölüm sırası sabit; boş bölüm pakete girmez;
// Rows = satır sayısı; öncelik 0 başlık bölümleri.
func TestContextPackSectionsOrder(t *testing.T) {
	scs := narrationScenarios()
	byName := map[string]narrationScenario{}
	for _, sc := range scs {
		byName[sc.name] = sc
	}
	full := BuildContextPack(byName["full"].inv, byName["full"].p, byName["full"].loc)
	if got := packKeys(full); got != "problem,rule,start,steps,synth,hypotheses,recommendations,findings" {
		t.Fatalf("sıra: %s", got)
	}
	for _, s := range full.Sections {
		if s.Rows != strings.Count(s.Text, "\n") || s.Rows == 0 {
			t.Errorf("%s: Rows=%d", s.Key, s.Rows)
		}
		if (s.Key == "problem" || s.Key == "rule" || s.Key == "start") != (s.Priority == 0) {
			t.Errorf("%s: öncelik %d", s.Key, s.Priority)
		}
	}
	if got := packKeys(BuildContextPack(byName["min"].inv, chstore.Problem{}, nil)); got != "problem,steps,findings" {
		t.Fatalf("en az: %s", got)
	}
	sum := SummaryOf(byName["full"].inv)
	if got := packKeys(BuildSavedPack("P-1042", sum, nil)); got != "saved_head,saved_findings,saved_hypotheses,saved_summary" {
		t.Fatalf("sohbet sırası: %s", got)
	}
	sum.State = StateRunning
	if len(BuildSavedPack("P-1042", sum, nil).Sections) != 0 {
		t.Fatal("bitmemiş kayıt boş paket")
	}
	if txt, meta := RenderTR(ContextPack{}, chatBudget); txt != "" || meta.Bytes != 0 || len(meta.Sections) != 0 {
		t.Fatalf("boş paket: %q %+v", txt, meta)
	}
}

// TestRenderTRLegacyEqualsCapBytes — legacy kip = bölümlerin birleşimi +
// tek capBytes(…, MaxBytes); künye kesimi ve bölüm boyutlarını taşır.
func TestRenderTRLegacyEqualsCapBytes(t *testing.T) {
	for _, sc := range narrationScenarios() {
		pack := BuildContextPack(sc.inv, sc.p, sc.loc)
		var join strings.Builder
		total := 0
		for _, s := range pack.Sections {
			join.WriteString(s.Text)
			total += len(s.Text)
		}
		got, meta := RenderTR(pack, narrationBudget)
		if want := capBytes(join.String(), renderMaxBytes); got != want {
			t.Errorf("%s: legacy ≠ capBytes(join)", sc.name)
		}
		if meta.Mode != PackModeLegacy || meta.Bytes != len(got) || meta.EstTokens != estimateTokens(got) ||
			meta.Capped != (total > renderMaxBytes) || len(meta.Sections) != len(pack.Sections) {
			t.Errorf("%s: künye %+v (toplam %d)", sc.name, meta, total)
		}
		if sc.name == "capped" && !meta.Capped {
			t.Error("capped senaryosu kesilmeli")
		}
		if uncapped, _ := RenderTR(pack, PackBudget{Mode: PackModeLegacy}); uncapped != join.String() {
			t.Errorf("%s: tavansız bütçe kesmemeli", sc.name)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	for in, want := range map[string]int{"": 0, "a": 1, "abcd": 1, "abcde": 2, "ğğ": 1, strings.Repeat("x", 4096): 1024} {
		if got := estimateTokens(in); got != want {
			t.Errorf("estimateTokens(%q)=%d, beklenen %d", in, got, want)
		}
	}
}

// TestContextPackNeverCarriesHumanOrVotes — pack kaynağı oy / geri bildirim /
// insan işareti / model güveni okumaz (hipotez satırları renderHypothesesTR
// üzerinden gelir, doğrudan değil); kurucuların girdi tiplerinde oy alanı yok.
func TestContextPackNeverCarriesHumanOrVotes(t *testing.T) {
	files, _ := filepath.Glob("contextpack*.go")
	if len(files) == 0 {
		t.Fatal("contextpack.go yok")
	}
	idents := []string{"HumanState", "MarkDismissed", "MarkConfirmed", "ModelConfidence", "Operator"}
	subs := []string{"feedback", "vote", "thumb", "cosre_signals", "cosre_answer_quality", "ai_feedback"}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			var name string
			switch x := n.(type) {
			case *ast.Ident:
				name = x.Name
			case *ast.BasicLit:
				name = x.Value
			default:
				return true
			}
			for _, id := range idents {
				if name == id {
					t.Errorf("%s: %s — pack insan işaretini doğrudan okumaz", fset.Position(n.Pos()), name)
				}
			}
			low := strings.ToLower(name)
			for _, s := range subs {
				if strings.Contains(low, s) {
					t.Errorf("%s: %s — pack oy / geri bildirim okumaz", fset.Position(n.Pos()), name)
				}
			}
			return true
		})
	}
	// Girdi tipleri (Investigation, Problem, sohbet özeti) oy alanı taşımaz.
	seen := map[reflect.Type]bool{}
	var walk func(rt reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array || rt.Kind() == reflect.Map {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			low := strings.ToLower(f.Name + " " + f.Tag.Get("json"))
			for _, b := range []string{"feedback", "vote", "thumb"} {
				if strings.Contains(low, b) {
					t.Errorf("%s.%s — pack girdisi oy / geri bildirim taşımamalı", path, f.Name)
				}
			}
			walk(f.Type, path+"."+f.Name)
		}
	}
	walk(reflect.TypeOf(Investigation{}), "Investigation")
	walk(reflect.TypeOf(chstore.Problem{}), "Problem")
	walk(reflect.TypeOf(mcptools.InvestigationSummary{}), "InvestigationSummary")
}
