package api

// v0.10.1443 — sohbetten dashboard (R1): sunucu doğrulaması, tekilleştirme,
// tavanlar ve ad çakışması — sahte dashboardStore ile tablo testleri.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

type fakeDashStore struct {
	rows    map[string]chstore.Dashboard
	upserts []chstore.Dashboard
}

func newFakeDashStore(ds ...chstore.Dashboard) *fakeDashStore {
	f := &fakeDashStore{rows: map[string]chstore.Dashboard{}}
	for _, d := range ds {
		f.rows[d.ID] = d
	}
	return f
}

func (f *fakeDashStore) ListDashboards(context.Context) ([]chstore.Dashboard, error) {
	out := make([]chstore.Dashboard, 0, len(f.rows))
	for _, d := range f.rows {
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeDashStore) GetDashboard(_ context.Context, id string) (*chstore.Dashboard, error) {
	d, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	return &d, nil
}

func (f *fakeDashStore) UpsertDashboard(_ context.Context, d chstore.Dashboard) error {
	f.rows[d.ID] = d
	f.upserts = append(f.upserts, d)
	return nil
}

func seqID() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("gen%d", n) }
}

func promqlPanel(id, title, query, source string) json.RawMessage {
	cfg := map[string]any{"query": query, "unit": "req/s", "viz": "line"}
	if source != "" {
		cfg["source"] = source
	}
	b, _ := json.Marshal(map[string]any{"id": id, "type": "promql", "title": title, "width": 2, "config": cfg})
	return b
}

func spanPanel(id, title, svc string) json.RawMessage {
	filters, _ := json.Marshal([]map[string]any{{"k": "service.name", "op": "=", "v": []string{svc}}})
	b, _ := json.Marshal(map[string]any{"id": id, "type": "spanmetric", "title": title, "width": 2,
		"config": map[string]any{"agg": "p99", "filters": string(filters), "unit": "ms"}})
	return b
}

func TestDashValidatePromQL(t *testing.T) {
	cases := []struct {
		name, query, source string
		ok                  bool
	}{
		{"plain promql", `sum(rate(http_server_requests_total{service="orders-api"}[5m]))`, "vm", true},
		{"plain promql no source", `sum(rate(http_server_requests_total[5m]))`, "", true},
		{"plain promql ch", `sum(rate(http_server_requests_total[5m]))`, "ch", true},
		{"metricsql vm", `rollup_rate(http_server_requests_total[5m])`, "vm", true},
		{"metricsql without source", `rollup_rate(http_server_requests_total[5m])`, "", false},
		{"metricsql ch", `rollup_rate(http_server_requests_total[5m])`, "ch", false},
		{"subquery", `max_over_time(rate(http_server_requests_total[5m])[1h:1m])`, "vm", false},
		{"label_replace", `label_replace(up, "a", "$1", "b", "(.*)")`, "vm", false},
		{"empty", ``, "vm", false},
		{"unknown source", `sum(rate(x_total[5m]))`, "thanos", false},
	}
	for _, c := range cases {
		err := dashValidatePromQL(c.query, c.source)
		if (err == nil) != c.ok {
			t.Errorf("%s: ok=%v want %v (err=%v)", c.name, err == nil, c.ok, err)
		}
	}
}

