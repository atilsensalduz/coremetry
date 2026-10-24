package devops

// operation_card_test.go — v0.10.1506 (plan-operation-card §7 R4):
// FetchOperationCard uçtan uca sahte DevOps sunucusuyla (fakeTFS) —
// istek bütçesi (≤4 arama, ≤8 dosya), Code Search kapalı yolu (0 istek),
// önbellek (sıcak tekrar 0 istek, eşleme yine taze), önbellek anahtarı.

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const (
	ocRoot   = "/src/main/java/com/example/orders"
	ocCtrl   = ocRoot + "/web/OrderController.java"
	ocSvc    = ocRoot + "/service/OrderService.java"
	ocImpl   = ocRoot + "/service/OrderServiceImpl.java"
	ocClient = ocRoot + "/client/PaymentsClient.java"
	ocReq    = ocRoot + "/api/ApproveRequest.java"
	ocResp   = ocRoot + "/api/ApproveResponse.java"
	ocChan   = ocRoot + "/api/ChannelInfo.java"
)

func opCardFixtureTFS(t *testing.T, search bool) (*fakeTFS, *Service) {
	t.Helper()
	f := newFakeTFS(t)
	f.tree = []string{ocCtrl, ocSvc, ocImpl, ocClient, ocReq, ocResp, ocChan, ocRoot + "/web/OrderControllerTest.java"}
	f.files[ocCtrl] = opcardFixture(t, "OrderController.java")
	f.files[ocSvc] = "package x; public interface OrderService { ApproveResponse approve(long id, ApproveRequest r); }"
	f.files[ocImpl] = opcardFixture(t, "OrderServiceImpl.java")
	f.files[ocClient] = opcardFixture(t, "PaymentsClient.java")
	f.files[ocReq] = opcardFixture(t, "ApproveRequest.java")
	f.files[ocResp] = opcardFixture(t, "ApproveResponse.java")
	f.files[ocChan] = opcardFixture(t, "ChannelInfo.java")
	f.searchFor = func(text string) []CodeSearchHit {
		if text == `"approve" ext:java` {
			return []CodeSearchHit{
				{Project: "Orders", Repository: "orders-api", Path: ocRoot + "/web/OrderControllerTest.java"},
				{Project: "Orders", Repository: "orders-api", Path: ocCtrl},
			}
		}
		return nil
	}
	svc := New()
	cfg := f.settings()
	cfg.CodeSearch = search
	svc.Configure(cfg)
	return f, svc
}

func opCardTestReq() OperationCardRequest {
	return OperationCardRequest{
		Repo: "orders-api", Service: "orders-api", Entry: "http", Method: "POST", Route: "/orders/{id}/approve",
		Children: []CardChildHint{
			{ParentKey: "entry", Kind: "service", Protocol: "http", Target: "payments-api", Op: "POST /payments/authorize"},
			{ParentKey: "entry", Kind: "db", Protocol: "db", Target: "db:oracle", Op: "SELECT orders"},
			{ParentKey: "entry", Kind: "messaging", Protocol: "kafka", Target: "queue:kafka:order-events", Op: "order-events publish"},
		},
	}
}

