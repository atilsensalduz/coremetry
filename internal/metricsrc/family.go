package metricsrc

import "strings"

// family.go — v0.10.1270 (CoSRE adım 1): metrik ADINDAN aile sınıflandırıcısı.
//
// Saf, I/O yok. Bir adın hangi DEPOYA ait olduğunu adlandırma
// geleneğinden çıkarır:
//
//   - Prometheus cAdvisor / kube-state-metrics / node-exporter / kubelet
//     adları (container_*, kube_*, node_*, kubelet_*, machine_* ve bunları
//     içeren recording rule'lar) → FamilyPodSaturation (Thanos).
//   - JMX exporter adları (jboss_*, OTel semconv KARŞILIĞI OLMAYAN jvm_*)
//     → FamilyJMX (Thanos).
//   - OTel runtime semconv (jvm.*, process.runtime.*, … ve VM'nin
//     nokta→alt çizgi sanitize ettiği jvm_memory_used(_bytes) gibi
//     karşılıkları) → FamilyOTelRuntime (VM, kapalıysa CH).
//   - OTLP ile gelen k8s/host metrikleri (k8s.*, container.*, system.*,
//     process.*, host.*, hw.*) → FamilyOTelInfra (VM/CH — kubeletstats /
//     hostmetrics receiver kopyası Thanos'ta YOK).
//   - geri kalan her şey (http.server.request.duration, rpc.*, db.*,
//     özel uygulama metrikleri) → FamilyApp (VM/CH).
//
// BİLİNEN ÇAKIŞMALAR (ad tek başına karar veremez; varsayım kayıtlı):
//
//  1. jvm_memory_used_bytes hem VM'nin OTel jvm.memory.used sanitize'ı
//     hem de Prometheus client_java 1.x JVM metriği olabilir. Semconv
//     setinde olan jvm_* adı OTelRuntime sayılır — bugünkü VM okumalarının
//     (vmetrics/runtime_pods.go jvm_gc_duration_seconds_*) davranışı
//     korunur. Thanos JMX yüzeyleri (JMXTrend) aileyi AÇIKÇA FamilyJMX
//     verir; sınıflandırıcıya dayanmaz.
//  2. container_memory_working_set_bytes hem cAdvisor hem kubeletstats'ın
//     VM sanitize'ı olabilir. Alt çizgili cAdvisor adı Thanos'a gider
//     (kanonik cAdvisor deposu); OTLP kopyası noktalı adıyla
//     (container.memory.working_set) OTelInfra olarak bulunur.
//
// Bugün HİÇBİR mevcut okuma yolu ailesini FamilyOf ile seçmiyor — çağıranlar
// aileyi açıkça veriyor. Sınıflandırıcı CoSRE adım 2+ (serbest ad → kaynak)
// için tohum; çakışma kuralları değişirse önce bu dosyanın tablo testi.

// podSatPrefixes — Prometheus k8s saturation exporter'larının ad önekleri.
var podSatPrefixes = []string{"container_", "kube_", "node_", "kubelet_", "machine_"}

// otelRuntimeDotted — OTel runtime semconv kök önekleri (noktalı ad).
var otelRuntimeDotted = []string{"jvm.", "process.runtime.", "dotnet.", "go.", "v8js.", "nodejs.", "cpython.", "runtime."}

// otelInfraDotted / otelInfraUnderscore — OTLP-ingested k8s/host aileleri.
var (
	otelInfraDotted     = []string{"k8s.", "container.", "system.", "process.", "host.", "hw."}
	otelInfraUnderscore = []string{"k8s_", "system_"}
)

// otelJVMSemconv — OTel JVM runtime semconv metrikleri, VM sanitize'ı
// (nokta → alt çizgi) uygulanmış KÖK ad (birim/tip soneki yok).
var otelJVMSemconv = map[string]bool{
	"jvm_memory_used": true, "jvm_memory_committed": true, "jvm_memory_limit": true,
	"jvm_memory_init": true, "jvm_memory_used_after_last_gc": true,
	"jvm_gc_duration": true, "jvm_thread_count": true,
	"jvm_class_loaded": true, "jvm_class_unloaded": true, "jvm_class_count": true,
	"jvm_cpu_time": true, "jvm_cpu_count": true, "jvm_cpu_recent_utilization": true,
	"jvm_buffer_memory_used": true, "jvm_buffer_memory_limit": true, "jvm_buffer_count": true,
	"jvm_buffer_memory_usage": true, "jvm_system_cpu_utilization": true,
	"jvm_system_cpu_load_1m": true, "jvm_file_descriptor_count": true,
}

var (
	histSuffixes = []string{"_bucket", "_sum", "_count"}
	unitSuffixes = []string{"_bytes", "_seconds", "_milliseconds", "_ratio", "_total"}
)

// isOTelJVMSemconv — alt çizgili jvm_* adı semconv kökünün sanitize'ı mı?
// Histogram parçası ve birim soneki (her biri en fazla bir kez) soyulur;
// kök adı zaten _count ile biten (jvm_class_count) sonek soymadan da eşleşir.
func isOTelJVMSemconv(name string) bool {
	cands := []string{name}
	for _, h := range histSuffixes {
		if strings.HasSuffix(name, h) {
			cands = append(cands, strings.TrimSuffix(name, h))
		}
	}
	for _, c := range cands {
		if otelJVMSemconv[c] {
			return true
		}
		for _, u := range unitSuffixes {
			if strings.HasSuffix(c, u) && otelJVMSemconv[strings.TrimSuffix(c, u)] {
				return true
			}
		}
	}
	return false
}

func hasAnyPrefix(s string, ps []string) bool {
	for _, p := range ps {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// FamilyOf — metrik adından aile. Boş ad FamilyUnknown; tanınmayan her ad
// FamilyApp (bugünkü metrik gezgini varsayılanı: VM/CH).
func FamilyOf(name string) Family {
	name = strings.TrimSpace(name)
	if name == "" {
		return FamilyUnknown
	}
	// Recording rule (a:b:c) — herhangi bir parça k8s exporter adıysa.
	if strings.Contains(name, ":") {
		for _, seg := range strings.Split(name, ":") {
			if hasAnyPrefix(seg, podSatPrefixes) {
				return FamilyPodSaturation
			}
		}
	}
	switch {
	case hasAnyPrefix(name, otelRuntimeDotted):
		return FamilyOTelRuntime
	case hasAnyPrefix(name, otelInfraDotted):
		return FamilyOTelInfra
	case strings.HasPrefix(name, "jboss_"):
		return FamilyJMX
	case strings.HasPrefix(name, "jvm_"):
		if isOTelJVMSemconv(name) {
			return FamilyOTelRuntime
		}
		return FamilyJMX
	case hasAnyPrefix(name, podSatPrefixes):
		return FamilyPodSaturation
	case hasAnyPrefix(name, otelInfraUnderscore):
		return FamilyOTelInfra
	}
	return FamilyApp
}
