package api

// search_routes_test.go — v0.10.1333. Rota defterde (api.go büyümez),
// "GET /api/search" kalıbıyla çözülür, rol kapısı yok (viewer okur), audit yok
// (yazma değil); serveCached closure'ı VERİLEN ctx'i kullanır (v0.8.319 —
// r.Context()'e kapanan closure SWR tazelemesini context.Canceled ile
// öldürürdü). Href'ler güvenli uygulama yolu: "/" ile başlar, "//" ya da şema
// ile başlamaz, kimlik değerleri kaçışlanır.

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestSearchRouteRegistered(t *testing.T) {
	src, err := os.ReadFile("search.go")
	if err != nil {
		t.Fatal(err)
	}
	code := stripGoComments(string(src))
	if !strings.Contains(code, `registerRoutesExtra("search", (*Server).registerSearchRoutes)`) {
		t.Fatal("search.go init() defter kaydı yok")
	}
	if strings.Contains(code, "s.audit(") || strings.Contains(code, "RequireRole") || strings.Contains(code, "RequireAnyRole") {
		t.Fatal("arama salt-okunur: rol kapısı / audit yok")
	}
	mux := (&Server{}).buildMux()
	if _, pat := mux.Handler(httptest.NewRequest("GET", "/api/search?q=ab", nil)); pat != "GET /api/search" {
		t.Fatalf("GET /api/search → %q", pat)
	}
}

func TestSearchServeCachedClosureUsesGivenCtx(t *testing.T) {
	src, err := os.ReadFile("search.go")
	if err != nil {
		t.Fatal(err)
	}
	body := funcBody(stripGoComments(string(src)), "getSearch")
	i := strings.Index(body, "s.serveCached(")
	if i < 0 {
		t.Fatal("getSearch serveCached kullanmalı")
	}
	closure := body[i:]
	if !strings.Contains(closure, "func(ctx context.Context)") || !strings.Contains(closure, "s.runSearch(ctx,") {
		t.Fatalf("closure verilen ctx'i runSearch'e geçirmeli:\n%s", closure)
	}
	if strings.Contains(closure, "r.Context()") {
		t.Fatalf("closure r.Context()'e kapanıyor (v0.8.319):\n%s", closure)
	}
	// Fan-out errgroup DEĞİL: bir kaynağın hatası kardeşini iptal etmemeli.
	for _, f := range []string{"search.go", "search_sources.go"} {
		b, _ := os.ReadFile(f)
		if strings.Contains(stripGoComments(string(b)), "errgroup") {
			t.Fatalf("%s errgroup kullanıyor — tek hata kardeşleri iptal eder", f)
		}
	}
}

func TestSearchHrefsSafeAppPaths(t *testing.T) {
	hostile := "//evil.example/x?y=1&z"
	rows := []SearchResult{}
	rows = append(rows, serviceResults([]string{hostile}, "prod")...)
	rows = append(rows, endpointResults([]chstore.OperationHit{{Service: hostile, Name: "GET /a&b"}})...)
	rows = append(rows, problemResults([]chstore.Problem{{ID: hostile, RuleName: "r", Service: "s", Severity: "critical"}})...)
	rows = append(rows, exceptionResults([]chstore.ExceptionGroup{{Fingerprint: hostile, Type: "E", Message: "m"}})...)
	rows = append(rows, traceResults([]chstore.TraceRow{{TraceID: hostile, RootName: "root"}}, "")...)
	rows = append(rows, logResults([]*logstore.LogRecord{{Body: "b", ServiceName: hostile}}, "javascript:alert(1)", "")...)
	if len(rows) != 6 {
		t.Fatalf("her türden bir satır: %d", len(rows))
	}
	for _, r := range rows {
		if !isSafeSearchHref(r.Href) {
			t.Errorf("%s href güvenli uygulama yolu değil: %q", r.Type, r.Href)
		}
		if strings.Contains(r.Href, "//evil") || strings.Contains(r.Href, "&z") {
			t.Errorf("%s href kaçışlanmamış: %q", r.Type, r.Href)
		}
	}
	for _, bad := range []string{"", "//evil", "https://x", "javascript:alert(1)", "/\\evil"} {
		if isSafeSearchHref(bad) {
			t.Errorf("güvensiz href kabul edildi: %q", bad)
		}
	}
	if got := rows[0].Href; got != "/service?env=prod&name=%2F%2Fevil.example%2Fx%3Fy%3D1%26z" {
		t.Errorf("service href: %q", got)
	}
	if got := rows[4].Href; !strings.HasPrefix(got, "/trace?id=") {
		t.Errorf("trace href: %q", got)
	}
	if got := rows[2].Href; !strings.HasPrefix(got, "/problems?problem=") {
		t.Errorf("problem href: %q", got)
	}
	if got := rows[3].Href; !strings.HasPrefix(got, "/problems?exc=") {
		t.Errorf("exception href: %q", got)
	}
	if got := rows[1].Href; !strings.HasPrefix(got, "/traces?") || !strings.Contains(got, "rootOnly=false") || !strings.Contains(got, "filters=") {
		t.Errorf("endpoint href (operationTracesHref aynası): %q", got)
	}
}

func TestProblemMatchesQuery(t *testing.T) {
	p := chstore.Problem{ID: "abc123def", RuleName: "High Latency", Service: "payments", Description: "p95 over 2s on Checkout"}
	disp := chstore.ProblemDisplayID(p.ID)
	for _, q := range []string{"latency", "PAYMENTS", "checkout", disp, strings.ToLower(disp)} {
		if !problemMatchesQuery(p, q) {
			t.Errorf("%q eşleşmeliydi", q)
		}
	}
	if problemMatchesQuery(p, "orders") {
		t.Error("orders eşleşmemeli")
	}
}
