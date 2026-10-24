package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1491 — GET /api/endpoints/operation-card (operasyon kartı, R3 trace
// yarısı). Pinler: rota defterde ve rol kapısız (viewer görür), 400
// sınırları + 24 saat pencere kırpması, hash-ALL-inputs önbellek anahtarı,
// bölüm toleransı (ağaç / pencere sayısı ayrı düşer, ham CH hatası
// viewer'a gitmez), R4 yuvası `code` null.

func TestOperationCardRouteRegisteredWithoutRoleGate(t *testing.T) {
	if _, ok := extraRouteRegistrars["operation-card"]; !ok {
		t.Fatal(`registerRoutesExtra("operation-card", …) defterde yok — kayıtsız rota SPA'ya düşer (200 + boş ekran)`)
	}
	mux := (&Server{}).buildMux()
	req := httptest.NewRequest("GET", "/api/endpoints/operation-card", nil)
	if _, pattern := mux.Handler(req); pattern != "GET /api/endpoints/operation-card" {
		t.Fatalf("kalıp %q", pattern)
	}
	// Viewer: rol kapısı olsaydı 403; kapısız uç doğrulamaya (400) ulaşır.
	req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u1", Role: auth.RoleViewer}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("viewer + eksik parametre: kod %d, 400 bekleniyordu (403 = rol kapısı sızdı)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Fatalf("400 gövdesi JSON olmalı (writeJSONError), Content-Type %q", ct)
	}
}

func TestParseOperationCardParams(t *testing.T) {
	to := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	q := func(kv ...string) map[string][]string {
		m := map[string][]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = []string{kv[i+1]}
		}
		return m
	}
	bad := []struct {
		name string
		q    map[string][]string
		f, t time.Time
	}{
		{"service yok", q("path", "/x"), from, to},
		{"path yok", q("service", "orders-api"), from, to},
		{"boş servis", q("service", "  ", "path", "/x"), from, to},
		{"entry geçersiz", q("service", "s", "path", "/x", "entry", "grpc"), from, to},
		{"sample geçersiz", q("service", "s", "path", "/x", "sample", "fast"), from, to},
		{"path çok uzun", q("service", "s", "path", strings.Repeat("a", opCardMaxPathLen+1)), from, to},
		{"servis çok uzun", q("service", strings.Repeat("s", opCardMaxServiceLen+1), "path", "/x"), from, to},
		{"ters pencere", q("service", "s", "path", "/x"), to, from},
	}
	for _, c := range bad {
		if _, msg := parseOperationCardParams(c.q, c.f, c.t); msg == "" {
			t.Errorf("%s: 400 bekleniyordu", c.name)
		}
	}
	p, msg := parseOperationCardParams(q("service", " orders-api ", "path", "/orders/{id}"), from, to)
	if msg != "" || p.Service != "orders-api" || p.Entry != "http" || p.Sample != "slow" || p.Sig || p.Code || p.Clamped {
		t.Fatalf("varsayılanlar: %+v %q", p, msg)
	}
	p, _ = parseOperationCardParams(q("service", "s", "path", "orders.Approve", "entry", "rpc", "sample", "random", "sig", "1", "code", "1"), from, to)
	if p.Entry != "rpc" || p.Sample != "random" || !p.Sig || !p.Code {
		t.Fatalf("bayraklar: %+v", p)
	}
	// 7 günlük istek son 24 saate kırpılır.
	p, msg = parseOperationCardParams(q("service", "s", "path", "/x"), to.Add(-7*24*time.Hour), to)
	if msg != "" || !p.Clamped || p.To.Sub(p.From) != operationCardMaxWindow || !p.To.Equal(to) {
		t.Fatalf("kırpma: %+v %q", p, msg)
	}
}

func TestOperationCardKeyHashesAllInputs(t *testing.T) {
	to := time.Date(2026, 10, 24, 12, 0, 30, 0, time.UTC)
	base := operationCardParams{
		Service: "orders-api", Path: "/orders/{id}", Entry: "http", Sample: "slow",
		Env: "prod", Cluster: "c1", From: to.Add(-time.Hour), To: to,
	}
	k0 := operationCardKey(base)
	variants := map[string]func(p *operationCardParams){
		"service": func(p *operationCardParams) { p.Service = "payments-api" },
		"path":    func(p *operationCardParams) { p.Path = "/orders" },
		"sig":     func(p *operationCardParams) { p.Sig = true },
		"entry":   func(p *operationCardParams) { p.Entry = "rpc" },
		"sample":  func(p *operationCardParams) { p.Sample = "random" },
		"code":    func(p *operationCardParams) { p.Code = true },
		"env":     func(p *operationCardParams) { p.Env = "uat" },
		"cluster": func(p *operationCardParams) { p.Cluster = "c2" },
		"from":    func(p *operationCardParams) { p.From = p.From.Add(-time.Minute) },
		"to":      func(p *operationCardParams) { p.To = p.To.Add(time.Minute) },
		// Alan sınırı sahte bir yolla taklit edilemez (NUL ayraç).
		"boundary": func(p *operationCardParams) { p.Service, p.Path = "orders-api/orders", "/{id}" },
		"env↔clu":  func(p *operationCardParams) { p.Env, p.Cluster = p.Cluster, p.Env },
	}
	seen := map[string]string{k0: "base"}
	for name, mut := range variants {
		p := base
		mut(&p)
		k := operationCardKey(p)
		if prev, dup := seen[k]; dup {
			t.Errorf("%s anahtarı %s ile çakıştı: %s", name, prev, k)
		}
		seen[k] = name
	}
	// Kararlılık: aynı dakikanın içindeki iki istek aynı girdiyi paylaşır.
	p := base
	p.To = p.To.Add(20 * time.Second)
	p.From = p.From.Add(20 * time.Second)
	if operationCardKey(p) != k0 {
		t.Error("aynı dakika içinde anahtar değişmemeli")
	}
	if !strings.HasPrefix(k0, "opcard:v1:") {
		t.Errorf("anahtar öneki: %s", k0)
	}
}

