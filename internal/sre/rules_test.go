package sre

// v0.10.1289 — CoSRE operatör kuralları AI SRE ajanında: kurallar YALNIZ
// anlatım girdisine (renderInvestigationTR paketinin sonu + sistem mesajı eki)
// girer; Score / planSteps / herhangi bir kontrol onları ASLA okumaz
// (operatör: kural insan yazımı bağlam notudur, kanıt değil; hipotez skoru
// yalnız telemetri gerçeklerinden — TestVotesNeverReachRuntimeDecisions'ın
// kural ikizi).

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/cosrerules"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestSRENarrationIncludesRules(t *testing.T) {
	at := start.UnixNano()
	rules := []cosrerules.Rule{
		{ID: "a", Text: "checkout'ta 02:00 batch'i 5xx üretir, alarm değildir", Scope: cosrerules.Scope{Kind: cosrerules.ScopeService, Value: "checkout"}, Author: "Ayşe", CreatedAt: at},
		{ID: "b", Text: "payments ekibi deploy'u Jenkins'ten yapar", Scope: cosrerules.Scope{Kind: cosrerules.ScopeTeam, Value: "payments"}, Author: "Ali", CreatedAt: at},
		{ID: "c", Text: "başka servisin kuralı burada görünmez", Scope: cosrerules.Scope{Kind: cosrerules.ScopeService, Value: "orders"}, Author: "Can", CreatedAt: at},
		{ID: "d", Text: "genel kural: hafta sonu trafik yarıya düşer", Scope: cosrerules.Scope{Kind: cosrerules.ScopeGlobal}, Author: "Ece", CreatedAt: at},
	}
	n := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: x"}
	rd := okReaders(&calls{}, n)
	rd.rules = func() []cosrerules.Rule { return rules }
	inv := newInv()
	if st := narrate(context.Background(), rd, inv, chstore.Problem{Service: "checkout", OwnerTeam: "payments"}, time.Now); st.State != StepOK {
		t.Fatalf("anlatım: %+v", st)
	}
	for _, want := range []string{cosrerules.BlockHeader, cosrerules.FenceOpen, "[servis checkout] checkout'ta 02:00", "[takım payments]", "[genel]", cosrerules.FenceClose} {
		if !strings.Contains(n.user, want) {
			t.Errorf("anlatım girdisinde %q yok:\n%s", want, n.user)
		}
	}
	if strings.Contains(n.user, "başka servisin kuralı") {
		t.Error("ilgisiz servis kuralı anlatıma girmemeli")
	}
	if strings.Index(n.user, "PROBLEM:") > strings.Index(n.user, cosrerules.BlockHeader) {
		t.Error("kural bloğu paketin SONUNDA olmalı")
	}
	if !strings.HasPrefix(n.sys, copilot.CosreRulesAddendum()) || !strings.HasSuffix(n.sys, copilot.SystemPromptSREInvestigation()) ||
		!strings.HasSuffix(n.sys, copilot.DataNotInstruction) {
		t.Error("ek sistem mesajının önünde, DataNotInstruction sonda kalmalı")
	}
	// Kural yokken iki metin bayt bayt eski.
	n2 := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: x"}
	rd2 := okReaders(&calls{}, n2)
	rd2.rules = func() []cosrerules.Rule { return nil }
	inv2 := newInv()
	narrate(context.Background(), rd2, inv2, chstore.Problem{Service: "checkout"}, time.Now)
	if n2.sys != copilot.SystemPromptSREInvestigation() || strings.Contains(n2.user, cosrerules.BlockHeader) || strings.Contains(n2.user, cosrerules.FenceOpen) {
		t.Errorf("kuralsız anlatım değişmemeli:\n%s", n2.user)
	}
}

// TestRulesNeverReachScoring — kaynak taraması: internal/sre'de kural
// belgesine / paketine dokunan tek yer anlatım (narrate.go) ve okuyucu dikişi
// (orchestrator.go readers.rules). Score, planSteps, kontroller (chk*) ve
// hipotez/plan/olgu dosyaları kuralları görmez.
func TestRulesNeverReachScoring(t *testing.T) {
	needles := []string{"cosrerules", "CurrentCosreRules", "CosreRules", ".rules"}
	allowed := map[string]bool{"narrate.go": true, "orchestrator.go": true}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, n := range needles {
			if strings.Contains(src, n) && !allowed[f] {
				t.Errorf("%s: %q — operatör kuralı yalnız anlatıma girer; skor/plan/kontrol okumaz", f, n)
			}
		}
	}
	// İzinli iki dosyada da kural yalnız anlatım fonksiyonlarında / dikiş
	// alanında: Score, planSteps, evidenceFrom ve her chk* gövdesi temiz.
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			touches := false
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if id, ok := x.X.(*ast.Ident); ok && id.Name == "cosrerules" {
						touches = true
					}
					if x.Sel.Name == "rules" || x.Sel.Name == "CurrentCosreRules" {
						touches = true
					}
				}
				return true
			})
			if !touches {
				continue
			}
			if name != "narrate" && name != "narrationRulesBlock" {
				t.Errorf("%s: %s kurallara dokunuyor — yalnız narrate/narrationRulesBlock izinli", f, name)
			}
		}
	}
	// Skor girdisi (Evidence) kural alanı taşımaz.
	if strings.Contains(strings.ToLower(typeFields(t, "hypotheses.go", "Evidence")), "rule") {
		t.Error("Evidence kural alanı taşımamalı")
	}
}

func typeFields(t *testing.T, file, typ string) string {
	t.Helper()
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	ast.Inspect(af, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != typ {
			return true
		}
		if st, ok := ts.Type.(*ast.StructType); ok {
			for _, f := range st.Fields.List {
				for _, nm := range f.Names {
					out = append(out, nm.Name)
				}
			}
		}
		return false
	})
	if len(out) == 0 {
		t.Fatalf("%s içinde %s bulunamadı", file, typ)
	}
	return strings.Join(out, ",")
}
