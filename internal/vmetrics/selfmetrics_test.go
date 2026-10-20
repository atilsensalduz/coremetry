package vmetrics

// v0.10.1422 — operatör: 'Kendi metriclerini de VictoriaMetrics ayarlardaki
// göndersin ve gözüksün'. Kendi-metrik yazımının karar tablosu, yazım yolu
// (WriteEnabled'dan bağımsız, aynı auth/gzip/hata sınıfları) ve yazım adının
// OKUMA adaylarıyla (names.go) eşliği.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/selfobs/otlpenc"
)

func TestSelfMetricsState(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  Settings
		want bool
		why  string
	}{
		{"boş blob", Settings{}, false, "hedefi yok"},
		{"auto + okuma açık + vmsingle", Settings{Enabled: true, BaseURL: "http://vm:8428"}, true, ""},
		{"auto + okuma kapalı", Settings{BaseURL: "http://vm:8428"}, false, "okuması kapalı"},
		{"auto + vmselect, yazma URL'i yok", Settings{Enabled: true, BaseURL: "http://vmselect:8481/select/0/prometheus"}, false, "vminsert"},
		{"auto + vmselect + vminsert", Settings{Enabled: true, BaseURL: "http://vmselect:8481/select/0/prometheus", WriteURL: "http://vminsert:8480/insert/0"}, true, ""},
		{"on + okuma kapalı", Settings{SelfMetrics: "on", BaseURL: "http://vm:8428"}, true, ""},
		{"on + hedef yok", Settings{SelfMetrics: "on"}, false, "hedefi yok"},
		{"off", Settings{SelfMetrics: "off", Enabled: true, BaseURL: "http://vm:8428"}, false, "kapalı"},
		{"bilinmeyen = auto", Settings{SelfMetrics: "x", Enabled: true, BaseURL: "http://vm:8428"}, true, ""},
		{"WriteEnabled kararı ETKİLEMEZ", Settings{Enabled: true, BaseURL: "http://vm:8428", WriteEnabled: false}, true, ""},
	} {
		got, why := SelfMetricsState(c.cfg)
		if got != c.want || !strings.Contains(why, c.why) {
			t.Errorf("%s: got (%v,%q), want (%v, ~%q)", c.name, got, why, c.want, c.why)
		}
	}
}

func TestValidSelfMetrics(t *testing.T) {
	for v, want := range map[string]bool{"": true, "auto": true, "on": true, "OFF": true, "yes": false} {
		if ValidSelfMetrics(v) != want {
			t.Errorf("ValidSelfMetrics(%q) != %v", v, want)
		}
	}
}

func TestWriteSelfOTLPIgnoresWriteEnabled(t *testing.T) {
	var got capture
	srv := writeServer(t, 204, "", &got)
	defer srv.Close()
	s := New()
	s.Configure(Settings{Enabled: true, BaseURL: srv.URL, AuthType: "bearer", Token: "tok-1", WriteEnabled: false})
	if !s.SelfMetricsActive() {
		t.Fatal("auto + read enabled must be active")
	}
	if err := s.WriteSelfOTLP(context.Background(), []byte("payload")); err != nil {
		t.Fatal(err)
	}
	if got.path != otlpMetricsPath || got.cenc != "gzip" || got.ctype != writeContentType || got.auth != "Bearer tok-1" {
		t.Fatalf("request = %+v", got)
	}
	if string(got.body) != "payload" {
		t.Fatalf("body = %q (must be gzip of the raw body)", got.body)
	}
}

