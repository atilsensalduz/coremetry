package api

// service_neighbors_test.go — v0.10.1408 regresyon testi.
//
// Operatör: "Downstream düzgün çalışmıyor" — Service → Overview → Bağlam
// paneli "12 upstream · 0 downstream" diyordu; aynı servis oracle'ı ~938
// kez çağırıyordu. Eski okuma (ham spans, en büyük 50 trace) downstream'i
// yalnız BAŞKA service_name'li çocuk span'lardan kuruyordu; DB / dış HTTP
// çağrıları servisin kendi CLIENT span'ı olduğu için hiç sayılmıyordu.
// Fixture: enstrümante callee'si SIFIR, ama db + dış HTTP CLIENT kenarı olan
// bir servis → downstream ikisini de göstermeli.

import (
	"reflect"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestServiceNeighborsFromEdges(t *testing.T) {
	type want struct {
		ID, Name, Kind, System string
		Calls                  uint64
	}
	flat := func(ns []serviceNeighbor) []want {
		out := []want{}
		for _, n := range ns {
			out = append(out, want{n.ID, n.Name, n.Kind, n.System, n.Calls})
		}
		return out
	}
	cases := []struct {
		name     string
		edges    []chstore.ServiceTopologyEdge
		known    []string
		wantUp   []want
		wantDown []want
	}{
		{
			name: "0 instrumented callees, db + external HTTP client spans → both downstream",
			edges: []chstore.ServiceTopologyEdge{
				{ParentService: "edge-gateway", ChildNode: "orders-api", NodeKind: "service", Protocol: "http", Calls: 400},
				{ParentService: "orders-api", ChildNode: "db:oracle@db.example.test", NodeKind: "db", Protocol: "db", Calls: 938, AvgMs: 4},
				{ParentService: "orders-api", ChildNode: "ext:payments.example.test", NodeKind: "external", Protocol: "http", Calls: 120},
			},
			wantUp: []want{{"edge-gateway", "edge-gateway", "service", "", 400}},
			wantDown: []want{
				{"db:oracle@db.example.test", "oracle@db.example.test", "database", "oracle", 938},
				{"ext:payments.example.test", "payments.example.test", "external", "", 120},
			},
		},
		{
			name: "queue producer + consumer: producer edge downstream, consumer edge upstream",
			edges: []chstore.ServiceTopologyEdge{
				{ParentService: "orders-api", ChildNode: "queue:kafka:orders.created", NodeKind: "queue", Protocol: "kafka", Calls: 50},
				{ParentService: "queue:kafka:orders.refund", ChildNode: "orders-api", NodeKind: "service", Protocol: "kafka", Calls: 7},
			},
			wantUp:   []want{{"queue:kafka:orders.refund", "kafka:orders.refund", "queue", "kafka", 7}},
			wantDown: []want{{"queue:kafka:orders.created", "kafka:orders.created", "queue", "kafka", 50}},
		},
		{
			name: "ext: peer that is a known service folds into the service node (Topology ext-merge)",
			edges: []chstore.ServiceTopologyEdge{
				{ParentService: "orders-api", ChildNode: "ext:inventory-api", NodeKind: "external", Protocol: "http", Calls: 30},
			},
			known:    []string{"orders-api", "inventory-api"},
			wantUp:   []want{},
			wantDown: []want{{"inventory-api", "inventory-api", "service", "", 30}},
		},
		{
			name: "same peer over two protocols folds into one neighbour; self-edge dropped",
			edges: []chstore.ServiceTopologyEdge{
				{ParentService: "orders-api", ChildNode: "billing-api", NodeKind: "service", Protocol: "http", Calls: 10, AvgMs: 2},
				{ParentService: "orders-api", ChildNode: "billing-api", NodeKind: "service", Protocol: "grpc", Calls: 30, AvgMs: 6},
				{ParentService: "orders-api", ChildNode: "orders-api", NodeKind: "service", Protocol: "internal", Calls: 99},
			},
			wantUp:   []want{},
			wantDown: []want{{"billing-api", "billing-api", "service", "", 40}},
		},
		{
			name:     "no edges → empty, not nil-panicking",
			wantUp:   []want{},
			wantDown: []want{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var known *chstore.ServiceNameSet
			if c.known != nil {
				known = chstore.NewServiceNameSet(c.known...)
			}
			up, down := serviceNeighborsFromEdges(c.edges, "orders-api", known)
			if got := flat(up); !reflect.DeepEqual(got, c.wantUp) {
				t.Errorf("upstream = %+v, want %+v", got, c.wantUp)
			}
			if got := flat(down); !reflect.DeepEqual(got, c.wantDown) {
				t.Errorf("downstream = %+v, want %+v", got, c.wantDown)
			}
		})
	}
}

// Protokol katlaması ağırlıklı ortalama + birleşik protokol listesi verir.
func TestServiceNeighborsFoldWeightedAvg(t *testing.T) {
	_, down := serviceNeighborsFromEdges([]chstore.ServiceTopologyEdge{
		{ParentService: "orders-api", ChildNode: "billing-api", NodeKind: "service", Protocol: "http", Calls: 10, Errors: 1, AvgMs: 2, P99Ms: 9},
		{ParentService: "orders-api", ChildNode: "billing-api", NodeKind: "service", Protocol: "grpc", Calls: 30, Errors: 3, AvgMs: 6, P99Ms: 20},
	}, "orders-api", nil)
	if len(down) != 1 {
		t.Fatalf("want 1 neighbour, got %d", len(down))
	}
	n := down[0]
	if n.AvgMs != 5 || n.ErrorRate != 10 || n.P99Ms != 20 || !reflect.DeepEqual(n.Protocols, []string{"grpc", "http"}) {
		t.Errorf("fold = %+v", n)
	}
}

func TestCapNeighbors(t *testing.T) {
	ns := make([]serviceNeighbor, serviceNeighborsSideCap+5)
	if got := len(capNeighbors(ns, serviceNeighborsSideCap)); got != serviceNeighborsSideCap {
		t.Fatalf("cap = %d", got)
	}
	if got := len(capNeighbors(ns[:3], serviceNeighborsSideCap)); got != 3 {
		t.Fatalf("under cap = %d", got)
	}
}
