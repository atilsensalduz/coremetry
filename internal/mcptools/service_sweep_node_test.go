package mcptools

// v0.10.1288 (AI SRE Faz 2) — tembel node / gürültülü komşu kontrolü:
// classifyNode eşikleri (node CPU ≥ %90, servis dışı pod ≥ %40) ve
// readNodeCheck'in durumları (node etiketi yok → skipped, okuma hatası →
// error, istek Nodes bayrağını taşır).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClassifyNode(t *testing.T) {
	cases := []struct {
		name    string
		in      NodePressure
		level   string
		reasons []string
	}{
		{"sakin node", NodePressure{Node: "n1", CPUPct: f64(55), TopPods: []NodeTopPod{{Pod: "pay-1", Pct: 30, Service: true}}}, levelOK, nil},
		{"tam eşik %90", NodePressure{Node: "n1", CPUPct: f64(90)}, levelWarn, []string{"node CPU %90 (allocatable'ın)"}},
		{"%89.4 eşik altı", NodePressure{Node: "n1", CPUPct: f64(89.4)}, levelOK, nil},
		{"gürültülü komşu %40", NodePressure{Node: "n1", CPUPct: f64(60), TopPods: []NodeTopPod{{Namespace: "batch", Pod: "etl-7", Pct: 40}}},
			levelWarn, []string{"gürültülü komşu: batch/etl-7 node CPU'sunun %40'ini kullanıyor"}},
		{"servisin kendi pod'u komşu değil", NodePressure{Node: "n1", CPUPct: f64(60), TopPods: []NodeTopPod{{Pod: "pay-1", Pct: 70, Service: true}}}, levelOK, nil},
		{"CPU okunamadı, komşu yine bakılır", NodePressure{Node: "n1", TopPods: []NodeTopPod{{Pod: "x", Pct: 55}}}, levelWarn,
			[]string{"gürültülü komşu: x node CPU'sunun %55'ini kullanıyor"}},
		{"ikisi birden", NodePressure{Node: "n1", CPUPct: f64(97), TopPods: []NodeTopPod{{Pod: "x", Pct: 41}, {Pod: "y", Pct: 39}}}, levelWarn,
			[]string{"node CPU %97 (allocatable'ın)", "gürültülü komşu: x node CPU'sunun %41'ini kullanıyor"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lvl, why, _ := classifyNode(c.in)
			if lvl != c.level || strings.Join(why, "|") != strings.Join(c.reasons, "|") {
				t.Fatalf("got %s %q, want %s %q", lvl, why, c.level, c.reasons)
			}
		})
	}
}

func TestReadNodeCheck(t *testing.T) {
	from, to := time.Unix(1_700_000_000, 0), time.Unix(1_700_002_700, 0)
	clusters := func() []ClusterRef { return []ClusterRef{{ID: "c1", Name: "prod"}} }
	var gotOpt ServicePodsOpts
	mk := func(res ServicePodsResult, err error) sweepReaders {
		return sweepReaders{clusters: clusters, servicePods: func(_ context.Context, _ string, _ []string, _, _ time.Time, opt ServicePodsOpts) (ServicePodsResult, error) {
			gotOpt = opt
			return res, err
		}}
	}
	// Thanos yok → skipped.
	if nc, _ := readNodeCheck(context.Background(), sweepReaders{}, "pay", from, to); nc.Section.State != hopSkipped {
		t.Fatalf("Thanos yok: %+v", nc.Section)
	}
	// Node etiketi yok → skipped (temiz DEĞİL).
	nc, _ := readNodeCheck(context.Background(), mk(ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{{ID: "c1", Name: "prod", NodesOK: true}}}, nil), "pay", from, to)
	if nc.Section.State != hopSkipped || !strings.Contains(nc.Section.Summary, "node etiketi yok") || !gotOpt.Nodes || gotOpt.WaitingReasons {
		t.Fatalf("etiket yok: %+v opt=%+v", nc.Section, gotOpt)
	}
	// Okuma hatası → error.
	nc, _ = readNodeCheck(context.Background(), mk(ServicePodsResult{}, errors.New("boom")), "pay", from, to)
	if nc.Section.State != hopError {
		t.Fatalf("hata: %+v", nc.Section)
	}
	// Baskılı node + komşu → bulgu, kanıt künyesi.
	nc, _ = readNodeCheck(context.Background(), mk(ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{{
		ID: "c1", Name: "prod", NodesOK: true, Query: "kube_pod_info…",
		PodNodes: map[string]string{"ns\x00pay-1": "n1"},
		Nodes:    []NodePressure{{Node: "n1", CPUPct: f64(94), TopPods: []NodeTopPod{{Namespace: "batch", Pod: "etl", Pct: 52}}}},
	}}}, nil), "pay", from, to)
	if nc.Section.State != hopOK || len(nc.Findings) != 1 || nc.Findings[0].Kind != sweepKindNode || len(nc.Findings[0].Reasons) != 2 ||
		len(nc.Evidences) != 1 || nc.Findings[0].EvidenceRefs[0] != "evidences[0]" || nc.Nodes[0].Cluster != "prod" {
		t.Fatalf("baskı: %+v", nc)
	}
}