type fakeOpCardReader struct {
	tree      *chstore.OpCallTree
	treeErr   error
	edges     []chstore.OpWindowEdge
	edgesErr  error
	edgeCalls int
	gotQ      chstore.OpCallTreeQuery
	gotOp     string
}

func (f *fakeOpCardReader) OperationCallTree(_ context.Context, q chstore.OpCallTreeQuery) (*chstore.OpCallTree, error) {
	f.gotQ = q
	return f.tree, f.treeErr
}

func (f *fakeOpCardReader) OperationWindowEdges(_ context.Context, _ string, op string, _, _ time.Time) ([]chstore.OpWindowEdge, error) {
	f.edgeCalls++
	f.gotOp = op
	return f.edges, f.edgesErr
}

func TestBuildOperationCardSectionTolerance(t *testing.T) {
	to := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	p := operationCardParams{Service: "orders-api", Path: "/orders/{id}", Entry: "http", Sample: "slow", From: to.Add(-time.Hour), To: to}
	ctx := context.Background()

	t.Run("ağaç hatası → tree null + temiz mesaj, ham hata sızmaz", func(t *testing.T) {
		rd := &fakeOpCardReader{treeErr: errors.New("code: 159, clickhouse-0.internal:9000 timeout")}
		out, err := buildOperationCard(ctx, rd, p)
		if err != nil || out.Tree != nil || out.TreeError == "" {
			t.Fatalf("out=%+v err=%v", out, err)
		}
		b, _ := json.Marshal(out)
		if strings.Contains(string(b), "clickhouse-0") || strings.Contains(string(b), "159") {
			t.Fatalf("ham CH hatası payload'a sızdı: %s", b)
		}
		if !strings.Contains(string(b), `"tree":null`) || !strings.Contains(string(b), `"code":null`) {
			t.Fatalf("tree/code null olmalı: %s", b)
		}
		if rd.edgeCalls != 0 {
			t.Fatal("ağaç yokken MV sorgulanmamalı")
		}
	})

	t.Run("pencere sayısı hatası → [] + not, ağaç yaşar", func(t *testing.T) {
		rd := &fakeOpCardReader{
			tree:     &chstore.OpCallTree{Entry: chstore.OpCallNode{Op: "POST /orders/{id}"}},
			edgesErr: errors.New("boom"),
		}
		out, err := buildOperationCard(ctx, rd, p)
		if err != nil || out.Tree == nil || out.WindowEdgesNote == "" {
			t.Fatalf("out=%+v err=%v", out, err)
		}
		b, _ := json.Marshal(out)
		for _, want := range []string{`"children":[]`, `"codes":[]`, `"windowEdges":[]`} {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s yok (nil dilim FE'de .map() kırar): %s", want, b)
			}
		}
		if rd.gotOp != "POST /orders/{id}" {
			t.Errorf("MV'nin parent_op'u giriş span adı olmalı, %q", rd.gotOp)
		}
	})

	t.Run("şekil kipi → MV sorgulanmaz, not", func(t *testing.T) {
		sp := p
		sp.Sig = true
		rd := &fakeOpCardReader{tree: &chstore.OpCallTree{Entry: chstore.OpCallNode{Op: "GET /orders/:id"}}}
		out, _ := buildOperationCard(ctx, rd, sp)
		if rd.edgeCalls != 0 || out.WindowEdgesNote == "" || !rd.gotQ.BySignature {
			t.Fatalf("şekil kipi: calls=%d note=%q q=%+v", rd.edgeCalls, out.WindowEdgesNote, rd.gotQ)
		}
	})

	t.Run("rpc girişi + random örneklem chstore'a taşınır", func(t *testing.T) {
		rp := p
		rp.Entry, rp.Sample, rp.Path = "rpc", "random", "orders.Approve"
		rd := &fakeOpCardReader{tree: &chstore.OpCallTree{}}
		if _, err := buildOperationCard(ctx, rd, rp); err != nil {
			t.Fatal(err)
		}
		if rd.gotQ.Entry != chstore.EntryRPC || rd.gotQ.Sample != chstore.EndpointSampleRandom || rd.gotQ.Path != "orders.Approve" {
			t.Fatalf("sorgu: %+v", rd.gotQ)
		}
		if rd.edgeCalls != 0 {
			t.Fatal("boş örneklemde (giriş adı yok) MV sorgulanmamalı")
		}
	})

	t.Run("iptal → hata döner (önbelleğe yazılmaz)", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		rd := &fakeOpCardReader{treeErr: context.Canceled}
		if _, err := buildOperationCard(cctx, rd, p); !errors.Is(err, context.Canceled) {
			t.Fatalf("iptal yutuldu: %v", err)
		}
	})
}
