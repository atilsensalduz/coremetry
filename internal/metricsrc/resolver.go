// Package metricsrc — v0.10.1270 (CoSRE adım 1): metrik kaynağı çözümleyici.
//
// "Sunucu deterministik toplar, model anlatır": hangi metriğin hangi
// depodan (Thanos / VictoriaMetrics / ClickHouse) okunacağına model değil
// bu paket karar verir. Karar TEK yerde: Decide (saf, tablo testli).
// Canlı Resolver ve VM-mi-CH-mi soran her çağıran (anomaly soruşturması,
// MCP get_pod_health, evaluator GC kapısı, api metricSource, heap bandı
// kaynağı) Decide'a sorar.
//
// YÖNLENDİRME TABLOSU (aile → depo). Operatör brief'i "JVM/GC/pod/cluster →
// Thanos; RED/app → VM" diyordu; keşif bunun OTel JVM metrikleri için
// doğru OLMADIĞINI gösterdi — Thanos'ta OTel jvm.memory.* / jvm.gc.* YOK,
// yalnız JMX exporter (jvm_*/jboss_*) ve cAdvisor/KSM var. Mevcut veriyi
// kırmamak için aile bazında açık tablo (VARSAYIM — commit mesajı bunu
// söyler):
//
//	FamilyPodSaturation  cAdvisor/KSM/node-exporter        → Thanos (cluster zorunlu, matcher'lı)
//	FamilyJMX            JMX exporter jvm_*/jboss_*         → Thanos (cluster zorunlu, matcher'lı)
//	FamilyOTelRuntime    OTel jvm.memory.*/jvm.gc.*/…       → VM yapılandırılmışsa VM, değilse CH
//	FamilyOTelInfra      OTLP k8s.*/container.*/system.*    → VM yapılandırılmışsa VM, değilse CH
//	FamilyApp            RED/HTTP/RPC/özel uygulama        → VM yapılandırılmışsa VM, değilse CH
//
// VM ile CH arasında GERİ DÜŞÜŞ yok (bugünkü davranış): VM yapılandırılmış
// ama erişilemezse okuma hata döner, sessizce CH'ye kaymaz — boş CH'den
// "veri yok" okumak yanlış kanıt olurdu.
//
// Thanos cluster kapsamı: Thanos'a giden her yeni yol ThanosInstant /
// Route.ScopedQuery'den geçer; ikisi de thanos paketinin doQueryWith /
// withClusterMatcher zincirini (EffectiveThanosLabel) kullanır — matcher
// burada YENİDEN yazılmaz.
package metricsrc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// Family — metrik ailesi (yönlendirme birimi).
type Family int

const (
	FamilyUnknown Family = iota
	FamilyPodSaturation
	FamilyJMX
	FamilyOTelRuntime
	FamilyOTelInfra
	FamilyApp
)

func (f Family) String() string {
	switch f {
	case FamilyPodSaturation:
		return "pod_saturation"
	case FamilyJMX:
		return "jmx"
	case FamilyOTelRuntime:
		return "otel_runtime"
	case FamilyOTelInfra:
		return "otel_infra"
	case FamilyApp:
		return "app"
	}
	return "unknown"
}

// thanosFamily — Thanos'ta yaşayan aileler.
func (f Family) thanosFamily() bool { return f == FamilyPodSaturation || f == FamilyJMX }

// Backend — okumayı cevaplayan depo. BackendNone = cevaplayacak depo yok
// (bilinmeyen cluster, Thanos kurulmamış).
type Backend string

const (
	BackendNone   Backend = ""
	BackendThanos Backend = "thanos"
	BackendVM     Backend = "vm"
	BackendCH     Backend = "ch"
)

// Scope — sorunun kapsamı. Service/Env bugün yönlendirmeyi DEĞİŞTİRMEZ
// (taşınır ki adım 2+ servis başına kaynak kuralı eklediğinde imza
// değişmesin); Cluster Thanos aileleri için zorunludur (id ya da ad).
type Scope struct {
	Service string
	Env     string
	Cluster string
}

