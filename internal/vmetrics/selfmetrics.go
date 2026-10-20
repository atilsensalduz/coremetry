package vmetrics

// selfmetrics.go — v0.10.1422 (operatör: "Kendi metriclerini de
// VictoriaMetrics ayarlardaki göndersin ve gözüksün").
//
// Coremetry'nin kendi metrikleri (selfobs/vmexport.go) bu servisin yazım
// istemcisini kullanır: aynı hedef (WriteTarget), aynı bearer/tokenRef,
// aynı TLS ayarı, aynı hata sınıfları (write.go). Tek fark: WriteEnabled'ı
// (gelen OTLP gövdesinin iletimi) BEKLEMEZ — kararı SelfMetrics modu verir.

import (
	"context"
	"strings"
)

const (
	SelfMetricsAuto = "auto"
	SelfMetricsOn   = "on"
	SelfMetricsOff  = "off"
)

// NormalizeSelfMetrics — boş ya da bilinmeyen değer "auto" (blob elle
// bozulsa bile karar tanımlı bir moda düşer; PUT bilinmeyeni 400'ler).
func NormalizeSelfMetrics(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case SelfMetricsOn:
		return SelfMetricsOn
	case SelfMetricsOff:
		return SelfMetricsOff
	}
	return SelfMetricsAuto
}

// ValidSelfMetrics — PUT doğrulaması ("" = auto).
func ValidSelfMetrics(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", SelfMetricsAuto, SelfMetricsOn, SelfMetricsOff:
		return true
	}
	return false
}

// SelfMetricsState — SAF: (etkin mi, değilse neden). Tablo testli
// (selfmetrics_test.go).
//
// auto'nun vmselect istisnası: cluster VM'de okuma adresi vmselect'in
// /select/<tenant>/prometheus önekidir ve oraya OTLP yazılamaz. Yazma URL'i
// boşken auto o adrese her 30 sn'de bir 4xx yiyen POST atmak yerine
// "Yazma URL'i girin" der; "on" operatörün açık kararıdır ve dener.
func SelfMetricsState(cfg Settings) (bool, string) {
	mode := NormalizeSelfMetrics(cfg.SelfMetrics)
	if mode == SelfMetricsOff {
		return false, "kapalı"
	}
	if WriteTarget(cfg) == "" {
		return false, "yazım hedefi yok (Base URL / Yazma URL'i boş)"
	}
	if mode == SelfMetricsOn {
		return true, ""
	}
	if !cfg.Enabled {
		return false, "otomatik: VictoriaMetrics okuması kapalı"
	}
	if strings.TrimSpace(cfg.WriteURL) == "" && strings.Contains(cfg.BaseURL, "/select/") {
		return false, "otomatik: Base URL vmselect — cluster kurulumunda Yazma URL'i (vminsert …/insert/<tenant>) girin"
	}
	return true, ""
}

// SelfMetricsActive — selfobs.VMSink; her export turunda okunur (PUT ile
// açılır/kapanır, restart yok).
func (s *Service) SelfMetricsActive() bool {
	if s == nil {
		return false
	}
	ok, _ := SelfMetricsState(s.CurrentSettings())
	return ok
}

// WriteSelfOTLP — selfobs.VMSink: sıkıştırılmamış gövde, gzip burada.
func (s *Service) WriteSelfOTLP(ctx context.Context, body []byte) error {
	if s == nil {
		return ErrNoWrite
	}
	s.mu.RLock()
	cfg, cli, tok := s.cfg, s.writeHTTP, effectiveToken(s.cfg, s.resolvedToken)
	s.mu.RUnlock()
	if ok, _ := SelfMetricsState(cfg); !ok {
		return ErrNoWrite
	}
	if cli == nil {
		cli = newWriteClient(cfg)
	}
	return writeOTLPWith(ctx, cli, cfg, tok, body, false)
}