func TestFetchOperationCard_RouteEndToEnd(t *testing.T) {
	f, svc := opCardFixtureTFS(t, true)
	cc := svc.FetchOperationCard(context.Background(), opCardTestReq())
	if cc.Entry == nil || cc.Entry.Path != ocCtrl || cc.Entry.Found != "route" || !strings.Contains(cc.Entry.Signature, "approve(") {
		t.Fatalf("giriş: %+v reason=%q outcome=%q", cc.Entry, cc.Reason, cc.Outcome)
	}
	if !strings.Contains(cc.Entry.URL, "line=") {
		t.Errorf("giriş linki satırlı olmalı: %q", cc.Entry.URL)
	}
	names := func(fs []DTOField) string {
		var b []string
		for _, x := range fs {
			b = append(b, x.Owner+"."+x.Name+":"+x.Type)
		}
		return strings.Join(b, ",")
	}
	if got := names(cc.Request); got != "ApproveRequest.amount:BigDecimal,ApproveRequest.channel:ChannelInfo,ApproveRequest.tags:List<String>,ApproveRequest.valueDate:LocalDate,ChannelInfo.code:String,ChannelInfo.name:String,ChannelInfo.parent:ChannelInfo" {
		t.Errorf("istek alanları: %s", got)
	}
	if got := names(cc.Response); got != "ApproveResponse.status:ApprovalStatus,ApproveResponse.message:String" {
		t.Errorf("cevap alanları: %s", got)
	}
	byCallee := map[string]CallSite{}
	for _, s := range cc.CallSites {
		byCallee[s.Callee] = s
	}
	if s := byCallee["paymentsClient.authorize"]; s.Service != "payments-api" || s.Target != "/payments/authorize" || s.MatchedChild != 0 || s.Path != ocImpl {
		t.Errorf("Feign çağrısı: %+v", s)
	}
	if s := byCallee["orderRepository.findById"]; s.MatchedChild != -1 {
		// İki db çağrı yeri (repository + jdbcTemplate) tek db çocuğu: belirsiz.
		t.Errorf("belirsiz db eşlenmemeli: %+v", s)
	}
	if s := byCallee["kafkaTemplate.send"]; s.Target != "order-events" || s.MatchedChild != 2 || !strings.Contains(s.WebURL, "line=17") {
		t.Errorf("kafka: %+v", s)
	}
	if s, ok := byCallee["auditClient.record"]; !ok || s.Target != "/audit/events" || s.MatchedChild != -1 {
		t.Errorf("audit: %+v", s)
	}
	if cc.Outcome != CodePartial || !strings.Contains(cc.Reason, "AuditClient") || !strings.Contains(cc.Reason, "ApprovalStatus") {
		t.Errorf("kısmi + dürüst not bekleniyordu: %q %q", cc.Outcome, cc.Reason)
	}
	if len(cc.Windows) < 2 || !strings.Contains(cc.Windows[0].Content, "MARKER_42") {
		t.Errorf("model pencereleri (giriş + çağrılan) dolu olmalı: %d", len(cc.Windows))
	}
	f.mu.Lock()
	search, items := f.hits["search"], f.hits["item"]
	f.mu.Unlock()
	if search > opCardSearchCap || items > opCardFileCap || search != cc.Searches || items != cc.Stats.Fetched {
		t.Fatalf("bütçe: arama=%d/%d dosya=%d/%d (rapor %d/%d)", search, opCardSearchCap, items, opCardFileCap, cc.Searches, cc.Stats.Fetched)
	}

	// Sıcak tekrar: istek yok, eşleme yeni çocuklarla yeniden.
	req2 := opCardTestReq()
	req2.Children = req2.Children[1:] // payments çocuğu yok
	cc2 := svc.FetchOperationCard(context.Background(), req2)
	f.mu.Lock()
	search2, items2 := f.hits["search"], f.hits["item"]
	f.mu.Unlock()
	if search2 != search || items2 != items {
		t.Errorf("önbellekten gelmeliydi: arama %d→%d dosya %d→%d", search, search2, items, items2)
	}
	for _, s := range cc2.CallSites {
		if s.Callee == "paymentsClient.authorize" && s.MatchedChild != -1 {
			t.Errorf("eşleme önbellekten bayat geldi: %+v", s)
		}
		if s.Callee == "kafkaTemplate.send" && s.MatchedChild != 1 {
			t.Errorf("yeni çocuk dizisine göre indeks 1 olmalı: %+v", s)
		}
	}
}

func TestFetchOperationCard_OffPaths(t *testing.T) {
	t.Run("Code Search kapalı → 0 istek + dürüst neden", func(t *testing.T) {
		f, svc := opCardFixtureTFS(t, false)
		cc := svc.FetchOperationCard(context.Background(), opCardTestReq())
		if !cc.SearchOff || cc.Reason != OperationCardSearchOffReason || cc.Entry != nil || f.countReqs("") != 0 {
			t.Errorf("kapalı: %+v istek=%v", cc, f.snapshotReqs())
		}
		if cc.CallSites == nil {
			t.Error("CallSites nil olmamalı (FE .map())")
		}
	})
	t.Run("yapılandırılmamış", func(t *testing.T) {
		cc := New().FetchOperationCard(context.Background(), opCardTestReq())
		if cc.Outcome != CodeUnconfigured || cc.SearchOff {
			t.Errorf("%+v", cc)
		}
		var nilSvc *Service
		if nilSvc.FetchOperationCard(context.Background(), opCardTestReq()).Outcome != CodeUnconfigured {
			t.Error("nil servis")
		}
	})
	t.Run("depo yok", func(t *testing.T) {
		f, svc := opCardFixtureTFS(t, true)
		r := opCardTestReq()
		r.Repo = ""
		if cc := svc.FetchOperationCard(context.Background(), r); cc.Outcome != CodeRepoUnresolved || f.countReqs("") != 0 {
			t.Errorf("%+v", cc)
		}
	})
	t.Run("rota bulunamadı → tree-miss, ≤2 aday dosya", func(t *testing.T) {
		f, svc := opCardFixtureTFS(t, true)
		r := opCardTestReq()
		r.Route = "/orders/{id}/approve/extra"
		f.searchFor = func(string) []CodeSearchHit {
			return []CodeSearchHit{{Repository: "orders-api", Path: ocCtrl}, {Repository: "orders-api", Path: ocImpl}, {Repository: "orders-api", Path: ocReq}}
		}
		cc := svc.FetchOperationCard(context.Background(), r)
		f.mu.Lock()
		items := f.hits["item"]
		f.mu.Unlock()
		if cc.Outcome != CodeTreeMiss || cc.Entry != nil || items > opCardEntryTries || !strings.Contains(cc.Reason, "giriş metodu bulunamadı") {
			t.Errorf("outcome=%q items=%d reason=%q", cc.Outcome, items, cc.Reason)
		}
	})
	t.Run("RPC biçimsiz span adı → istek yok", func(t *testing.T) {
		f, svc := opCardFixtureTFS(t, true)
		r := opCardTestReq()
		r.Entry, r.Route = "rpc", "order-events process"
		cc := svc.FetchOperationCard(context.Background(), r)
		f.mu.Lock()
		search := f.hits["search"]
		f.mu.Unlock()
		if search != 0 || !strings.Contains(cc.Reason, "RPC girişi") {
			t.Errorf("search=%d reason=%q", search, cc.Reason)
		}
	})
}

