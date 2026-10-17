package thanos

import "testing"

// v0.10.1287 — AI SRE Faz 1b: k8s kontrolünün bekleme sebebi şablonu.
func TestPodWaitingReasonQuery(t *testing.T) {
	for _, tc := range []struct {
		name, ns, re, want string
	}{
		{"servis regex'i", "", ServicePodRegex("checkout", ""),
			`max by (namespace, pod, reason) (kube_pod_container_status_waiting_reason{pod=~"(checkout)-.*"} == 1)`},
		{"namespace süzgeci", "shop|pay", "(pay)-.*",
			`max by (namespace, pod, reason) (kube_pod_container_status_waiting_reason{pod=~"(pay)-.*",namespace=~"shop|pay"} == 1)`},
		{"boş regex → tüm pod'lar", "", "",
			`max by (namespace, pod, reason) (kube_pod_container_status_waiting_reason{pod!=""} == 1)`},
		// Değer çerçevesi kaçışlanır: tırnak / ters bölü seçiciyi kıramaz.
		{"kaçış", `a"b`, `x\y"z`,
			`max by (namespace, pod, reason) (kube_pod_container_status_waiting_reason{pod=~"x\\y\"z",namespace=~"a\"b"} == 1)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PodWaitingReasonQuery(tc.ns, tc.re); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}
