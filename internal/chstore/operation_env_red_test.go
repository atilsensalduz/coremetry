package chstore

// v0.10.1374 (operator-reported, CoSRE küme kıyası) — "endpoint hem eu-a hem
// tr-c kümelerinde çağrılıyor, tekrar eden DB çağrılarına bak": ortam başına
// RED. MV'lerde deploy_env yok → v0.8.385 env ham geri dönüşü (MV değişikliği
// YOK). SQL pini: TEK ham sorgu GROUP BY deploy_env, servis anahtar öneki,
// ortam IN, giriş-noktası kind yüklemi, yarı-açık zaman, LIMIT 16,
// max_execution_time 10; p95 2., p50 1. kuantil (Scan: env, calls, errors,
// p95, p50). Adlar SENTETİK.

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRouteREDByEnvQuery(t *testing.T) {
	from := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	to := from.Add(3 * time.Hour)
	envs := []string{"eu-a", "tr-c"}
	q, args := routeREDByEnvQuery("orders-api-prod", "/shop/orders/detail", "GET /shop/orders/detail", envs, from, to)
	common := []string{
		"SELECT deploy_env,",
		"count(),",
		"countIf(status_code = 'error'),",
		"arrayElement(quantilesTDigest(0.5, 0.95)(duration), 2) / 1e6,",
		"arrayElement(quantilesTDigest(0.5, 0.95)(duration), 1) / 1e6",
		"FROM spans",
		"WHERE service_name = ? AND deploy_env IN (?)",
		"kind NOT IN ('client', 'producer')",
		"time >= ? AND time < ?",
		"GROUP BY deploy_env",
		"LIMIT 16",
		"SETTINGS max_execution_time = 10",
	}
	for _, want := range append(common, "AND (http_route = ? OR name = ?)") {
		if !strings.Contains(q, want) {
			t.Errorf("SQL %q taşımalı:\n%s", want, q)
		}
	}
	for _, banned := range []string{"spanmetrics_1m", "operation_summary_5m", "coremetry."} {
		if strings.Contains(q, banned) {
			t.Errorf("SQL %q taşımamalı (MV env boyutu yok; tablo niteliksiz):\n%s", banned, q)
		}
	}
	if want := []any{"orders-api-prod", envs, "/shop/orders/detail", "GET /shop/orders/detail", from, to}; !reflect.DeepEqual(args, want) {
		t.Errorf("bind'ler %v, want %v", args, want)
	}
	if i, j := strings.Index(q, "), 2) / 1e6"), strings.Index(q, "), 1) / 1e6"); i < 0 || j < 0 || i > j {
		t.Errorf("kolon sırası p95 sonra p50 olmalı (p95=%d p50=%d)", i, j)
	}
	// Tek eşleşme kolu.
	if q, args := routeREDByEnvQuery("orders-api-prod", "/shop/x", "", envs, from, to); !strings.Contains(q, "AND http_route = ?\n") || strings.Contains(q, "AND name = ?") || strings.Contains(q, "OR name") || len(args) != 5 {
		t.Errorf("yalnız rota: %s %v", q, args)
	}
	if q, args := routeREDByEnvQuery("orders-api-prod", "", "GET /shop/x", envs, from, to); !strings.Contains(q, "AND name = ?\n") || strings.Contains(q, "http_route") || len(args) != 5 {
		t.Errorf("yalnız ad: %s %v", q, args)
	}
	if q, _ := routeREDByEnvQuery("orders-api-prod", "", "", envs, from, to); q != "" {
		t.Errorf("eşleşme kolu yoksa sorgu yok: %s", q)
	}
	if q, _ := routeREDByEnvQuery("orders-api-prod", "/x", "", nil, from, to); q != "" {
		t.Errorf("ortam yoksa sorgu yok: %s", q)
	}

	// Yol-yalnız: url.path / http.target ('?' öncesi) birebir.
	pq, pargs := pathREDByEnvQuery("orders-api-prod", "/shop/orders/detail", envs, from, to)
	for _, want := range append(common,
		"attr_values[indexOf(attr_keys, 'url.path')] = ?",
		"splitByChar('?', attr_values[indexOf(attr_keys, 'http.target')])[1] = ?") {
		if !strings.Contains(pq, want) {
			t.Errorf("yol SQL'i %q taşımalı:\n%s", want, pq)
		}
	}
	if want := []any{"orders-api-prod", envs, "/shop/orders/detail", "/shop/orders/detail", from, to}; !reflect.DeepEqual(pargs, want) {
		t.Errorf("yol bind'leri %v, want %v", pargs, want)
	}
	if q, _ := pathREDByEnvQuery("orders-api-prod", "", envs, from, to); q != "" {
		t.Errorf("boş yol → sorgu yok: %s", q)
	}

	// Servissiz ham sorgu koşmaz (birincil anahtar budaması yok) — bağlantıya dokunmadan döner.
	s := &Store{}
	if _, err := s.RouteREDByEnv(context.Background(), "", "/x", "", envs, from, to); err == nil {
		t.Error("servissiz RouteREDByEnv hata dönmeli")
	}
	if _, err := s.PathREDByEnv(context.Background(), "", "/x", envs, from, to); err == nil {
		t.Error("servissiz PathREDByEnv hata dönmeli")
	}
}
