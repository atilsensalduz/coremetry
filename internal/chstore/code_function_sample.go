package chstore

// code_function_sample.go — v0.10.1276 (CoSRE adım 7): service_health_sweep'in
// DERİN kipinde "fonksiyon" katmanının kod yarısı.
//
// code.function / code.function.name / code.namespace OTel semconv öznitelikleri
// hiçbir MV'de ve terfi kolonunda yok (yalnız attr_keys/attr_values dizisinde).
// Bu yüzden okuma ÖRNEKLEMDİR, toplam DEĞİL: taramanın sorunlu bulduğu en çok
// üç operasyonun HATALI ya da YAVAŞ span'lerinden iç LIMIT 5000 satır alınır,
// fonksiyon adına göre gruplanır. Pencere ≤ 1 sa (çağıran kırpar, burada da
// kelepçe), max_execution_time 3, zaman-sınırlı WHERE + service_name ön eki
// (ORDER BY ile partition/granül budaması). Sonuç "sampled" etiketiyle
// sunulur — sayılar örneklem içi orandır, servis toplamı değil.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CodeFunctionSlice — örneklemde bir kod fonksiyonunun ağırlığı.
type CodeFunctionSlice struct {
	Function  string  `json:"function"`
	Namespace string  `json:"namespace,omitempty"`
	Calls     uint64  `json:"calls"`
	Errors    uint64  `json:"errors"`
	P99Ms     float64 `json:"p99Ms"`
}

// Örneklem tavanları.
const (
	codeFnSampleInner   = 5000
	codeFnSampleOut     = 10
	codeFnSampleMaxOps  = 3
	codeFnSampleMaxWin  = time.Hour
	codeFnSampleSlowMs  = 1000 // hata yoksa "yavaş" eşiği: ≥ 1 sn
	codeFnSampleTimeout = 3
)

// codeFunctionSampleSQL — SAF: SQL + bind argümanları (sıra = placeholder
// sırası). ops boş olamaz (çağıran denetler); en çok codeFnSampleMaxOps.
func codeFunctionSampleSQL(service string, ops []string, from, to time.Time) (string, []any) {
	if len(ops) > codeFnSampleMaxOps {
		ops = ops[:codeFnSampleMaxOps]
	}
	if to.Sub(from) > codeFnSampleMaxWin {
		from = to.Add(-codeFnSampleMaxWin)
	}
	ph := strings.TrimSuffix(strings.Repeat("?, ", len(ops)), ", ")
	sql := fmt.Sprintf(`
		SELECT fn, ns,
		       count()                                 AS calls,
		       countIf(is_err)                         AS errors,
		       quantile(0.99)(duration) / 1e6          AS p99_ms
		FROM (
			SELECT if(attr_values[indexOf(attr_keys, 'code.function.name')] != '',
			          attr_values[indexOf(attr_keys, 'code.function.name')],
			          attr_values[indexOf(attr_keys, 'code.function')])  AS fn,
			       attr_values[indexOf(attr_keys, 'code.namespace')]     AS ns,
			       status_code = 'error'                                 AS is_err,
			       duration
			FROM spans
			WHERE service_name = ?
			  AND time >= ?
			  AND time < ?
			  AND name IN (%s)
			  AND (status_code = 'error' OR duration >= ?)
			  AND (has(attr_keys, 'code.function') OR has(attr_keys, 'code.function.name'))
			LIMIT %d
		)
		WHERE fn != ''
		GROUP BY fn, ns
		ORDER BY errors DESC, calls DESC
		LIMIT %d
		SETTINGS max_execution_time = %d`, ph, codeFnSampleInner, codeFnSampleOut, codeFnSampleTimeout)
	args := make([]any, 0, 4+len(ops))
	args = append(args, service, from, to)
	for _, o := range ops {
		args = append(args, o)
	}
	args = append(args, uint64(codeFnSampleSlowMs)*1_000_000)
	return sql, args
}

// CodeFunctionSample — servis + sorunlu operasyonlar için örneklenmiş kod
// fonksiyonu kırılımı. ops boş → (nil, nil): okunacak bir şey yok.
func (s *Store) CodeFunctionSample(ctx context.Context, service string, ops []string, from, to time.Time) ([]CodeFunctionSlice, error) {
	if strings.TrimSpace(service) == "" || len(ops) == 0 || !to.After(from) {
		return nil, nil
	}
	sql, args := codeFunctionSampleSQL(service, ops, from, to)
	rows, err := s.telemetryReadConn().Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CodeFunctionSlice
	for rows.Next() {
		var c CodeFunctionSlice
		if err := rows.Scan(&c.Function, &c.Namespace, &c.Calls, &c.Errors, &c.P99Ms); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
