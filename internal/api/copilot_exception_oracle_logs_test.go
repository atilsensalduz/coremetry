package api

// copilot_exception_oracle_logs_test.go — v0.10.1370 regresyon testleri
// (operatör-bildirimli: "Teknik hatalar için Exceptions sayfasında trace
// incelemesi gibi loga bakmıyor — Ask CoSRE veya explain için"). Kök neden:
// Oracle (`ora:`) grubunun trace'i yalnız ClickHouse'ta arandı (Tempo'ya hiç
// bakılmadı) ve CoSRE takip sohbeti `ora:` grubunda span kurucusunu
// kullanıyordu. Pinler: interaktif yolun Oracle okuyucusu Tempo-önce
// (resolveTraceSpans) ve CH hiçbir id'yi bulamadığında en yeni id Tempo'dan
// yüklenir; arka plan açıklayıcının *chstore.Store'u Tempo denemesi yapmaz
// (CH-only); takip sohbeti dağıtıcıdan geçer.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// oraLogsReader — Oracle okuyucusunun sahtesi: CH hiçbir trace'i bilmiyor.
type oraLogsReader struct {
	rows     []chstore.OracleErrorRow
	chTraces []string
}

func (f *oraLogsReader) OracleGroupSource(context.Context, *chstore.ExceptionGroup) (string, error) {
	return "o-1", nil
}

func (f *oraLogsReader) OracleErrorsByOpCode(_ context.Context, _, _, _ string, _, _ time.Time, _ int) ([]chstore.OracleErrorRow, error) {
	return f.rows, nil
}

func (f *oraLogsReader) TraceFactsByIDs(context.Context, []string, time.Time, time.Time) (map[string]chstore.TraceFact, error) {
	return map[string]chstore.TraceFact{}, nil
}

func (f *oraLogsReader) GetTrace(_ context.Context, id string) ([]chstore.SpanRow, error) {
	f.chTraces = append(f.chTraces, id)
	return nil, nil
}

func TestOracleInteractiveReaderTempoFirst(t *testing.T) {
	if oracleInteractiveReader(&Server{}, nil) != nil {
		t.Fatal("nil taban nil kalmalı (nil arayüz)")
	}
	base := &oraLogsReader{}
	rd := oracleInteractiveReader(&Server{tempo: scTempo(t, map[string]string{"service.name": "svc-caller"})}, base)
	tsl, ok := rd.(anomaly.TraceSourceLoader)
	if !ok {
		t.Fatal("interaktif okuyucu TraceSourceLoader uygulamalı")
	}
	spans, src, err := tsl.GetTraceSource(context.Background(), tfTrace)
	if err != nil || len(spans) == 0 || src != traceSourceTempo || len(base.chTraces) != 0 {
		t.Fatalf("Tempo-önce: spans=%d src=%q err=%v ch=%v", len(spans), src, err, base.chTraces)
	}
	if sp, err := rd.GetTrace(context.Background(), tfTrace); err != nil || len(sp) == 0 || len(base.chTraces) != 0 {
		t.Fatalf("GetTrace da Tempo-önce: %d %v ch=%v", len(sp), err, base.chTraces)
	}
	// Arka plan açıklayıcı *chstore.Store verir → Tempo denemesi YOK (CH-only).
	if _, ok := any((*chstore.Store)(nil)).(anomaly.TraceSourceLoader); ok {
		t.Fatal("*chstore.Store TraceSourceLoader uygulamamalı (arka plan CH-only)")
	}
}

func TestOracleBuilderLoadsTempoTraceWhenCHMisses(t *testing.T) {
	g := &chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint("o-1", "ORA-00001", "OP_ORDER"),
		Type: "ORA-00001", Message: "OP_ORDER", Service: chstore.OracleGroupFallbackService("core-errlog"),
		FirstSeen: time.Now().Add(-2 * time.Hour).UnixNano(), LastSeen: time.Now().Add(-time.Hour).UnixNano()}
	base := &oraLogsReader{rows: []chstore.OracleErrorRow{{SourceID: "o-1", Time: time.Now().Add(-time.Hour), TraceID: tfTrace, Body: "x"}}}
	s := &Server{tempo: scTempo(t, map[string]string{"service.name": "svc-caller"})}

	// CH-only (arka plan): trace yok → "yok" satırı.
	in := anomaly.BuildOracleExceptionExplainInput(context.Background(), base, nil, g, time.UTC)
	if in.Oracle.CoremetryTrace != "" || !strings.Contains(in.User, "Coremetry trace'i: yok") {
		t.Fatalf("CH-only yol trace yüklememeli: %q", in.Oracle.CoremetryTrace)
	}
	// İnteraktif: en yeni id Tempo-önce BİR kez → yüklenir.
	in = anomaly.BuildOracleExceptionExplainInput(context.Background(), oracleInteractiveReader(s, base), nil, g, time.UTC)
	if in.Oracle.CoremetryTrace != tfTrace || in.TraceID != tfTrace || !strings.Contains(in.User, "Coremetry TRACE'i (") {
		t.Fatalf("Tempo'daki trace yüklenmeli: %q\n%s", in.Oracle.CoremetryTrace, in.User)
	}
	if len(base.chTraces) != 0 {
		t.Fatalf("Tempo cevap verdi → CH'ye gidilmemeli: %v", base.chTraces)
	}
}

// Kaynak pini: varsayılan Oracle kurucusu okuyucuyu Tempo-önce sarar; takip
// sohbeti dağıtıcıdan geçer (span kurucusunu doğrudan çağırmaz).
func TestOracleInteractiveWiring(t *testing.T) {
	b, _ := os.ReadFile("copilot_exception_oracle.go")
	if !strings.Contains(string(b), "rd = oracleInteractiveReader(s, s.store)") {
		t.Error("Oracle kurucusu okuyucuyu Tempo-önce sarmalı")
	}
	f, _ := os.ReadFile("chat_exception_followup.go")
	if !strings.Contains(string(f), "in, _ := s.exceptionExplainInput(ctx, g, loc)") || strings.Contains(string(f), "anomaly.BuildExceptionExplainInput(ctx") {
		t.Error("drawerExceptionInput exceptionExplainInput dağıtıcısını kullanmalı")
	}
}
