package devops

// operation_card_parse_test.go — v0.10.1506 (plan-operation-card §7 R4):
// kod yarısının saf ayrıştırıcıları, testdata/opcard/ Java fixture'larıyla.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func opcardFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "opcard", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRouteSegmentsAndSearchSegment(t *testing.T) {
	cases := []struct{ route, norm, seg string }{
		{"/orders/{id}/approve", "/orders/{}/approve", "approve"},
		{"/orders/:id/approve", "/orders/{}/approve", "approve"},
		{"/api/v1/credit-products/{productId:\\d+}", "/api/v1/credit-products/{}", "credit-products"},
		{"/api/v1/{id}", "/api/v1/{}", ""},
		{"/payments/approve?x=1", "/payments/approve", "payments"},
		{"", "/", ""},
	}
	for _, c := range cases {
		if got := NormRoute(c.route); got != c.norm {
			t.Errorf("NormRoute(%q)=%q, want %q", c.route, got, c.norm)
		}
		if got := RouteSearchSegment(c.route); got != c.seg {
			t.Errorf("RouteSearchSegment(%q)=%q, want %q", c.route, got, c.seg)
		}
	}
}

func TestRouteMethodWindow(t *testing.T) {
	ctrl := opcardFixture(t, "OrderController.java")
	jax := opcardFixture(t, "AccountResource.java")
	cases := []struct {
		name, src, method, route string
		wantDecl                 string // imzada geçmeli; "" = bulunmamalı
	}{
		{"spring POST + sınıf öneki + {id}", ctrl, "POST", "/orders/{id}/approve", "approve("},
		{"aynı rota GET ayrı metot", ctrl, "GET", "/orders/{id}/approve", "approvalStatus("},
		{":id biçimi", ctrl, "POST", "/orders/:id/approve", "approve("},
		{"yöntem bilinmiyor + iki aday = belirsiz", ctrl, "", "/orders/{id}/approve", ""},
		{"bağlam yolu öneki (sonek eşleşmesi)", ctrl, "GET", "/api/orders/{id}", "get("},
		{"yanlış rota", ctrl, "POST", "/orders/{id}/reject", ""},
		{"JAX-RS @Path + @GET", jax, "GET", "/accounts/{accountId}/balance", "balance("},
		{"JAX-RS @POST yolsuz metot", jax, "POST", "/accounts", "open("},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mw, ok := RouteMethodWindow(c.src, c.method, c.route, 60)
			if c.wantDecl == "" {
				if ok {
					t.Fatalf("bulunmamalıydı: %q", mw.Header)
				}
				return
			}
			if !ok || !strings.Contains(mw.Header, c.wantDecl) {
				t.Fatalf("ok=%v header=%q", ok, mw.Header)
			}
			if mw.BodyLine < mw.Window.FromLine || mw.BodyLine > mw.Window.ToLine {
				t.Errorf("BodyLine %d pencere [%d,%d] dışında", mw.BodyLine, mw.Window.FromLine, mw.Window.ToLine)
			}
		})
	}
}

