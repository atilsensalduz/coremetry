package selfobs

// vmexport.go — v0.10.1422 (operatör: "Kendi metriclerini de
// VictoriaMetrics ayarlardaki göndersin ve gözüksün").
//
// Coremetry'nin KENDİ metrikleri, Ayarlar → Metrik backend'inde tanımlı
// VictoriaMetrics'e doğrudan yazılır (POST <hedef>/opentelemetry/v1/metrics,
// protobuf+gzip, vmetrics'in yazım istemcisi — bearer/TLS aynı). Collector
// gerekmez; ingest pod'u (self-loop yüzünden trace'i kapalı) da yazar.
//
// GEÇ BAĞLAMA: selfobs.Init, ClickHouse ve ayar blob'undan ÖNCE koşar. Bu
// yüzden okuyucu (PeriodicReader, 30 s) boot'ta ayardan bağımsız kurulur;
// hedef sonradan SetVMSink ile takılır. Hedef yokken / kapalıyken her tur
// sessizce düşer (sayılır) — başlangıcı hiçbir şey bekletmez.
//
// İSTEK YOLU GÜVENLİĞİ: dışa aktarım okuyucunun kendi goroutine'inde, 30
// sn'de bir, en fazla bir 10 s POST. Hata sayılır, durum satırına yazılır,
// ASLA bir isteğe ya da açılışa yansımaz. Export hata DÖNMEZ (SDK'nın global
// hata işleyicisi her turu loglardı); log seyreltilmiş (ilk hata + her 20.).
//
// TEK HEDEF: VM yazımı etkinken gRPC (collector) metrik yolu gatedExporter
// ile düşürülür — aynı seri iki yoldan (collector → ingest → VM ileti) VM'e
// iki farklı etiket kümesiyle girip toplamları ikiye katlamasın.

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/selfobs/otlpenc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// VMSink — vmetrics.Service bunu uygular (selfobs vmetrics'i içe aktaramaz:
// vmetrics → chstore → selfobs döngüsü).
type VMSink interface {
	// SelfMetricsActive — ayara göre kendi metrikleri yazılmalı mı (istek
	// anında okunur; PUT ile açılıp kapanır, restart yok).
	SelfMetricsActive() bool
	// WriteSelfOTLP — sıkıştırılmamış ExportMetricsServiceRequest gövdesi.
	WriteSelfOTLP(ctx context.Context, body []byte) error
}

type sinkHolder struct{ s VMSink }

var vmSink atomic.Value // sinkHolder

// SetVMSink — main, vmetrics ayarı yüklendikten sonra çağırır. nil = çöz.
func SetVMSink(s VMSink) { vmSink.Store(sinkHolder{s: s}) }

func currentSink() VMSink {
	h, _ := vmSink.Load().(sinkHolder)
	return h.s
}

// vmSinkActive — hedef takılı VE ayar etkin.
func vmSinkActive() (VMSink, bool) {
	s := currentSink()
	if s == nil || !s.SelfMetricsActive() {
		return nil, false
	}
	return s, true
}

// VMExportStatus — Ayarlar sekmesindeki durum satırı + /api/health sayaçları.
// POD BAŞINA: her pod kendi metriklerini yazar; bu yanıtı veren pod'un durumu.
type VMExportStatus struct {
	Running     bool   `json:"running"` // okuyucu kurulu (Init koştu)
	Active      bool   `json:"active"`  // ayar etkin, hedef var
	LastOKAt    int64  `json:"lastOkAt,omitempty"`
	LastError   string `json:"lastError,omitempty"`
	LastErrorAt int64  `json:"lastErrorAt,omitempty"`
	Exported    uint64 `json:"exported"`
	Failed      uint64 `json:"failed"`
	Skipped     uint64 `json:"skipped,omitempty"` // desteklenmeyen tür (üstel histogram)
	GatedDrops  uint64 `json:"gatedDrops,omitempty"`
}

type vmStats struct {
	running    atomic.Bool
	exported   atomic.Uint64
	failed     atomic.Uint64
	skipped    atomic.Uint64
	gatedDrops atomic.Uint64
	lastOK     atomic.Int64
	mu         sync.Mutex
	lastErr    string
	lastErrAt  int64
}

var vmst vmStats

// VMExportStatusNow — anlık durum (nil-güvenli, kilit yalnız hata metni için).
func VMExportStatusNow() VMExportStatus {
	_, active := vmSinkActive()
	vmst.mu.Lock()
	le, lea := vmst.lastErr, vmst.lastErrAt
	vmst.mu.Unlock()
	return VMExportStatus{
		Running: vmst.running.Load(), Active: active,
		LastOKAt: vmst.lastOK.Load(), LastError: le, LastErrorAt: lea,
		Exported: vmst.exported.Load(), Failed: vmst.failed.Load(),
		Skipped: vmst.skipped.Load(), GatedDrops: vmst.gatedDrops.Load(),
	}
}

// vmExporter — sdkmetric.Exporter; kümülatif, varsayılan toplama.
type vmExporter struct {
	now func() time.Time
}

func (e *vmExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(k)
}

func (e *vmExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}

func (e *vmExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	s, ok := vmSinkActive()
	if !ok {
		return nil
	}
	body, st, err := otlpenc.Marshal(rm)
	if st.Skipped > 0 {
		vmst.skipped.Add(uint64(st.Skipped)) // #nosec G115 — küçük pozitif sayaç
	}
	if err == nil && body == nil {
		return nil
	}
	if err == nil {
		err = s.WriteSelfOTLP(ctx, body)
	}
	now := time.Now
	if e.now != nil {
		now = e.now
	}
	if err != nil {
		n := vmst.failed.Add(1)
		vmst.mu.Lock()
		vmst.lastErr, vmst.lastErrAt = SafeAttr(err.Error()), now().UnixMilli()
		vmst.mu.Unlock()
		if n == 1 || n%20 == 0 {
			log.Printf("[selfobs] VM self-metrics export failed (#%d): %v", n, err)
		}
		return nil
	}
	vmst.exported.Add(1)
	vmst.lastOK.Store(now().UnixMilli())
	return nil
}

func (e *vmExporter) ForceFlush(context.Context) error { return nil }
func (e *vmExporter) Shutdown(context.Context) error   { return nil }

// gatedExporter — collector (gRPC) metrik yolunu VM yazımı etkinken düşürür.
type gatedExporter struct{ inner sdkmetric.Exporter }

func (g gatedExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return g.inner.Temporality(k)
}

func (g gatedExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return g.inner.Aggregation(k)
}

func (g gatedExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	if _, active := vmSinkActive(); active {
		vmst.gatedDrops.Add(1)
		return nil
	}
	return g.inner.Export(ctx, rm)
}

func (g gatedExporter) ForceFlush(ctx context.Context) error { return g.inner.ForceFlush(ctx) }
func (g gatedExporter) Shutdown(ctx context.Context) error   { return g.inner.Shutdown(ctx) }
