// topology_focus_test.go — v0.9.366 regression. The neighborhood scope
// read the whole estate's top-20000-by-calls edges and hop-walked in Go:
// past 20k estate edges the focused service's own quiet dependencies fell
// out of the LIMIT window and silently vanished from its Topology tab.
// The focus walk (ReadServiceTopologyAggForFocus) hops with IN-filtered MV
// reads instead; topologyNextFrontier is its pure hop step.
package chstore

import (
	"reflect"
	"testing"
)

func edge(parent, child string) ServiceTopologyEdge {
	return ServiceTopologyEdge{ParentService: parent, ChildNode: child}
}

func TestTopologyNextFrontier(t *testing.T) {
	cases := []struct {
		name  string
		edges []ServiceTopologyEdge
		seen  map[string]bool
		cap   int
		want  []string
	}{
		{
			"collects unseen endpoints busiest-first",
			[]ServiceTopologyEdge{edge("gw", "orders"), edge("gw", "billing")},
			map[string]bool{"gw": true},
			10,
			[]string{"orders", "billing"},
		},
		{
			"seen nodes are skipped, not re-emitted",
			[]ServiceTopologyEdge{edge("gw", "orders"), edge("orders", "gw")},
			map[string]bool{"gw": true, "orders": true},
			10,
			nil,
		},
		{
			"ext: endpoints contribute both spellings for the api ext-merge",
			[]ServiceTopologyEdge{edge("gw", "ext:orders")},
			map[string]bool{"gw": true},
			10,
			[]string{"ext:orders", "orders"},
		},
		{
			// v0.10.1260 — operatör raporu 2026-10-17: k8s DNS adı da
			// gerçek servis adayını sınıra koyar, 2. hop onun kenarlarını okur.
			"ext: k8s DNS endpoint contributes the normalized service name",
			[]ServiceTopologyEdge{edge("gw", "ext:orders.shop.svc.cluster.local:50051")},
			map[string]bool{"gw": true},
			10,
			[]string{"ext:orders.shop.svc.cluster.local:50051", "orders.shop.svc.cluster.local:50051", "orders.shop.svc.cluster.local", "orders"},
		},
		{
			"ext: public host is not shortened",
			[]ServiceTopologyEdge{edge("gw", "ext:api.stripe.com")},
			map[string]bool{"gw": true},
			10,
			[]string{"ext:api.stripe.com", "api.stripe.com"},
		},
		{
			"cap truncates to the busiest (edge order = calls DESC)",
			[]ServiceTopologyEdge{edge("gw", "a"), edge("gw", "b"), edge("gw", "c")},
			map[string]bool{"gw": true},
			2,
			[]string{"a", "b"},
		},
		{
			"empty endpoint never enters the frontier",
			[]ServiceTopologyEdge{edge("", "orders")},
			map[string]bool{},
			10,
			[]string{"orders"},
		},
	}
	for _, c := range cases {
		got := topologyNextFrontier(c.edges, c.seen, c.cap)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
		for _, n := range got {
			if !c.seen[n] {
				t.Errorf("%s: %q returned but not marked seen", c.name, n)
			}
		}
	}
}
