package api

// operation_card_code_test.go — v0.10.1506 (plan-operation-card §7 R4):
// ?code=1 dalı. Pinler: kod GÖVDESİ yanıta girmez (fixture'daki MARKER_42 /
// SQL / payload sabitleri JSON'da yok, content/windows anahtarı yok), katalog
// pini fail-closed (okunamayan pin → catalog-error, DevOps'a 0 istek), Code
// Search kapalı / DevOps yok → dürüst reason + 0 istek, istek kurucusu.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/devops"
)

const ocTestRoot = "/src/main/java/com/example/orders"

type ocGit struct {
	mu   sync.Mutex
	reqs int
}

func readOpcardFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "devops", "testdata", "opcard", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newOcGit — sahte Azure DevOps: Code Search "approve" → controller; ağaç +
// dosya içeriği fixture'lardan.
func newOcGit(t *testing.T, codeSearch bool) (*ocGit, *devops.Service) {
	t.Helper()
	g := &ocGit{}
	files := map[string]string{
		ocTestRoot + "/web/OrderController.java":      readOpcardFixture(t, "OrderController.java"),
		ocTestRoot + "/service/OrderServiceImpl.java": readOpcardFixture(t, "OrderServiceImpl.java"),
		ocTestRoot + "/client/PaymentsClient.java":    readOpcardFixture(t, "PaymentsClient.java"),
		ocTestRoot + "/api/ApproveRequest.java":       readOpcardFixture(t, "ApproveRequest.java"),
		ocTestRoot + "/api/ApproveResponse.java":      readOpcardFixture(t, "ApproveResponse.java"),
		ocTestRoot + "/api/ChannelInfo.java":          readOpcardFixture(t, "ChannelInfo.java"),
		ocTestRoot + "/service/OrderService.java":     "public interface OrderService { ApproveResponse approve(long id, ApproveRequest r); }",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.reqs++
		g.mu.Unlock()
		p, q := r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(p, "/_apis/search/codesearchresults"):
			var body struct {
				SearchText string `json:"searchText"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.SearchText != `"approve" ext:java` {
				_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{
				map[string]any{"path": ocTestRoot + "/web/OrderController.java", "repository": map[string]string{"name": "orders-api", "type": "git"},
					"matches": map[string]any{"content": []any{map[string]any{"line": 30, "codeSnippet": `String marker = "MARKER_42"`}}}},
			}})
		case strings.HasSuffix(p, "/refs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []map[string]string{{"name": "refs/heads/release"}}})
		case strings.HasSuffix(p, "/items") && q.Get("recursionLevel") == "Full":
			items := []map[string]any{}
			for pth := range files {
				items = append(items, map[string]any{"path": pth, "gitObjectType": "blob"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": items})
		case strings.HasSuffix(p, "/items"):
			c, ok := files[q.Get("path")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": c})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"defaultBranch": "refs/heads/release"})
		}
	}))
	t.Cleanup(srv.Close)
	dv := devops.New()
	dv.Configure(devops.Settings{BaseURL: srv.URL, Collection: "DefaultCollection", Project: "Orders",
		PAT: "test-pat", Flavor: devops.FlavorServer, CodeSearch: codeSearch})
	return g, dv
}

func (g *ocGit) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reqs
}

type fakeOpCardMeta struct {
	md  *chstore.ServiceMetadata
	err error
}

func (f fakeOpCardMeta) GetServiceMetadataStrict(context.Context, string) (*chstore.ServiceMetadata, error) {
	return f.md, f.err
}

func ocTestParams() operationCardParams {
	to := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	return operationCardParams{Service: "orders-api", Path: "/orders/{id}/approve", Entry: "http", Sample: "slow", Code: true, From: to.Add(-time.Hour), To: to}
}

func ocTestTree() *chstore.OpCallTree {
	return &chstore.OpCallTree{
		Entry: chstore.OpCallNode{Service: "orders-api", Op: "POST /orders/{id}/approve"},
		Children: []chstore.OpCallEdge{
			{Depth: 1, ParentKey: "entry", Key: "svc:payments-api|POST /payments/authorize", Kind: "service", Protocol: "http", Target: "payments-api", Op: "POST /payments/authorize"},
			{Depth: 1, ParentKey: "entry", Key: "queue:kafka:order-events", Kind: "messaging", Protocol: "kafka", Target: "queue:kafka:order-events", Op: "order-events publish"},
		},
	}
}

func TestOperationCardCodePayloadCarriesNoCodeBodies(t *testing.T) {
	g, dv := newOcGit(t, true)
	code, err := buildOperationCardCode(context.Background(), dv, nil, ocTestParams(), ocTestTree())
	if err != nil {
		t.Fatal(err)
	}
	if code.Entry == nil || code.Entry.Found != "route" || !strings.Contains(code.Entry.Signature, "approve(") {
		t.Fatalf("giriş bulunmalıydı: %+v reason=%q", code.Entry, code.Reason)
	}
	if len(code.Request) == 0 || len(code.CallSites) == 0 || code.Source != devops.RepoSourceConvention {
		t.Fatalf("DTO/çağrı yeri/kaynak: %+v", code)
	}
	matched := 0
	for _, s := range code.CallSites {
		if s.MatchedChild >= 0 {
			matched++
		}
	}
	if matched != 2 {
		t.Errorf("payments + kafka eşlenmeliydi: %+v", code.CallSites)
	}
	if g.count() == 0 {
		t.Fatal("DevOps'a hiç istek gitmedi")
	}
	payload := operationCardPayload{Service: "orders-api", Path: "/orders/{id}/approve", Tree: ocTestTree(), Code: code}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	// Fixture kaynaklarındaki gövde-içi sabitler (dize, SQL, payload,
	// ilklendirici) ve satır numaralı içerik HİÇBİR alanda yok.
	for _, leak := range []string{"MARKER_42", "MARKER_SQL", "MARKER_PAYLOAD", "MARKER_BODY", "MARKER_DEFAULT", "MARKER_CONST", "UPDATE orders", "| ", "ResponseEntity.ok("} {
		if strings.Contains(js, leak) {
			t.Errorf("kod gövdesi yanıta sızdı (%q): %s", leak, js)
		}
	}
	var generic map[string]any
	_ = json.Unmarshal(b, &generic)
	codeObj, _ := generic["code"].(map[string]any)
	for _, k := range []string{"windows", "content", "body", "snippet", "Windows"} {
		if _, ok := codeObj[k]; ok {
			t.Errorf("code.%s anahtarı olmamalı", k)
		}
	}
	for _, k := range []string{"outcome", "entry", "request", "response", "callSites", "stats"} {
		if _, ok := codeObj[k]; !ok {
			t.Errorf("code.%s anahtarı eksik: %s", k, js)
		}
	}
	if entry, _ := codeObj["entry"].(map[string]any); entry != nil {
		for _, k := range []string{"content", "body"} {
			if _, ok := entry[k]; ok {
				t.Errorf("entry.%s olmamalı", k)
			}
		}
	}
}

func TestOperationCardCodeGates(t *testing.T) {
	ctx := context.Background()
	t.Run("DevOps yok → unconfigured, null değil", func(t *testing.T) {
		d, err := buildOperationCardCode(ctx, nil, nil, ocTestParams(), ocTestTree())
		if err != nil || d.Outcome != string(devops.CodeUnconfigured) || d.Reason == "" || d.Request == nil || d.CallSites == nil {
			t.Fatalf("%+v %v", d, err)
		}
	})
	t.Run("Code Search kapalı → search-off + 0 istek", func(t *testing.T) {
		g, dv := newOcGit(t, false)
		d, err := buildOperationCardCode(ctx, dv, nil, ocTestParams(), ocTestTree())
		if err != nil || d.Outcome != opCardSearchOff || d.Reason != devops.OperationCardSearchOffReason || d.Entry != nil {
			t.Fatalf("%+v %v", d, err)
		}
		if g.count() != 0 {
			t.Errorf("kapalıyken %d istek", g.count())
		}
	})
	t.Run("katalog okunamadı → fail-closed, 0 istek, ham hata yok", func(t *testing.T) {
		g, dv := newOcGit(t, true)
		d, err := buildOperationCardCode(ctx, dv, fakeOpCardMeta{err: errors.New("dial tcp clickhouse-0:9000 password=hunter2")}, ocTestParams(), ocTestTree())
		if err != nil || d.Outcome != string(devops.CodeCatalogError) || g.count() != 0 {
			t.Fatalf("%+v %v istek=%d", d, err, g.count())
		}
		if strings.Contains(d.Reason, "hunter2") || strings.Contains(d.Reason, "clickhouse-0") {
			t.Errorf("ham hata sızdı: %q", d.Reason)
		}
	})
	t.Run("pin depoyu ezer", func(t *testing.T) {
		_, dv := newOcGit(t, true)
		d, _ := buildOperationCardCode(ctx, dv, fakeOpCardMeta{md: &chstore.ServiceMetadata{Repository: "pinned-repo"}}, ocTestParams(), ocTestTree())
		if d.Source != devops.RepoSourcePin {
			t.Errorf("kaynak pin olmalı: %+v", d)
		}
	})
	t.Run("iptal → hata döner", func(t *testing.T) {
		_, dv := newOcGit(t, true)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := buildOperationCardCode(cctx, dv, nil, ocTestParams(), ocTestTree()); !errors.Is(err, context.Canceled) {
			t.Fatalf("iptal yutuldu: %v", err)
		}
	})
}

func TestOperationCardCodeRequest(t *testing.T) {
	p := ocTestParams()
	tree := ocTestTree()
	tree.Version, tree.OperationCode = "1.42.0", "ORDER_APPROVE"
	req := operationCardCodeRequest(p, tree, "orders-api", devops.ProjectHint{})
	if req.Method != "POST" || req.Route != "/orders/{id}/approve" || req.Entry != "http" || req.Version != "1.42.0" ||
		req.Operation != "ORDER_APPROVE" || len(req.Children) != 2 || req.Children[0].Target != "payments-api" {
		t.Fatalf("%+v", req)
	}
	tree.Entry.Op = "orders approve" // yöntem öneki yok
	if r := operationCardCodeRequest(p, tree, "x", devops.ProjectHint{}); r.Method != "" {
		t.Errorf("fiil olmayan önek yöntem sayılmamalı: %q", r.Method)
	}
	rp := p
	rp.Entry, rp.Path = "rpc", "risk.RiskService/Score"
	if r := operationCardCodeRequest(rp, tree, "x", devops.ProjectHint{}); r.Method != "" || r.Route != "risk.RiskService/Score" || r.Entry != "rpc" {
		t.Errorf("rpc: %+v", r)
	}
	if r := operationCardCodeRequest(p, nil, "x", devops.ProjectHint{}); r.Route != p.Path || r.Children != nil {
		t.Errorf("ağaçsız: %+v", r)
	}
}
