package devops

// method_usages_test.go — v0.10.1509: "Bu metodu kim kullanıyor?" (ref:).

import (
	"context"
	"strings"
	"testing"
)

func TestMethodUsageSkipReason(t *testing.T) {
	for _, c := range []struct {
		m    string
		skip bool
	}{
		{"approve", false}, {"authorizePayment", false},
		{"get", true}, {"save", true}, {"Handle", true}, {"run", true}, {"abc", true},
		{"a b", true}, {"", true}, {"x\"y", true},
	} {
		if got := MethodUsageSkipReason(c.m) != ""; got != c.skip {
			t.Errorf("%q: skip=%v", c.m, got)
		}
	}
}

func TestParseUsageHitsDropsSnippet(t *testing.T) {
	raw := []byte(`{"results":[
	 {"path":"/src/A.java","repository":{"name":"web-bff","type":"git"},"project":{"name":"P"},"versions":[{"branchName":"refs/heads/main"}],
	  "matches":{"content":[{"line":0,"codeSnippet":"x"},{"line":42,"codeSnippet":"secret MARKER_42 code"}]}},
	 {"path":"$/tfvc/B.java","repository":{"name":"old","type":"tfvc"}}]}`)
	hits, err := parseUsageHits(raw)
	if err != nil || len(hits) != 1 {
		t.Fatalf("%+v %v", hits, err)
	}
	h := hits[0]
	if h.Repo != "web-bff" || h.Path != "/src/A.java" || h.Line != 42 || h.Branch != "main" || h.Project != "P" {
		t.Errorf("%+v", h)
	}
	if strings.Contains(strings.ToLower(strings.Join([]string{h.Repo, h.Path, h.Branch, h.Project}, "|")), "marker") {
		t.Error("parça kopyalandı")
	}
}

func TestRankMethodUsages(t *testing.T) {
	req := MethodUsageRequest{Method: "approve", Class: "OrderController", EntryRepo: "orders-api",
		EntryPath: "/src/web/OrderController.java", CallerRepos: []string{"web-bff"}}
	hits := []usageHit{
		{Repo: "orders-api", Path: "/src/web/OrderController.java", Line: 30},     // giriş dosyası: elenir
		{Repo: "orders-api", Path: "/src/test/OrderControllerTest.java", Line: 9}, // test: elenir
		{Repo: "misc-batch", Path: "/src/Job.java", Line: 5},                      // başka depo: 1
		{Repo: "orders-api", Path: "/src/service/Other.java", Line: 7},            // aynı depo: 0
		{Repo: "web-bff", Path: "/src/Flow.java", Line: 3},                        // çağıran: 5
		{Repo: "mobile-gw", Path: "/src/OrdersClient.java", Line: 12},             // istemci: 3
		{Repo: "web-bff", Path: "/src/Flow.java", Line: 3},                        // tekrar
	}
	got := RankMethodUsages(hits, req)
	var order []string
	for _, u := range got {
		order = append(order, u.Repo+":"+u.Path)
	}
	want := "web-bff:/src/Flow.java,mobile-gw:/src/OrdersClient.java,misc-batch:/src/Job.java,orders-api:/src/service/Other.java"
	if strings.Join(order, ",") != want {
		t.Fatalf("sıra:\n got %s\nwant %s", strings.Join(order, ","), want)
	}
	if !got[0].Caller || got[0].SameRepo || !got[3].SameRepo {
		t.Errorf("bayraklar: %+v", got)
	}
}

func TestFetchMethodUsages(t *testing.T) {
	t.Run("ok + önbellek + bütçe", func(t *testing.T) {
		f, svc := operationFixture(t, true)
		var seen []string
		f.searchFor = func(text string) []CodeSearchHit {
			seen = append(seen, text)
			return []CodeSearchHit{
				{Project: "Orders", Repository: "web-bff", Path: "/src/Flow.java", Branch: "main"},
				{Project: "Orders", Repository: "orders-api", Path: "/src/OrderController.java", Branch: "release"},
			}
		}
		req := MethodUsageRequest{Method: "approve", EntryRepo: "orders-api", EntryPath: "/src/OrderController.java", CallerRepos: []string{"web-bff"}}
		u := svc.FetchMethodUsages(context.Background(), req)
		if u.Outcome != "ok" || len(u.Hits) != 1 || u.Hits[0].Repo != "web-bff" || !u.Hits[0].Caller || !strings.Contains(u.Hits[0].URL, "web-bff") {
			t.Fatalf("%+v", u)
		}
		if len(seen) != 1 || seen[0] != "ref:approve ext:java" {
			t.Fatalf("sorgu: %v", seen)
		}
		u2 := svc.FetchMethodUsages(context.Background(), MethodUsageRequest{Method: "approve"})
		if len(seen) != 1 || u2.Total != 2 {
			t.Errorf("önbellekten gelmeli (arama=%d) ve sıralama yeniden: %+v", len(seen), u2)
		}
	})
	t.Run("Code Search kapalı → 0 istek", func(t *testing.T) {
		f, svc := operationFixture(t, false)
		u := svc.FetchMethodUsages(context.Background(), MethodUsageRequest{Method: "approve"})
		if u.Outcome != "search-off" || f.countReqs("") != 0 || u.Hits == nil {
			t.Errorf("%+v", u)
		}
	})
	t.Run("genel ad → aranmaz", func(t *testing.T) {
		f, svc := operationFixture(t, true)
		u := svc.FetchMethodUsages(context.Background(), MethodUsageRequest{Method: "get"})
		if u.Outcome != "skipped" || f.countReqs("") != 0 {
			t.Errorf("%+v", u)
		}
	})
	t.Run("isabet yok", func(t *testing.T) {
		f, svc := operationFixture(t, true)
		f.searchFor = func(string) []CodeSearchHit { return nil }
		if u := svc.FetchMethodUsages(context.Background(), MethodUsageRequest{Method: "approveLoan"}); u.Outcome != "no_hits" || u.Reason == "" {
			t.Errorf("%+v", u)
		}
	})
}

func TestMethodUsageCacheKey(t *testing.T) {
	a := Settings{BaseURL: "https://a", Collection: "C"}
	k := methodUsageCacheKey(a, "ref:x ext:java")
	b := a
	b.Collection = "D"
	c := a
	c.BaseURL = "https://b"
	for _, o := range []string{methodUsageCacheKey(b, "ref:x ext:java"), methodUsageCacheKey(c, "ref:x ext:java"), methodUsageCacheKey(a, "ref:y ext:java")} {
		if o == k {
			t.Errorf("çakışma: %s", o)
		}
	}
}
