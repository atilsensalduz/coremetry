package chstore

// operation_catalog.go — CoSRE operasyon tespiti Faz 1 (DDL: store.go
// "cosre_operation_catalog"). Doldurma sunucu-içi INSERT…SELECT (veri
// istemciye gelmez); okuma Faz 2'nin eşleyicisi için.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const operationCatalogWindow = 24 * time.Hour

// spanOperationCatalogSQL — SAF: MV'den (ham span DEĞİL) son 24 sa.
const spanOperationCatalogSQL = `
	INSERT INTO cosre_operation_catalog (operation, source, service, function_codes, last_seen, call_count, error_count)
	SELECT name, 'span', service_name, [], max(time_bucket), countMerge(span_count_state), countMerge(error_count_state)
	FROM operation_summary_5m
	WHERE time_bucket >= ? AND name != ''
	GROUP BY service_name, name
	LIMIT 500000
	SETTINGS max_execution_time = 25`

// endpointCatalogSQL — SAF: spanmetrics_1m'den (servis × http_route) son 24 sa
// (resolve_business_code endpoint ekseni). Ham span DEĞİL.
const endpointCatalogSQL = `
	INSERT INTO cosre_operation_catalog (operation, source, service, function_codes, last_seen, call_count, error_count)
	SELECT http_route, 'endpoint', service_name, [], max(time_bucket), countMerge(calls_state), countMerge(error_state)
	FROM spanmetrics_1m
	WHERE time_bucket >= ? AND http_route != ''
	GROUP BY service_name, http_route
	LIMIT 500000
	SETTINGS max_execution_time = 25`

// oracleOperationCatalogSQL — SAF: oracle_error_log son 24 sa; fonksiyon
// kodu ifadesi oracleOpHitsSQL ile AYNI (kaynağın `code` alanı fonksiyon
// koduysa error_code, değilse attribute). service = kaynak kimliği.
func oracleOperationCatalogSQL(nCodeSources int) string {
	fc := oracleFunctionCodeAttrExpr
	if nCodeSources > 0 {
		holders := strings.TrimSuffix(strings.Repeat("?,", nCodeSources), ",")
		fc = "if(source_id IN (" + holders + "), trimBoth(error_code), " + oracleFunctionCodeAttrExpr + ")"
	}
	return `
	INSERT INTO cosre_operation_catalog (operation, source, service, function_codes, last_seen, call_count, error_count)
	SELECT operation_code, 'oracle', anyHeavy(source_id),
	       arraySlice(arrayFilter(x -> x != '', groupUniqArray(8)(fc)), 1, 5), max(time), count(), count()
	FROM (
		SELECT operation_code, source_id, time, ` + fc + ` AS fc
		FROM oracle_error_log
		WHERE time >= ? AND operation_code != ''
	)
	GROUP BY operation_code
	LIMIT 200000
	SETTINGS max_execution_time = 25`
}

// RefreshOperationCatalog — iki kaynağı yeniden yazar; Oracle tablosu yoksa
// (kaynak yapılandırılmamış) yalnız span tarafı (oracleErr döner, span yazıldı).
func (s *Store) RefreshOperationCatalog(ctx context.Context, codeSources []string, now time.Time) (spanErr, oracleErr error) {
	from := chDateTime64Arg(now.Add(-operationCatalogWindow))
	spanErr = s.conn.Exec(ctx, spanOperationCatalogSQL, from)
	if err := s.conn.Exec(ctx, endpointCatalogSQL, from); err != nil && spanErr == nil {
		spanErr = fmt.Errorf("endpoint catalog: %w", err)
	}
	args := make([]any, 0, len(codeSources)+1)
	for _, id := range codeSources {
		args = append(args, id)
	}
	args = append(args, from)
	oracleErr = s.conn.Exec(ctx, oracleOperationCatalogSQL(len(codeSources)), args...)
	return spanErr, oracleErr
}

// CatalogOperation — katalog satırı.
type CatalogOperation struct {
	Operation     string    `json:"operation"`
	Source        string    `json:"source"`
	Service       string    `json:"service"`
	FunctionCodes []string  `json:"functionCodes"`
	LastSeen      time.Time `json:"lastSeen"`
	CallCount     uint64    `json:"callCount"`
	ErrorCount    uint64    `json:"errorCount"`
}

// CatalogCandidates — eşleyicinin aday listesi: verilen belirteçlerden
// herhangi birini (harf duyarsız) İÇEREN operasyonlar; sıralama/eşik
// kararı saf eşleyicide (internal/opmatch). Belirteç ≥4 karakter, ≤12.
func (s *Store) CatalogCandidates(ctx context.Context, tokens []string, limit int) ([]CatalogOperation, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	preds := make([]string, len(tokens))
	args := make([]any, 0, len(tokens)+1)
	for i, t := range tokens {
		preds[i] = "positionCaseInsensitiveUTF8(operation, ?) > 0"
		args = append(args, t)
	}
	args = append(args, limit)
	rows, err := s.conn.Query(ctx, `
		SELECT operation, source, service, function_codes, last_seen, call_count, error_count
		FROM cosre_operation_catalog FINAL
		WHERE `+strings.Join(preds, " OR ")+`
		ORDER BY call_count DESC
		LIMIT ?
		SETTINGS max_execution_time = 3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CatalogOperation
	for rows.Next() {
		var c CatalogOperation
		if err := rows.Scan(&c.Operation, &c.Source, &c.Service, &c.FunctionCodes, &c.LastSeen, &c.CallCount, &c.ErrorCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// catalogOperationSearchSQL — v0.10.1390 SAF: CoSRE başlangıç kartındaki
// operasyon arama kutusunun okuması (api/copilot_operation_search.go).
// Kaynak sohbetin ön-getirmesiyle AYNI katalog (CatalogCandidates) → seçilen
// ad orada birebir eşleşir. Yalnız span + oracle (endpoint satırı yol; o
// endpoint anma aşamasının işi). Boşluklu / <4 karakter ad SQL'de elenir
// (belirteç kuralı; kesin süzgeç çağıranda). Sıra: birebir → önek → son 24
// sa hata → çağrı.
const catalogOperationSearchSQL = `
	SELECT operation
	FROM cosre_operation_catalog FINAL
	WHERE source IN ('span', 'oracle')
	  AND positionCaseInsensitiveUTF8(operation, ?) > 0
	  AND lengthUTF8(operation) >= 4
	  AND NOT match(operation, '\\s')
	GROUP BY operation
	ORDER BY lowerUTF8(operation) = lowerUTF8(?) DESC, startsWith(lowerUTF8(operation), lowerUTF8(?)) DESC,
	         sum(error_count) DESC, sum(call_count) DESC, operation
	LIMIT ?
	SETTINGS max_execution_time = 3`

// SearchCatalogOperations — v0.10.1390: katalogda alt-dizge araması (harf
// duyarsız), en çok `limit` tekil ad. q boşsa CH'ye gidilmez.
func (s *Store) SearchCatalogOperations(ctx context.Context, q string, limit int) ([]string, error) {
	q = strings.TrimSpace(q)
	if q == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := s.conn.Query(ctx, catalogOperationSearchSQL, q, q, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var op string
		if err := rows.Scan(&op); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}