func TestRPCEntryParts(t *testing.T) {
	cases := []struct {
		in, svc, m string
		ok         bool
	}{
		{"com.example.risk.RiskService/Score", "RiskService", "Score", true},
		{"RiskService/Score", "RiskService", "Score", true},
		{"order-events process", "", "", false},
		{"/Score", "", "", false},
		{"a.B/c d", "", "", false},
	}
	for _, c := range cases {
		s, m, ok := RPCEntryParts(c.in)
		if s != c.svc || m != c.m || ok != c.ok {
			t.Errorf("%q → %q %q %v", c.in, s, m, ok)
		}
	}
}

func TestOperationCardCacheKeyHashesAllInputs(t *testing.T) {
	cfg := Settings{BaseURL: "https://tfs.example.test", Collection: "C", Project: "P"}
	src := RefSpec{Kind: "branch", Name: "release"}
	req := OperationCardRequest{Entry: "http", Operation: "ORDER_APPROVE", Method: "POST", Route: "/orders/{id}"}
	k0 := operationCardCacheKey(cfg, "orders-api", src, req, 9000)
	muts := map[string]func(c *Settings, repo *string, s *RefSpec, r *OperationCardRequest, b *int){
		"base": func(c *Settings, _ *string, _ *RefSpec, _ *OperationCardRequest, _ *int) { c.BaseURL += "x" },
		"coll": func(c *Settings, _ *string, _ *RefSpec, _ *OperationCardRequest, _ *int) { c.Collection = "D" },
		"proj": func(c *Settings, _ *string, _ *RefSpec, _ *OperationCardRequest, _ *int) { c.Project = "Q" },
		"repo": func(_ *Settings, r *string, _ *RefSpec, _ *OperationCardRequest, _ *int) { *r = "payments-api" },
		"ref": func(_ *Settings, _ *string, s *RefSpec, _ *OperationCardRequest, _ *int) {
			*s = RefSpec{Kind: "commit", Name: "c0ffee"}
		},
		"entry":  func(_ *Settings, _ *string, _ *RefSpec, r *OperationCardRequest, _ *int) { r.Entry = "rpc" },
		"op":     func(_ *Settings, _ *string, _ *RefSpec, r *OperationCardRequest, _ *int) { r.Operation = "" },
		"method": func(_ *Settings, _ *string, _ *RefSpec, r *OperationCardRequest, _ *int) { r.Method = "GET" },
		"route":  func(_ *Settings, _ *string, _ *RefSpec, r *OperationCardRequest, _ *int) { r.Route = "/orders" },
		"budget": func(_ *Settings, _ *string, _ *RefSpec, _ *OperationCardRequest, b *int) { *b = 6000 },
		"ayraç": func(_ *Settings, _ *string, _ *RefSpec, r *OperationCardRequest, _ *int) {
			r.Method, r.Route = `POST|r="/orders/{id}`, ""
		},
		"repo↔pr": func(c *Settings, r *string, _ *RefSpec, _ *OperationCardRequest, _ *int) {
			c.Project, *r = "orders-api", "P"
		},
	}
	seen := map[string]string{k0: "base"}
	for name, m := range muts {
		c, repo, s, r, b := cfg, "orders-api", src, req, 9000
		m(&c, &repo, &s, &r, &b)
		k := operationCardCacheKey(c, repo, s, r, b)
		if prev, dup := seen[k]; dup {
			t.Errorf("%s anahtarı %s ile çakıştı: %s", name, prev, k)
		}
		seen[k] = name
	}
	// Children anahtarda değil (eşleme önbellek dışında).
	r := req
	r.Children = []CardChildHint{{Kind: "db"}}
	if operationCardCacheKey(cfg, "orders-api", src, r, 9000) != k0 {
		t.Error("Children anahtarı değiştirmemeli")
	}
}

// v0.10.1509 — geçici DevOps hatası (arama 500) önbelleğe yazılmaz: sonraki
// istek yeniden dener.
func TestFetchOperationCard_TransientErrorNotCached(t *testing.T) {
	f, svc := opCardFixtureTFS(t, true)
	fail := true
	inner := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail && strings.HasSuffix(r.URL.Path, "/_apis/search/codesearchresults") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		inner.ServeHTTP(w, r)
	})
	cc := svc.FetchOperationCard(context.Background(), opCardTestReq())
	if cc.Entry != nil || !strings.Contains(cc.Reason, "rota araması başarısız") {
		t.Fatalf("ilk istek: %+v", cc)
	}
	fail = false
	cc2 := svc.FetchOperationCard(context.Background(), opCardTestReq())
	if cc2.Entry == nil {
		t.Fatalf("geçici hata önbellekten döndü: %q", cc2.Reason)
	}
}