func TestWriteSelfOTLPErrorClasses(t *testing.T) {
	for _, c := range []struct {
		status int
		want   error
	}{{400, ErrRejected}, {401, ErrUpstream}, {503, ErrRetryable}} {
		var got capture
		srv := writeServer(t, c.status, "nope", &got)
		s := New()
		s.Configure(Settings{SelfMetrics: "on", BaseURL: srv.URL})
		err := s.WriteSelfOTLP(context.Background(), []byte("x"))
		srv.Close()
		if !errors.Is(err, c.want) {
			t.Errorf("HTTP %d: err = %v, want %v", c.status, err, c.want)
		}
	}
	s := New()
	s.Configure(Settings{SelfMetrics: "off", BaseURL: "http://vm:8428"})
	if err := s.WriteSelfOTLP(context.Background(), []byte("x")); !errors.Is(err, ErrNoWrite) {
		t.Fatalf("off → ErrNoWrite, got %v", err)
	}
	var nilSvc *Service
	if nilSvc.SelfMetricsActive() || !errors.Is(nilSvc.WriteSelfOTLP(context.Background(), nil), ErrNoWrite) {
		t.Fatal("nil receiver must be inactive")
	}
}

func TestSelfMetricsJSONKey(t *testing.T) {
	raw, _ := json.Marshal(Settings{Enabled: true, BaseURL: "http://vm:8428"})
	if strings.Contains(string(raw), "selfMetrics") {
		t.Fatalf("default blob must omit selfMetrics: %s", raw)
	}
	raw, _ = json.Marshal(Settings{SelfMetrics: "off"})
	if !strings.Contains(string(raw), `"selfMetrics":"off"`) {
		t.Fatalf("blob = %s", raw)
	}
	s := New()
	s.Configure(Settings{Enabled: true, BaseURL: "http://vm:8428"})
	snap := s.Snapshot()
	if snap.SelfMetrics != "auto" || !snap.SelfMetricsActive {
		t.Fatalf("snapshot = %+v", snap)
	}
	sraw, _ := json.Marshal(snap)
	for _, k := range []string{`"selfMetrics":"auto"`, `"selfMetricsActive":true`} {
		if !strings.Contains(string(sraw), k) {
			t.Fatalf("snapshot JSON lacks %s: %s", k, sraw)
		}
	}
	for _, rel := range []string{
		filepath.Join("frontend", "src", "lib", "types.ts"),
		filepath.Join("frontend", "src", "pages", "settings", "MetricsBackendTab.tsx"),
	} {
		b, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if !strings.Contains(string(b), "selfMetrics") {
			t.Errorf("%s does not mention selfMetrics", rel)
		}
	}
}

// Yazım adı (otlpenc) okuma adaylarından (nameSpellings) biri olmalı; etiket
// sanitizasyonu promLabel ile aynı olmalı. Biri ayrışırsa VM'e yazılan seri
// metrik yüzeylerinden okunamaz.
func TestSelfMetricNamesMatchReadCandidates(t *testing.T) {
	for _, c := range []struct {
		name, unit string
		k          otlpenc.Kind
	}{
		{"http.server.request.duration", "s", otlpenc.KindHistogram},
		{"rpc.server.duration", "ms", otlpenc.KindHistogram},
		{"worker.job.duration", "s", otlpenc.KindHistogram},
		{"worker.job.runs", "", otlpenc.KindCounter},
		{"process.cpu.time", "s", otlpenc.KindCounter},
		{"go.memory.used", "By", otlpenc.KindGauge},
		{"cpu.utilization", "1", otlpenc.KindGauge},
		{"ldap_sync_errors_total", "", otlpenc.KindCounter},
		{"op_baseline_refresh_duration_seconds", "s", otlpenc.KindHistogram},
	} {
		w := otlpenc.PromMetricName(c.name, c.unit, c.k)
		cands := nameSpellings(c.name, c.k == otlpenc.KindCounter)
		found := false
		for _, x := range cands {
			if x == w {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: written %q not among read candidates %v", c.name, w, cands)
		}
	}
	for _, k := range []string{"service.name", "http.route", "http.request.method", "http.response.status_code",
		"deployment.environment.name", "k8s.pod.name", "rpc.grpc.status_code", "9x", "a-b"} {
		if otlpenc.PromLabel(k) != promLabel(k) {
			t.Errorf("label %q: otlpenc %q != vmetrics %q", k, otlpenc.PromLabel(k), promLabel(k))
		}
	}
}
