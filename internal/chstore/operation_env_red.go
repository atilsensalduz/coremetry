package chstore

// operation_env_red.go — v0.10.1374 (operator-reported, CoSRE küme kıyası):
// iki bölgeden servis edilen bir endpoint için ortam (deploy_env) başına RED.
// spanmetrics_1m / operation_summary_5m deploy_env boyutu TAŞIMAZ; env süzgeçli
// endpoint okumaları v0.8.385 kararıyla ham spans'a düşer (GetEndpoints
// forcesRaw, TraceFilter.Env) — MV DEĞİŞİKLİĞİ YOK. Bu dosya aynı sınırlı ham
// geri dönüşü TEK sorguya indirir: GROUP BY deploy_env, servis birincil anahtar
// öneki (spans ORDER BY (service_name, time)), yarı-açık zaman penceresi
// (çağıran ≤6 sa'e kırpar), LIMIT 16, max_execution_time 10. Servis boşsa
// sorgu KOŞMAZ (birincil anahtar budaması yok — çağıran "ölçülemedi" der).

import (
	"context"
	"errors"
	"time"
)

// errEnvREDNoService — v0.10.1374: servissiz ham RED sorgusu reddedilir.
var errEnvREDNoService = errors.New("env RED: service required (raw spans read is bounded by the service_name key prefix)")

// envREDSelect — v0.10.1374: ortak SELECT listesi (FROM her sorguda, LIMIT +
// max_execution_time ile aynı literalde — make audit CHECK 6). Scan sırası:
// env, calls, errors, p95, p50. Kuantil tdigest (bellek sınırlı; getEndpointsRaw
// ile aynı aile).
const envREDSelect = `SELECT deploy_env,
	             count(),
	             countIf(status_code = 'error'),
	             arrayElement(quantilesTDigest(0.5, 0.95)(duration), 2) / 1e6,
	             arrayElement(quantilesTDigest(0.5, 0.95)(duration), 1) / 1e6`

// RouteREDByEnv — v0.10.1374: (servis, http_route | span adı) için ortam
// başına RED. route ve name'den en az biri dolu olmalı; ikisi doluysa OR.
// Dönen haritada olmayan ortam = pencerede span yok.
func (s *Store) RouteREDByEnv(ctx context.Context, service, route, name string, envs []string, from, to time.Time) (map[string]OpRED, error) {
	if service == "" {
		return nil, errEnvREDNoService
	}
	q, args := routeREDByEnvQuery(service, route, name, envs, from, to)
	if q == "" {
		return map[string]OpRED{}, nil
	}
	return s.scanEnvRED(ctx, q, args)
}

// PathREDByEnv — v0.10.1374: katalogda olmayan (yol-yalnız) endpoint'in
// ortam başına RED'i: url.path ya da http.target (sorgu dizesi atılmış)
// attribute'u BİREBİR yol. Çağıran yalnız somut yolda (elips/joker yok) ve
// tek serviste çağırır.
func (s *Store) PathREDByEnv(ctx context.Context, service, path string, envs []string, from, to time.Time) (map[string]OpRED, error) {
	if service == "" {
		return nil, errEnvREDNoService
	}
	q, args := pathREDByEnvQuery(service, path, envs, from, to)
	if q == "" {
		return map[string]OpRED{}, nil
	}
	return s.scanEnvRED(ctx, q, args)
}

func (s *Store) scanEnvRED(ctx context.Context, q string, args []any) (map[string]OpRED, error) {
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]OpRED{}
	for rows.Next() {
		var env string
		var r OpRED
		var p95, p50 float64
		if err := rows.Scan(&env, &r.Calls, &r.Errors, &p95, &p50); err != nil {
			return nil, err
		}
		if p95 == p95 { // NaN korunmaz
			r.P95Ms = p95
		}
		if p50 == p50 {
			r.P50Ms = p50
		}
		r.Measured = true
		out[env] = r
	}
	return out, rows.Err()
}

// routeREDByEnvQuery — SAF (v0.10.1374): RouteREDByEnv'in SQL'i + bind'leri.
// Giriş-noktası kind yüklemi routeREDQuery ile AYNI (client/producer hariç).
// route ve name ikisi de boşsa ya da ortam yoksa "" (sorgu yok).
func routeREDByEnvQuery(service, route, name string, envs []string, from, to time.Time) (string, []any) {
	var match string
	var margs []any
	switch {
	case route != "" && name != "":
		match, margs = "(http_route = ? OR name = ?)", []any{route, name}
	case route != "":
		match, margs = "http_route = ?", []any{route}
	case name != "":
		match, margs = "name = ?", []any{name}
	default:
		return "", nil
	}
	if len(envs) == 0 {
		return "", nil
	}
	q := envREDSelect + `
	      FROM spans
	      WHERE service_name = ? AND deploy_env IN (?) AND ` + match + `
	        AND kind NOT IN ('client', 'producer')
	        AND time >= ? AND time < ?
	      GROUP BY deploy_env
	      LIMIT 16
	      SETTINGS max_execution_time = 10`
	args := append([]any{service, envs}, margs...)
	return q, append(args, from, to)
}

// pathREDByEnvQuery — SAF (v0.10.1374): PathREDByEnv'in SQL'i. url.path ya da
// http.target'ın '?' öncesi BİREBİR yol (getEndpointsRaw pathExpr'inin 3. ve 4.
// katmanı); kind yüklemi routeREDByEnvQuery ile aynı.
func pathREDByEnvQuery(service, path string, envs []string, from, to time.Time) (string, []any) {
	if path == "" || len(envs) == 0 {
		return "", nil
	}
	q := envREDSelect + `
	      FROM spans
	      WHERE service_name = ? AND deploy_env IN (?)
	        AND (attr_values[indexOf(attr_keys, 'url.path')] = ?
	             OR splitByChar('?', attr_values[indexOf(attr_keys, 'http.target')])[1] = ?)
	        AND kind NOT IN ('client', 'producer')
	        AND time >= ? AND time < ?
	      GROUP BY deploy_env
	      LIMIT 16
	      SETTINGS max_execution_time = 10`
	return q, []any{service, envs, path, path, from, to}
}
