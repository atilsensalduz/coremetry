package main

// v0.10.1282 — AI SRE Faz 0a: sre rolü, kuyruk, drain, durum.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseRunModeValue(t *testing.T) {
	cases := []struct {
		in                                  string
		name                                string
		ingest, api, worker, agent, sre, so bool
	}{
		{"", "all", true, true, true, true, true, false},
		{" ALL ", "all", true, true, true, true, true, false},
		{"ingest", "ingest", true, false, false, false, false, false},
		{"api", "api", false, true, false, false, false, false},
		{"worker", "worker", false, false, true, false, false, false}, // worker sre İÇERMEZ
		{"agent", "agent", false, false, false, true, false, false},
		{"sre", "sre", false, false, false, false, true, true},
		{"Sre", "sre", false, false, false, false, true, true},
	}
	for _, c := range cases {
		m, err := parseRunModeValue(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if m.name != c.name || m.ingest != c.ingest || m.api != c.api || m.worker != c.worker || m.agent != c.agent || m.sre != c.sre || m.sreOnly() != c.so {
			t.Errorf("%q → %+v (sreOnly=%v)", c.in, m, m.sreOnly())
		}
	}
	_, err := parseRunModeValue("jobs")
	if err == nil || !strings.Contains(err.Error(), "sre") {
		t.Fatalf("geçersiz değer hata vermeli ve izinli listede sre olmalı: %v", err)
	}
}

// TestSREStartsOnlyUnderSREMode — go/ast: sre.StartDrain yalnız `if mode.sre`
// gövdesinde, sre.StartEnqueuer yalnız `if mode.worker` gövdesinde çağrılır.
func TestSREStartsOnlyUnderSREMode(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"StartDrain": "sre", "StartEnqueuer": "worker"}
	found := map[string]int{}
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "sre" {
			return true
		}
		flag, tracked := want[sel.Sel.Name]
		if !tracked {
			return true
		}
		found[sel.Sel.Name]++
		// En yakın kapsayan if'in koşulu tam olarak mode.<flag> olmalı.
		var cond ast.Expr
		for i := len(stack) - 2; i >= 0; i-- {
			if is, ok := stack[i].(*ast.IfStmt); ok {
				cond = is.Cond
				break
			}
		}
		cs, ok := cond.(*ast.SelectorExpr)
		if !ok || cs.Sel.Name != flag {
			t.Errorf("%s: sre.%s `if mode.%s` içinde olmalı", fset.Position(call.Pos()), sel.Sel.Name, flag)
		} else if id, ok := cs.X.(*ast.Ident); !ok || id.Name != "mode" {
			t.Errorf("%s: sre.%s koşulu mode.%s olmalı", fset.Position(call.Pos()), sel.Sel.Name, flag)
		}
		return true
	})
	for name := range want {
		if found[name] != 1 {
			t.Errorf("sre.%s main.go'da tam bir kez çağrılmalı, %d", name, found[name])
		}
	}
}

func TestParseIdleExit(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"": 0, "0": 0, "-5": 0, "120": 120 * time.Second, " 30 ": 30 * time.Second, "2m": 2 * time.Minute, "garbage": 0, "-1m": 0,
	} {
		if got := parseIdleExit(in); got != want {
			t.Errorf("parseIdleExit(%q) = %s, beklenen %s", in, got, want)
		}
	}
	all, _ := parseRunModeValue("all")
	t.Setenv("COREMETRY_SRE_IDLE_EXIT", "120")
	if idleExitFor(all) != 0 {
		t.Fatal("all modunda süreç-içi drain asla idle exit yapmaz")
	}
	sreMode, _ := parseRunModeValue("sre")
	if idleExitFor(sreMode) != 120*time.Second {
		t.Fatal("sre modu env'i okur")
	}
}

func TestSREHealthHandler(t *testing.T) {
	h := sreHealthHandler()
	for path, code := range map[string]int{"/livez": 200, "/readyz": 200, "/api/version": 200, "/api/problems": 404, "/": 404, "/v1/traces": 404} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != code {
			t.Errorf("GET %s = %d, beklenen %d", path, w.Code, code)
		}
	}
}
