// v0.10.1412 — "CH DateTime parse error in the demo stack".
//
// The exact demo log line was not recorded. The worker:operation-catalog tick
// ("[op-catalog] span: …", api/cosre_operation_catalog.go) is the only call
// site this scan finds, and the shape is the one v0.9.581 recorded verbatim on
// prod: code 53 "Cannot convert string '<…>.176482289' to type DateTime: while
// executing function greaterOrEquals on arguments __table1.time_bucket
// DateTime".
//
// RefreshOperationCatalog bound ONE chDateTime64Arg value (nanosecond
// fraction) into three INSERT…SELECTs: two read `time_bucket` (plain
// DateTime — operation_summary_5m / spanmetrics_1m are toStartOfInterval MVs)
// and one reads oracle_error_log.time (DateTime64(9)). The two span-side
// statements failed server-side, so the span + endpoint halves of
// cosre_operation_catalog were never refreshed.
//
// TestNoDateTime64BindAgainstTimeBucket (chtime_bind_test.go) did not catch
// it: it only looks 25 lines below the `time_bucket >= ?` text, and here the
// SQL is a package const declared far away from the Exec that binds a local
// variable. This test resolves both sides through the AST instead:
//
//   - the SQL argument of every conn.Exec/Query/QueryRow call is resolved to
//     text (string literal, package const/var, `+` concatenation, or the
//     string literals of the package func it calls), and
//   - each bind argument is "nanosecond-tainted" when it is a
//     chDateTime64Arg(...) call or a local identifier assigned from one.
//
// A tainted arg bound into SQL that compares a DateTime column against a bare
// `?` (and has no DateTime64 comparison that could legitimately take it) is
// the v0.9.578 / v0.10.1412 bug.
package chstore

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// dateTimeColCmp — bare `?` compared against a column that is plain DateTime
// (every toStartOfInterval-built MV bucket). Wrapped binds such as
// toDateTime(?, 'UTC') do not match: CH converts those itself.
// v0.10.1419 — `bucket` da (workload_revision_activity_1m.bucket DateTime; prod span'i:
// "Cannot convert string '2026-10-19 18:27:00.809' to type DateTime … __table1.bucket").
// Ad tabanlı: first_seen/last_seen/ts gibi adlar bazı tablolarda DateTime64, bu yüzden eklenmedi.
var dateTimeColCmp = regexp.MustCompile(`\b(time_bucket|bucket)\s*(>=|<=|>|<|=)\s*\?`)

// dateTime64Cmp — any comparison that may legitimately take a nanosecond bind
// (raw `time`, `created_at`, …, or an explicit toDateTime64(?)). When the same
// SQL has one, a tainted slice element may be meant for it, so slice-shaped
// args are only flagged when the SQL has no such comparison.
var dateTime64Cmp = regexp.MustCompile(`(\b(time|created_at|started_at|updated_at|first_seen|last_seen|opened_at|resolved_at)\s*(>=|<=|>|<|=)\s*\?)|toDateTime64\(\s*\?`)

type chPkg struct {
	consts map[string]ast.Expr      // package-level const/var name → init expr
	funcs  map[string]*ast.FuncDecl // package-level func name → decl
}

func loadCHPkg(t *testing.T) (*token.FileSet, []*ast.File, chPkg) {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	p := chPkg{consts: map[string]ast.Expr{}, funcs: map[string]*ast.FuncDecl{}}
	var files []*ast.File
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					vs, ok := sp.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range vs.Names {
						if i < len(vs.Values) {
							p.consts[name.Name] = vs.Values[i]
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil {
					p.funcs[d.Name.Name] = d
				}
			}
		}
	}
	return fset, files, p
}

// sqlText — best-effort text of a SQL expression. Unknown shapes resolve to
// "" (never flagged), so the scan can only miss, not invent, a finding.
func (p chPkg) sqlText(e ast.Expr, depth int) string {
	if depth > 6 || e == nil {
		return ""
	}
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			if s, err := strconv.Unquote(e.Value); err == nil {
				return s
			}
		}
	case *ast.Ident:
		if v, ok := p.consts[e.Name]; ok {
			return p.sqlText(v, depth+1)
		}
	case *ast.ParenExpr:
		return p.sqlText(e.X, depth+1)
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return p.sqlText(e.X, depth+1) + p.sqlText(e.Y, depth+1)
		}
	case *ast.CallExpr:
		if id, ok := e.Fun.(*ast.Ident); ok {
			if fd, ok := p.funcs[id.Name]; ok && fd.Body != nil {
				var b strings.Builder
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					switch n := n.(type) {
					case *ast.BasicLit:
						b.WriteString(p.sqlText(n, depth+1))
					case *ast.Ident:
						if _, ok := p.consts[n.Name]; ok {
							b.WriteString(p.sqlText(n, depth+1))
						}
					}
					return true
				})
				return b.String()
			}
		}
	}
	return ""
}

