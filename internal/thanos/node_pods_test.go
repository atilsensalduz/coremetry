package thanos

// Clusters → Nodes: node başına pod sayısı (operatör isteği, 2026-10-15).
// Kaynak summary/namespace sayılarıyla aynı cAdvisor ailesi; `node` label'ı
// yoksa kube_pod_info yedeği; ikisi de yoksa alan YOK (0 değil — "—").

import (
	"context"
	"strings"
	"testing"
)

func nodeBase() map[string]string {
	return map[string]string{
		`mode!="idle"`:                   vec(nodeSample("10.0.1.5:9100", "2")),
		"node_memory_MemTotal_bytes":     vec(nodeSample("10.0.1.5:9100", "1000")),
		"node_memory_MemAvailable_bytes": vec(nodeSample("10.0.1.5:9100", "500")),
		"kube_node_info": `{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"node":"worker-1","internal_ip":"10.0.1.5"},"value":[1784271068,"1"]}]}}`,
	}
}

func podsByNodeVec(node, v string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"node":"` + node + `"},"value":[1784271068,"` + v + `"]}]}}`
}

func TestNodeMetricsPodCount(t *testing.T) {
	cases := []struct {
		name   string
		extra  map[string]string
		want   int
		absent bool
	}{
		{"cAdvisor", map[string]string{"container_cpu_usage_seconds_total": podsByNodeVec("worker-1", "37")}, 37, false},
		{"kube_pod_info yedeği", map[string]string{"kube_pod_info": podsByNodeVec("worker-1", "41")}, 41, false},
		{"sıfır pod gerçek değer", map[string]string{"container_cpu_usage_seconds_total": podsByNodeVec("worker-1", "0")}, 0, false},
		{"ölçülemedi", map[string]string{"container_cpu_usage_seconds_total": `{"status":"error","errorType":"execution","error":"nope"}`}, 0, true},
		{"başka node", map[string]string{"container_cpu_usage_seconds_total": podsByNodeVec("worker-9", "5")}, 0, true},
	}
	for _, c := range cases {
		m := nodeBase()
		for k, v := range c.extra {
			m[k] = v
		}
		srv := fakeQuerier(t, "", m)
		rows, err := New().NodeMetrics(context.Background(), ClusterConfig{Name: "c", URL: srv.URL, Enabled: true})
		srv.Close()
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s: rows=%+v err=%v", c.name, rows, err)
		}
		p := rows[0].Pods
		switch {
		case c.absent && p != nil:
			t.Errorf("%s: alan yok olmalıydı, %d", c.name, *p)
		case !c.absent && (p == nil || *p != c.want):
			t.Errorf("%s: pods=%v want %d", c.name, p, c.want)
		}
	}
}

func TestNodePodCountQueriesHonourNamespaceFilter(t *testing.T) {
	for _, q := range []string{nodePodCountQuery("team-.*"), nodePodInfoCountQuery("team-.*")} {
		if !strings.Contains(q, `namespace=~"team-.*"`) || !strings.HasPrefix(q, "count by (node)") {
			t.Errorf("sorgu: %s", q)
		}
	}
	if q := nodePodCountQuery(""); strings.Contains(q, "namespace=~") {
		t.Errorf("boş filtre matcher eklememeli: %s", q)
	}
}