func TestMethodSignatureTypes(t *testing.T) {
	ctrl := opcardFixture(t, "OrderController.java")
	mw, ok := RouteMethodWindow(ctrl, "POST", "/orders/{id}/approve", 60)
	if !ok {
		t.Fatal("approve bulunamadı")
	}
	sig := MethodSignature(mw.Header)
	if strings.Contains(sig, "\n") || strings.Contains(sig, "MARKER_42") || !strings.HasPrefix(sig, "public ResponseEntity<ApproveResponse> approve(") {
		t.Fatalf("imza: %q", sig)
	}
	cases := []struct {
		sig       string
		ret, name string
		params    []TypedParam
	}{
		{sig, "ResponseEntity<ApproveResponse>", "approve", []TypedParam{
			{Name: "id", Type: "long", Annotations: []string{"PathVariable"}},
			{Name: "req", Type: "ApproveRequest", Annotations: []string{"Valid", "RequestBody"}},
		}},
		{"public Mono<List<OrderView>> list(final @RequestParam(name = \"q\", required = false) String q, Map<String, List<Long>> m)", "Mono<List<OrderView>>", "list", []TypedParam{
			{Name: "q", Type: "String", Annotations: []string{"RequestParam"}},
			{Name: "m", Type: "Map<String,List<Long>>"},
		}},
		{"void ping()", "void", "ping", nil},
		{"public static <T> T id(T... xs)", "T", "id", []TypedParam{{Name: "xs", Type: "T..."}}},
	}
	for _, c := range cases {
		ps, ret, name, ok := MethodSignatureTypes(c.sig)
		if !ok || ret != c.ret || name != c.name {
			t.Errorf("%q: ok=%v ret=%q name=%q", c.sig, ok, ret, name)
			continue
		}
		if len(ps) != len(c.params) {
			t.Errorf("%q: params %+v", c.sig, ps)
			continue
		}
		for i := range ps {
			if ps[i].Name != c.params[i].Name || ps[i].Type != c.params[i].Type || !reflect.DeepEqual(ps[i].Annotations, c.params[i].Annotations) {
				t.Errorf("%q param %d: %+v want %+v", c.sig, i, ps[i], c.params[i])
			}
		}
	}
	ps, ret, _, _ := MethodSignatureTypes(sig)
	req, resp := RequestResponseTypes(ps, ret)
	if !reflect.DeepEqual(req, []string{"ApproveRequest"}) || !reflect.DeepEqual(resp, []string{"ApproveResponse"}) {
		t.Errorf("req=%v resp=%v", req, resp)
	}
	if _, _, _, ok := MethodSignatureTypes("no parens here"); ok {
		t.Error("parantezsiz imza tanınmamalı")
	}
}

func TestAppTypes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"ResponseEntity<ApproveResponse>", []string{"ApproveResponse"}},
		{"Mono<List<OrderView>>", []string{"OrderView"}},
		{"Optional<com.example.Order>", []string{"Order"}},
		{"List<String>", nil},
		{"Map<String, OrderView>", nil},
		{"String", nil},
		{"long", nil},
		{"T", nil},
		{"OrderLine[]", []string{"OrderLine"}},
		{"List<? extends Item>", []string{"Item"}},
		{"HttpServletRequest", nil},
		{"CompletableFuture<ResponseEntity<AuthView>>", []string{"AuthView"}},
	}
	for _, c := range cases {
		if got := AppTypes(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("AppTypes(%q)=%v want %v", c.in, got, c.want)
		}
	}
}

func TestDTOFields(t *testing.T) {
	req := opcardFixture(t, "ApproveRequest.java")
	got, ok := DTOFields(req, "ApproveRequest")
	want := []DTOField{
		{Owner: "ApproveRequest", Name: "amount", Type: "BigDecimal"},
		{Owner: "ApproveRequest", Name: "channel", Type: "ChannelInfo", Nested: true},
		{Owner: "ApproveRequest", Name: "tags", Type: "List<String>"},
		{Owner: "ApproveRequest", Name: "valueDate", Type: "LocalDate"},
	}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("ApproveRequest:\n got %+v\nwant %+v", got, want)
	}
	// İç içe sınıf aynı dosyada: kendi alanı.
	if in, ok := DTOFields(req, "Inner"); !ok || len(in) != 1 || in[0].Name != "notMine" {
		t.Errorf("Inner: %+v %v", in, ok)
	}
	rec, ok := DTOFields(opcardFixture(t, "ChannelInfo.java"), "ChannelInfo")
	wantRec := []DTOField{
		{Owner: "ChannelInfo", Name: "code", Type: "String"},
		{Owner: "ChannelInfo", Name: "name", Type: "String"},
		{Owner: "ChannelInfo", Name: "parent", Type: "ChannelInfo"}, // kendine özyineleme Nested değil
	}
	if !ok || !reflect.DeepEqual(rec, wantRec) {
		t.Errorf("record:\n got %+v\nwant %+v", rec, wantRec)
	}
	resp, ok := DTOFields(opcardFixture(t, "ApproveResponse.java"), "ApproveResponse")
	if !ok || len(resp) != 2 || resp[0].Name != "status" || resp[0].Type != "ApprovalStatus" || !resp[0].Nested || resp[1].Name != "message" {
		t.Errorf("response: %+v", resp)
	}
	if _, ok := DTOFields(req, "Missing"); ok {
		t.Error("tanımsız tip ok=false olmalı")
	}
	if e, ok := DTOFields("public enum ApprovalStatus { OK, DENIED }", "ApprovalStatus"); !ok || len(e) != 0 {
		t.Errorf("enum: %+v %v", e, ok)
	}
	// DEĞER yok: ilklendirici sabitleri hiçbir alana sızmaz.
	for _, f := range append(append(got, rec...), resp...) {
		if strings.Contains(f.Name+f.Type, "MARKER") {
			t.Errorf("değer sızdı: %+v", f)
		}
	}
}

