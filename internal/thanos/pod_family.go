package thanos

// pod_family.go — v0.10.1271 (CoSRE adım 2): query_metrics aracının pod
// doygunluk ailelerinin SABİT PromQL şablonları + servis → pod regex'i.
//
// Model PromQL YAZMAZ: aile (cpu | memory | restarts) + pod regex'i + pencere
// sunucuda bu şablonlara girer. Seçici pod/namespace matcher'ları envanter
// sorgularıyla AYNI yardımcılardan (podMatcher / nsMatcher); cluster matcher'ı
// burada DEĞİL — çağıran metricsrc.Resolver.ThanosInstant → doQuery yolundan
// geçirir (paylaşımlı querier'da her seçiciye enjekte edilir).
//
// Pencere toplaması anlık sorguda range-vector ile yapılır (alt sorgu yok):
//   cpu      → rate(container_cpu_usage_seconds_total[W])      — pencerede ORTALAMA çekirdek
//   memory   → max_over_time(container_memory_working_set_bytes[W]) — pencerede TEPE bayt (OOM ekseni)
//   restarts → increase(kube_pod_container_status_restarts_total[W]) — pencerede yeniden başlatma
// Pencere "şimdi"de bitmiyorsa (çıpalı sohbet) `offset` ile geriye kaydırılır.

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Pod aile adları (query_metrics enum'unun Thanos yarısı).
const (
	PodFamilyCPU      = "cpu"
	PodFamilyMemory   = "memory"
	PodFamilyRestarts = "restarts"
)

var servicePodEnvSuffixes = []string{"-prod", "-int", "-uat", "-prep"}

// ServicePodRegex — SAF; frontend pages/clusters/podWorkload.ts
// servicePodRegex ile birebir (Infrastructure sekmesiyle aynı pod kümesi):
// deployment | servis | ortam eksiz ad, "-.*" son eki. v0.10.1271 — api'deki
// thanosServicePodRegex'ten taşındı (mcptools da kullanır; api delege eder).
func ServicePodRegex(service, deploy string) string {
	stripped := service
	for _, suf := range servicePodEnvSuffixes {
		if len(service) > len(suf) && strings.HasSuffix(service, suf) {
			stripped = strings.TrimSuffix(service, suf)
			break
		}
	}
	seen := map[string]bool{}
	var cands []string
	for _, c := range []string{deploy, service, stripped} {
		if c != "" && !seen[c] {
			seen[c] = true
			cands = append(cands, regexp.QuoteMeta(c))
		}
	}
	return "(" + strings.Join(cands, "|") + ")-.*"
}

// promDur — saniye cinsinden PromQL süre literal'i (en az 1s).
func promDur(d time.Duration) string {
	s := int64(d / time.Second)
	if s < 1 {
		s = 1
	}
	return fmt.Sprintf("%ds", s)
}

// PodFamilyQuery — SAF: pod ailesinin anlık PromQL'i. podRe boş = cluster
// geneli (namespace filtresiyle); byPod=false → tek toplam değer;
// byPod=true → topk(limit) pod başına. offset ≤ 0 → kaydırma yok.
func PodFamilyQuery(family, nsFilter, podRe string, window, offset time.Duration, byPod bool, limit int) (string, error) {
	if window <= 0 {
		return "", fmt.Errorf("thanos pod family: window must be positive")
	}
	off := ""
	if offset >= time.Minute {
		off = " offset " + promDur(offset)
	}
	sel := podMatcher(podRe) + nsMatcher(nsFilter)
	var inner string
	switch family {
	case PodFamilyCPU:
		inner = fmt.Sprintf(`rate(container_cpu_usage_seconds_total{container!="",container!="POD",%s}[%s]%s)`, sel, promDur(window), off)
	case PodFamilyMemory:
		inner = fmt.Sprintf(`max_over_time(container_memory_working_set_bytes{container!="",container!="POD",%s}[%s]%s)`, sel, promDur(window), off)
	case PodFamilyRestarts:
		inner = fmt.Sprintf(`increase(kube_pod_container_status_restarts_total{%s}[%s]%s)`, sel, promDur(window), off)
	default:
		return "", fmt.Errorf("thanos pod family: unknown family %q", family)
	}
	if !byPod {
		return "sum(" + inner + ")", nil
	}
	if limit <= 0 || limit > podListLimit {
		limit = podListLimit
	}
	return fmt.Sprintf("topk(%d, sum by (namespace, pod) (%s))", limit, inner), nil
}

// PodWaitingReasonQuery — v0.10.1287 (AI SRE Faz 1b, k8s kontrolü): servis
// pod'larının ŞU ANKİ bekleme sebebi (CrashLoopBackOff / ImagePullBackOff /
// ErrImagePull / CreateContainerConfigError …). KSM serisi
// kube_pod_container_status_waiting_reason (namespace, pod, container, reason)
// etiketli; `== 1` ŞART (podLastTermQuery gerekçesi: KSM sebep dışı reason
// serilerini de 0 değerle basar), container ekseni max by ile katlanır.
// Seçici envanter sorgularıyla AYNI yardımcılar (podMatcher / nsMatcher —
// değer çerçevesi escapeLabelValue ile kaçışlanır); cluster matcher'ı
// çağıranın doQuery yolunda enjekte edilir. Kubernetes Events ingest
// edilmiyor — yalnız KSM durumu okunur. SAF.
func PodWaitingReasonQuery(nsFilter, podRe string) string {
	return fmt.Sprintf(
		`max by (namespace, pod, reason) (kube_pod_container_status_waiting_reason{%s%s} == 1)`,
		podMatcher(podRe), nsMatcher(nsFilter))
}