func isDateTime64ArgCall(e ast.Expr) bool {
	c, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := c.Fun.(*ast.Ident)
	return ok && id.Name == "chDateTime64Arg"
}

// taintedIdents — local names in fn assigned (anywhere) from chDateTime64Arg,
// and slice names that receive such a value via append or a composite literal.
func taintedIdents(fn *ast.BlockStmt) (scalars, slices map[string]bool) {
	scalars, slices = map[string]bool{}, map[string]bool{}
	ast.Inspect(fn, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || i >= len(as.Rhs) {
				continue
			}
			if isDateTime64ArgCall(as.Rhs[i]) {
				scalars[id.Name] = true
			}
		}
		return true
	})
	isTainted := func(e ast.Expr) bool {
		if isDateTime64ArgCall(e) {
			return true
		}
		id, ok := e.(*ast.Ident)
		return ok && scalars[id.Name]
	}
	ast.Inspect(fn, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || i >= len(as.Rhs) {
				continue
			}
			ast.Inspect(as.Rhs[i], func(m ast.Node) bool {
				switch m := m.(type) {
				case *ast.CallExpr:
					if f, ok := m.Fun.(*ast.Ident); ok && f.Name == "append" {
						for _, a := range m.Args[1:] {
							if isTainted(a) {
								slices[id.Name] = true
							}
						}
					}
				case *ast.CompositeLit:
					for _, a := range m.Elts {
						if isTainted(a) {
							slices[id.Name] = true
						}
					}
				}
				return true
			})
		}
		return true
	})
	return scalars, slices
}

var chQueryMethods = map[string]bool{"Exec": true, "Query": true, "QueryRow": true}

// dateTime64BindFindings — every (file:line) where a nanosecond-tainted arg
// reaches SQL that compares a plain DateTime column against a bare `?`.
func dateTime64BindFindings(t *testing.T) []string {
	t.Helper()
	fset, files, p := loadCHPkg(t)
	var out []string
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			scalars, slices := taintedIdents(fd.Body)
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !chQueryMethods[sel.Sel.Name] || len(call.Args) < 3 {
					return true
				}
				sql := p.sqlText(call.Args[1], 0)
				if !dateTimeColCmp.MatchString(sql) {
					return true
				}
				for _, a := range call.Args[2:] {
					bad := isDateTime64ArgCall(a)
					if id, ok := a.(*ast.Ident); ok {
						bad = bad || scalars[id.Name]
						if call.Ellipsis.IsValid() && slices[id.Name] && !dateTime64Cmp.MatchString(sql) {
							bad = true
						}
					}
					if bad {
						out = append(out, fset.Position(a.Pos()).String())
					}
				}
				return true
			})
		}
	}
	return out
}

func TestNoDateTime64BindAgainstDateTimeColumnAST(t *testing.T) {
	for _, pos := range dateTime64BindFindings(t) {
		t.Errorf("%s — nanosecond chDateTime64Arg value bound into SQL that compares "+
			"time_bucket/bucket (plain DateTime, toStartOfInterval MV) against a bare `?`. "+
			"CH rejects the fraction with code 53 \"Cannot convert string … to type "+
			"DateTime\"; bind chDateTimeArg (v0.9.581, v0.10.1412).", pos)
	}
}

// The scan must actually see the v0.10.1412 shape; a resolver regression that
// returned "" for const SQL would make the test above vacuously green.
func TestDateTime64BindScanResolvesConstSQL(t *testing.T) {
	_, _, p := loadCHPkg(t)
	for _, name := range []string{"spanOperationCatalogSQL", "endpointCatalogSQL"} {
		e, ok := p.consts[name]
		if !ok {
			t.Fatalf("%s not found — the scan's fixture moved", name)
		}
		if !dateTimeColCmp.MatchString(p.sqlText(e, 0)) {
			t.Errorf("%s: resolver lost the time_bucket comparison", name)
		}
	}
	if got := p.sqlText(&ast.CallExpr{Fun: ast.NewIdent("oracleOperationCatalogSQL")}, 0); !strings.Contains(got, "oracle_error_log") {
		t.Errorf("func-call SQL not resolved: %q", got)
	}
}