// State — kararın girdisi olan depo durumu (saf Decide için; canlı
// Resolver bunu her çağrıda doldurur).
type State struct {
	VMConfigured bool
	// Cluster / ClusterFound — Scope.Cluster'ın çözülmüş kaydı.
	Cluster      thanos.ClusterConfig
	ClusterFound bool
}

// Route — kararın sonucu.
type Route struct {
	Family  Family
	Backend Backend
	Reason  string
	// NeedsCluster — Thanos ailesi ama cluster verilmedi: kapsamsız sorgu
	// tüm cluster'ları okurdu, bu yüzden yol KULLANILAMAZ.
	NeedsCluster bool
	// Cluster — Thanos rotasında çözülmüş kayıt.
	Cluster thanos.ClusterConfig
	// MatcherLabel/Value — paylaşımlı querier'da her seçiciye eklenecek
	// cluster matcher'ı (EffectiveThanosLabel). Boş = cluster başına URL.
	MatcherLabel, MatcherValue string
}

// ClusterScoped — Thanos rotası ve cluster'ı çözülmüş (okunabilir).
func (r Route) ClusterScoped() bool {
	return r.Backend == BackendThanos && !r.NeedsCluster && r.Cluster.URL != ""
}

// ScopedQuery — Thanos'a GERÇEKTEN gidecek ifade (doQueryWith'in
// enjeksiyonuyla aynı: ClusterConfig.EffectiveQuery). Thanos dışı ya da
// kapsamsız rota hata döner.
func (r Route) ScopedQuery(expr string) (string, error) {
	if !r.ClusterScoped() {
		return "", fmt.Errorf("metricsrc: %s route is not a cluster-scoped Thanos route (%s)", r.Family, r.Reason)
	}
	return r.Cluster.EffectiveQuery(expr), nil
}

// Decide — TEK karar noktası (saf). Bkz. paket başlığındaki tablo.
func Decide(f Family, sc Scope, st State) Route {
	r := Route{Family: f}
	if f.thanosFamily() {
		r.Backend = BackendThanos
		switch {
		case sc.Cluster == "":
			r.NeedsCluster = true
			r.Reason = f.String() + " lives in Thanos; a cluster is required (unscoped query would read every cluster)"
		case !st.ClusterFound:
			r.Backend = BackendNone
			r.Reason = fmt.Sprintf("cluster %q is not an enabled Thanos cluster", sc.Cluster)
		default:
			r.Cluster = st.Cluster
			r.MatcherLabel, r.MatcherValue = st.Cluster.EffectiveThanosLabel()
			if r.MatcherLabel != "" {
				r.Reason = fmt.Sprintf("%s → Thanos cluster %s (shared querier, matcher %s=%q)", f, st.Cluster.Name, r.MatcherLabel, r.MatcherValue)
			} else {
				r.Reason = fmt.Sprintf("%s → Thanos cluster %s (per-cluster URL)", f, st.Cluster.Name)
			}
		}
		return r
	}
	// OTel runtime / infra / app / unknown: VM if configured else CH. These
	// are OTLP-ingested; Thanos does not hold them, so a cluster never
	// changes the backend.
	if st.VMConfigured {
		r.Backend = BackendVM
		r.Reason = f.String() + " → VictoriaMetrics (configured)"
	} else {
		r.Backend = BackendCH
		r.Reason = f.String() + " → ClickHouse (VictoriaMetrics not configured)"
	}
	return r
}

// UsesVM — VM-mi-CH-mi soran çağıranların kısa yolu (evaluator, api,
// anomaly). Decide'ı çağırır; kendi kuralı yoktur.
func UsesVM(f Family, vmConfigured bool) bool {
	return Decide(f, Scope{}, State{VMConfigured: vmConfigured}).Backend == BackendVM
}

// ── canlı Resolver ──────────────────────────────────────────────────────