func TestDashAppendPanels(t *testing.T) {
	q := `sum(rate(http_server_requests_total{service="orders-api"}[5m]))`
	existingPanels, _ := json.Marshal([]json.RawMessage{promqlPanel("p1", "Throughput", q, "vm")})
	base := chstore.Dashboard{ID: "d1", Name: "Orders", Description: "keep me", Panels: existingPanels,
		Variables: json.RawMessage(`[{"name":"service","type":"service"}]`), Tags: json.RawMessage(`["prod"]`), CreatedAt: 7}

	t.Run("dedupe against existing + within batch, carries fields", func(t *testing.T) {
		st := newFakeDashStore(base)
		req := dashPanelsReq{Panels: []json.RawMessage{
			promqlPanel("x", "Throughput again", "sum(rate(http_server_requests_total{service=\"orders-api\"}[5m]))   ", "vm"), // whitespace-normalised dup
			spanPanel("p1", "p99", "orders-api"), // id collision → new id
			spanPanel("y", "p99 dup", "orders-api"),
			promqlPanel("z", "bad", `label_replace(up, "a", "$1", "b", "(.*)")`, "vm"),
		}}
		req.Origin = &dashPanelsOrigin{Conversation: "c1"}
		res, err := dashAppendPanels(context.Background(), st, "d1", req, seqID())
		if err != nil {
			t.Fatal(err)
		}
		if res.Added != 1 || len(res.Skipped) != 3 {
			t.Fatalf("added=%d skipped=%+v", res.Added, res.Skipped)
		}
		reasons := []string{res.Skipped[0].Reason, res.Skipped[1].Reason, res.Skipped[2].Reason}
		if strings.Join(reasons, ",") != "duplicate,duplicate,invalid" {
			t.Errorf("reasons = %v", reasons)
		}
		if res.Href != "/dashboard?id=d1" || res.Name != "Orders" {
			t.Errorf("res = %+v", res)
		}
		got := st.rows["d1"]
		if got.Description != "keep me" || string(got.Variables) != string(base.Variables) || string(got.Tags) != `["prod"]` || got.CreatedAt != 7 {
			t.Errorf("fields not carried: %+v", got)
		}
		var panels []map[string]any
		_ = json.Unmarshal(got.Panels, &panels)
		if len(panels) != 2 || panels[1]["id"] != "gen1" {
			t.Errorf("panels = %v", panels)
		}
	})

	t.Run("explore pin (no origin) skips chat allowlist", func(t *testing.T) {
		st := newFakeDashStore(base)
		req := dashPanelsReq{Panels: []json.RawMessage{
			promqlPanel("z", "relabel", `label_replace(up, "a", "$1", "b", "(.*)")`, ""),
			promqlPanel("e", "empty", "  ", ""),
		}}
		res, err := dashAppendPanels(context.Background(), st, "d1", req, seqID())
		if err != nil || res.Added != 1 || len(res.Skipped) != 1 || res.Skipped[0].Reason != dashSkipInvalid {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	})

	t.Run("nothing new → no write", func(t *testing.T) {
		st := newFakeDashStore(base)
		res, err := dashAppendPanels(context.Background(), st, "d1", dashPanelsReq{Panels: []json.RawMessage{promqlPanel("x", "dup", q, "vm")}}, seqID())
		if err != nil || res.Added != 0 || len(st.upserts) != 0 {
			t.Fatalf("res=%+v err=%v upserts=%d", res, err, len(st.upserts))
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := dashAppendPanels(context.Background(), newFakeDashStore(), "nope", dashPanelsReq{Panels: []json.RawMessage{spanPanel("a", "a", "s")}}, seqID())
		if pe, ok := err.(*dashPanelErr); !ok || pe.code != http.StatusNotFound {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("panel cap", func(t *testing.T) {
		var many []json.RawMessage
		for i := 0; i < dashPanelsMax-1; i++ {
			many = append(many, spanPanel(fmt.Sprintf("e%d", i), "x", fmt.Sprintf("svc-%d", i)))
		}
		raw, _ := json.Marshal(many)
		st := newFakeDashStore(chstore.Dashboard{ID: "full", Name: "Full", Panels: raw})
		res, err := dashAppendPanels(context.Background(), st, "full", dashPanelsReq{Panels: []json.RawMessage{
			spanPanel("n1", "fits", "new-1"), spanPanel("n2", "over", "new-2"),
		}}, seqID())
		if err != nil || res.Added != 1 || len(res.Skipped) != 1 || res.Skipped[0].Reason != dashSkipCap {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	})

	t.Run("size cap", func(t *testing.T) {
		big := strings.Repeat("a", 300<<10)
		md := func(id string) json.RawMessage {
			b, _ := json.Marshal(map[string]any{"id": id, "type": "markdown", "title": id, "config": map[string]any{"text": big + id}})
			return b
		}
		st := newFakeDashStore(chstore.Dashboard{ID: "d", Name: "D"})
		res, err := dashAppendPanels(context.Background(), st, "d", dashPanelsReq{Panels: []json.RawMessage{md("a"), md("b")}}, seqID())
		if err != nil || res.Added != 1 || res.Skipped[0].Reason != dashSkipSize {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	})
}

func TestDashCreateFromPanels(t *testing.T) {
	st := newFakeDashStore(
		chstore.Dashboard{ID: "a", Name: "orders-api latency"},
		chstore.Dashboard{ID: "b", Name: "Orders-API latency (2)"},
	)
	long := strings.Repeat("ş", 200)
	req := dashPanelsReq{Panels: []json.RawMessage{spanPanel("", long, "orders-api")}}
	req.Dashboard = &struct {
		Name string `json:"name"`
	}{Name: "  orders-api   latency "}
	res, err := dashCreateFromPanels(context.Background(), st, req, seqID())
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "orders-api latency (3)" || res.ID != "gen2" || res.Added != 1 {
		t.Fatalf("res = %+v", res)
	}
	d := st.rows["gen2"]
	if string(d.Tags) != `["cosre"]` {
		t.Errorf("tags = %s", d.Tags)
	}
	var panels []map[string]any
	_ = json.Unmarshal(d.Panels, &panels)
	if title, _ := panels[0]["title"].(string); len([]rune(title)) != dashPanelTitleMax || panels[0]["id"] != "gen1" {
		t.Errorf("panel = %v (title runes %d)", panels[0], len([]rune(title)))
	}

	// Geçerli panel yok → pano kurulmaz (boş pano yazmak yok).
	_, err = dashCreateFromPanels(context.Background(), st, dashPanelsReq{Panels: []json.RawMessage{promqlPanel("x", "bad", "", "vm")}}, seqID())
	if pe, ok := err.(*dashPanelErr); !ok || pe.code != http.StatusBadRequest {
		t.Fatalf("err = %v", err)
	}
	// Ad verilmezse varsayılan.
	res, _ = dashCreateFromPanels(context.Background(), st, dashPanelsReq{Panels: []json.RawMessage{spanPanel("", "t", "x")}}, seqID())
	if res.Name != dashChatDefaultName {
		t.Errorf("default name = %q", res.Name)
	}
}

func TestDashPanelInspectRejectsUnknownType(t *testing.T) {
	for _, raw := range []string{`{"type":"iframe","title":"x","config":{}}`, `[]`, `{"type":"spanmetric","title":"x","config":{}}`, `{"type":"promql","title":"x"}`} {
		if _, _, _, err := dashPanelInspect(json.RawMessage(raw), false); err == nil {
			t.Errorf("%s accepted", raw)
		}
	}
}

// Boş origin nesnesi de sohbet kökenidir (kalıcılaşmamış yeni konuşma) —
// izin listesi yalnız gövdede origin HİÇ yokken (Explore) gevşer.
func TestDashPanelsReqFromChat(t *testing.T) {
	for body, want := range map[string]bool{
		`{"panels":[]}`:                               false,
		`{"panels":[],"origin":{}}`:                   true,
		`{"panels":[],"origin":{"conversation":"c"}}`: true,
	} {
		var r dashPanelsReq
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatal(err)
		}
		if got := r.fromChat(); got != want {
			t.Errorf("%s fromChat=%v want %v", body, got, want)
		}
	}
}
