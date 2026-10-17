package thanos

// v0.10.1288 (AI SRE Faz 2) — tembel node kontrolünün PromQL şablonları:
// node adları regex + dize çerçevesinde kaçışlanır, tavan 3 node, cluster
// matcher enjeksiyonu group_left / on listelerini metrik sanmaz.

import (
	"strings"
	"testing"
	"time"
)

func TestNodeQueriesEscape(t *testing.T) {
	nodes := []string{`ip-10-0-1-2.ec2.internal`, `evil"} or vector(1) #`, `back\slash`, `fourth-node`}
	q, err := NodeCPUPressureQuery(nodes, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := `node=~"^(ip-10-0-1-2\\.ec2\\.internal|evil\"\\} or vector\\(1\\) #|back\\\\slash)$"`
	if strings.Count(q, want) != 2 {
		t.Fatalf("node seçicisi iki tarafta da kaçışlı olmalı:\n%s\nbeklenen parça: %s", q, want)
	}
	if strings.Contains(q, "fourth-node") {
		t.Fatal("tavan 3 node")
	}
	if strings.Contains(q, " offset ") {
		t.Fatal("offset < 1 dk → kaydırma yok")
	}
	top, err := NodeTopPodsQuery(nodes[:1], 0, 90*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(top, "topk(5, ") || strings.Count(top, " offset 5400s") != 2 || !strings.Contains(top, "group_left sum by (node)") {
		t.Fatalf("top pods: %s", top)
	}
	// Seçici dize çerçevesini kıramaz: tırnaklar dengeli kalır.
	for _, s := range []string{q, top} {
		unesc := strings.ReplaceAll(strings.ReplaceAll(s, `\\`, ""), `\"`, "")
		if strings.Count(unesc, `"`)%2 != 0 {
			t.Fatalf("tırnak dengesi bozuk: %s", s)
		}
	}
	// Cluster matcher enjeksiyonu: iki seçiciye de gider, group_left/on metrik değil.
	inj := withClusterMatcher(top, "cluster", "p1")
	if strings.Count(inj, `cluster="p1"`) != 2 || strings.Contains(inj, `group_left{`) || strings.Contains(inj, `on{`) {
		t.Fatalf("enjeksiyon: %s", inj)
	}
	if _, err := NodeCPUPressureQuery(nil, 0); err == nil {
		t.Fatal("node yok → hata (boş seçici tüm node'ları okurdu)")
	}
	if got := PodNodeQuery(`team-.*`, `(pay|pay-prod)-.*`); got != `max by (namespace, pod, node) (kube_pod_info{node!="",pod=~"(pay|pay-prod)-.*",namespace=~"team-.*"})` {
		t.Fatalf("PodNodeQuery: %s", got)
	}
}