// VMBackend — VictoriaMetrics: yapılandırma kapısı + JVM pod okumaları.
// *vmetrics.Service bunu karşılar (Configured nil-güvenli).
type VMBackend interface {
	Configured() bool
	chstore.RuntimePodReader
}

// ThanosBackend — cluster çözümü + matcher'lı anlık sorgu.
// *thanos.Service bunu karşılar.
type ThanosBackend interface {
	ClusterByRef(ref string) (thanos.ClusterConfig, bool)
	InstantSamples(ctx context.Context, c thanos.ClusterConfig, expr string) ([]thanos.Sample, error)
}

// Backends — main.go / api'nin elindeki somut servisler. Her biri nil
// olabilir (typed-nil dahil: *vmetrics.Service.Configured ve
// *thanos.Service.ClusterByRef nil alıcıda güvenli).
type Backends struct {
	VM     VMBackend
	CH     chstore.RuntimePodReader
	Thanos ThanosBackend
}

// Resolver — Backends + Decide. Ucuz değer; her çağrıda kurulabilir.
type Resolver struct{ b Backends }

func New(b Backends) *Resolver { return &Resolver{b: b} }

func (r *Resolver) vmConfigured() bool { return r.b.VM != nil && r.b.VM.Configured() }

// Resolve — canlı durumla Decide. Karar ÇAĞRI ANINDA: Settings değişince
// restart gerekmez.
func (r *Resolver) Resolve(f Family, sc Scope) Route {
	st := State{VMConfigured: r.vmConfigured()}
	if f.thanosFamily() && sc.Cluster != "" && r.b.Thanos != nil {
		st.Cluster, st.ClusterFound = r.b.Thanos.ClusterByRef(sc.Cluster)
	}
	return Decide(f, sc, st)
}

// RuntimePods — OTel JVM pod okuyucusu (anomaly soruşturması + MCP
// get_pod_health). Eski vmetrics.RuntimePodsOr'un yerini alır; seçim
// FamilyOTelRuntime için Decide'dan gelir.
func (r *Resolver) RuntimePods() chstore.RuntimePodReader { return runtimePods{r} }

type runtimePods struct{ r *Resolver }

func (p runtimePods) pick() chstore.RuntimePodReader {
	if p.r.Resolve(FamilyOTelRuntime, Scope{}).Backend == BackendVM {
		return p.r.b.VM
	}
	return p.r.b.CH
}

func (p runtimePods) JVMHeapPodUsage(ctx context.Context, from, to time.Time) ([]chstore.CapacitySample, error) {
	return p.pick().JVMHeapPodUsage(ctx, from, to)
}

func (p runtimePods) JVMGCPodPause(ctx context.Context, from, to time.Time) ([]chstore.CapacitySample, error) {
	return p.pick().JVMGCPodPause(ctx, from, to)
}

// ErrNotThanos — Thanos dışı aileyle ThanosInstant çağrıldı.
var ErrNotThanos = errors.New("metricsrc: family is not served by Thanos")

// ThanosInstant — cluster kapsamlı Thanos anlık sorgusu. Thanos'a giden
// her YENİ yol bundan geçer: cluster zorunlu, kayıt ClusterByRef ile
// çözülür ve sorgu thanos.InstantSamples → doQuery → doQueryWith yolundan
// gider, yani paylaşımlı querier'da matcher HER seçiciye enjekte edilir.
func (r *Resolver) ThanosInstant(ctx context.Context, f Family, clusterRef, expr string) ([]thanos.Sample, Route, error) {
	if !f.thanosFamily() {
		return nil, Route{Family: f}, ErrNotThanos
	}
	if r.b.Thanos == nil {
		return nil, Route{Family: f, Reason: "thanos not wired"}, errors.New("metricsrc: thanos backend not wired")
	}
	route := r.Resolve(f, Scope{Cluster: clusterRef})
	if !route.ClusterScoped() {
		return nil, route, errors.New("metricsrc: " + route.Reason)
	}
	out, err := r.b.Thanos.InstantSamples(ctx, route.Cluster, expr)
	return out, route, err
}