func TestManagerCallees(t *testing.T) {
	body := `orderService.approve(id, req); this.paymentsClient.authorize(x); orderService.other(); // yorumService.x()
	String s = "fakeService.call()"; orderRepository.findById(1);`
	got := ManagerCallees(body, 0)
	want := []Callee{{"orderService", "approve"}, {"paymentsClient", "authorize"}, {"orderRepository", "findById"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	if len(ManagerCallees(body, 2)) != 2 {
		t.Error("max uygulanmadı")
	}
	// ManagerCallee (v0.10.1330) korunur.
	if r, m, ok := ManagerCallee(body); !ok || r != "orderService" || m != "approve" {
		t.Errorf("ManagerCallee: %s %s %v", r, m, ok)
	}
}

func TestClassifyCallSites(t *testing.T) {
	impl := opcardFixture(t, "OrderServiceImpl.java")
	mw, ok := CalleeMethodWindow(impl, "approve", 60)
	if !ok {
		t.Fatal("approve gövdesi yok")
	}
	sites := ClassifyCallSites(mw, "/src/OrderServiceImpl.java")
	type row struct {
		Line                 int
		Kind, Callee, Target string
	}
	var got []row
	for _, s := range sites {
		got = append(got, row{s.Line, s.Kind, s.Callee, s.Target})
		if s.MatchedChild != -1 {
			t.Errorf("eşleme öncesi -1 olmalı: %+v", s)
		}
	}
	want := []row{
		{14, "db", "orderRepository.findById", ""},
		{15, "http", "paymentsClient.authorize", ""},
		{16, "db", "jdbcTemplate.update", ""}, // SQL sabiti ASLA okunmaz
		{17, "messaging", "kafkaTemplate.send", "order-events"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	extra := []struct {
		body, kind, target string
	}{
		{`restTemplate.postForObject(baseUrl + "/payments/authorize", req, X.class);`, "http", "/payments/authorize"},
		{`webClient.post().uri("http://payments-api/payments/authorize?x=1").retrieve();`, "http", "/payments/authorize"},
		{`restTemplate.getForObject(url, X.class);`, "http", ""},
		{`restTemplate.getForObject("${svc.url}", X.class);`, "http", ""},
		{`orderStub.approve(r);`, "grpc", ""},
		{`jmsTemplate.convertAndSend("queue.orders", m);`, "messaging", "queue.orders"},
		{`eventPublisher.publishEvent(e);`, "", ""},
		{`entityManager.createNativeQuery("select * from t");`, "db", ""},
		{`helper.compute(x);`, "", ""},
	}
	for _, c := range extra {
		ss := ClassifyCallSites(MethodWindow{Body: c.body, BodyLine: 5}, "/p")
		if c.kind == "" {
			if len(ss) != 0 {
				t.Errorf("%q: çağrı yeri olmamalı: %+v", c.body, ss)
			}
			continue
		}
		if len(ss) != 1 || ss[0].Kind != c.kind || ss[0].Target != c.target || ss[0].Line != 5 {
			t.Errorf("%q: %+v", c.body, ss)
		}
	}
}

func TestFeignTarget(t *testing.T) {
	src := opcardFixture(t, "PaymentsClient.java")
	name, path, ok := FeignTarget(src, "authorize")
	if !ok || name != "payments-api" || path != "/payments/authorize" {
		t.Errorf("authorize: %q %q %v", name, path, ok)
	}
	if _, p, _ := FeignTarget(src, "get"); p != "/payments/{id}" {
		t.Errorf("get: %q", p)
	}
	if _, _, ok := FeignTarget(opcardFixture(t, "OrderServiceImpl.java"), "approve"); ok {
		t.Error("Feign olmayan sınıf ok=false")
	}
	if n, _, _ := FeignTarget(`@FeignClient("${x.name}") interface A {}`, "m"); n != "" {
		t.Errorf("yer tutucu ad alınmamalı: %q", n)
	}
}

// Çağrı yeri ↔ trace çocuğu: tablo (plan §7 R4 "eşleşme/-1/belirsizlik").
func TestMatchCallSitesToChildren(t *testing.T) {
	children := []CardChildHint{
		{ParentKey: "entry", Kind: "service", Protocol: "http", Target: "payments-api", Op: "POST /payments/authorize"},                                       // 0
		{ParentKey: "entry", Kind: "db", Protocol: "db", Target: "db:oracle", Op: "SELECT orders"},                                                            // 1
		{ParentKey: "entry", Kind: "messaging", Protocol: "kafka", Target: "queue:kafka:order-events", Op: "order-events publish"},                            // 2
		{ParentKey: "svc:payments-api|POST /payments/authorize", Kind: "external", Protocol: "http", Target: "ext:bank-gw.example.test", Op: "POST /v1/auth"}, // 3 derin
		{ParentKey: "entry", Kind: "service", Protocol: "grpc", Target: "risk-api", Op: "risk.RiskService/Score"},                                             // 4
	}
	site := func(kind, callee, target, svc string) CallSite {
		return CallSite{Path: "/a.java", Kind: kind, Callee: callee, Target: target, Service: svc, MatchedChild: 7}
	}
	cases := []struct {
		name     string
		sites    []CallSite
		children []CardChildHint
		want     []int
	}{
		{"literal rota", []CallSite{site("http", "rest.post", "/payments/authorize", "")}, children, []int{0}},
		{"Feign adı", []CallSite{site("http", "paymentsClient.authorize", "", "payments-api")}, children, []int{0}},
		{"topic", []CallSite{site("messaging", "kafkaTemplate.send", "order-events", "")}, children, []int{2}},
		{"grpc metot adı", []CallSite{site("grpc", "riskStub.score", "", "")}, children, []int{4}},
		{"tek db çocuğu + tek repository", []CallSite{site("db", "orderRepository.findById", "", "")}, children, []int{1}},
		{"iki db çağrısı tek çocuk → belirsiz", []CallSite{site("db", "a.x", "", ""), site("db", "b.y", "", "")}, children, []int{-1, -1}},
		{"derin satır (başka servisin kodu) eşlenmez", []CallSite{site("http", "c.x", "/v1/auth", "")}, children[3:4], []int{-1}},
		{"eşleşmeyen rota", []CallSite{site("http", "c.x", "/other", "")}, []CardChildHint{children[0], {ParentKey: "entry", Kind: "external", Protocol: "http", Target: "ext:x", Op: "GET /y"}}, []int{-1}},
		{"eşit kanıtlı iki çocuk → -1", []CallSite{site("http", "c.x", "", "payments-api")}, []CardChildHint{children[0], {ParentKey: "entry", Kind: "service", Protocol: "http", Target: "payments-api", Op: "GET /payments/{id}"}}, []int{-1}},
		{"çocuk yok", []CallSite{site("db", "r.x", "", "")}, nil, []int{-1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MatchCallSitesToChildren(c.sites, c.children)
			if len(got) != len(c.want) {
				t.Fatalf("%+v", got)
			}
			for i := range got {
				if got[i].MatchedChild != c.want[i] {
					t.Errorf("site %d (%s) → %d, want %d", i, got[i].Callee, got[i].MatchedChild, c.want[i])
				}
			}
		})
	}
	// Çıktı sırası dosya:satır, girdi değişmez.
	in := []CallSite{{Path: "/b", Line: 1, Kind: "db"}, {Path: "/a", Line: 9, Kind: "db"}, {Path: "/a", Line: 2, Kind: "db"}}
	got := MatchCallSitesToChildren(in, nil)
	if got[0].Path != "/a" || got[0].Line != 2 || got[2].Path != "/b" || in[0].Path != "/b" {
		t.Errorf("sıra: %+v girdi: %+v", got, in)
	}
}
