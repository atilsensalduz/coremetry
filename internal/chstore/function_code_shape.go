package chstore

// function_code_shape.go — v0.10.1226 — fonksiyon kodu BİÇİM ölçümü.
//
// Kuyruk maddesi (v0.10.1207 civarı tartışma): function_code'da milyonlarca
// farklı değer görülüyordu; gerçek iş kodları FunctionCodeShape biçiminde
// ve birkaç bin. business_code_summary_5m yalnız FunctionCodeShape kapısından
// geçenleri saklar; kapının dışında kalan kuyruğun boyu burada ölçülür. Bu okuma (biçime uyuyor mu × uzunluk) başına farklı değer ve
// span sayısı ile bir örnek döndürür. Aynı iki yol, sırayla: geniş rollup
// (MV-first) → terfi kolonu attr_function_code. Pencere son 1 saat.

import (
	"context"
	"fmt"
	"time"
)

type FunctionCodeShapeRow struct {
	Conforms bool   `json:"conforms"`
	Len      int    `json:"len"`
	Distinct uint64 `json:"distinct"`
	Spans    uint64 `json:"spans"`
	Example  string `json:"example"`
}

type FunctionCodeShapeReport struct {
	Source string                 `json:"source"` // rollup | spans | "" (okuma yolu yok)
	From   int64                  `json:"from"`   // unix ms
	To     int64                  `json:"to"`
	Rows   []FunctionCodeShapeRow `json:"rows"`
}

// functionCodeShapeSQL — SAF. expr: kolon ifadesi; count: span sayısı ifadesi.
func functionCodeShapeSQL(table, tsCol, expr, count string) string {
	return fmt.Sprintf(`
		SELECT match(%[3]s, '%[5]s') AS ok, toInt32(length(%[3]s)) AS len,
		       uniq(%[3]s) AS d, %[4]s AS n, any(%[3]s) AS ex
		FROM %[1]s
		WHERE %[2]s >= ? AND %[2]s < ? AND %[3]s != ''
		GROUP BY ok, len
		ORDER BY d DESC
		LIMIT 60
		SETTINGS max_execution_time = 20`, table, tsCol, expr, count, FunctionCodeShape)
}

func (s *Store) FunctionCodeShapeStats(ctx context.Context, now time.Time) (FunctionCodeShapeReport, error) {
	to := now
	from := to.Add(-time.Hour)
	out := FunctionCodeShapeReport{From: from.UnixMilli(), To: to.UnixMilli(), Rows: []FunctionCodeShapeRow{}}
	var query string
	if ok, err := s.rollupTableExists(ctx, "rollup_spans_wide_1m"); err != nil {
		return out, err
	} else if ok {
		out.Source = FunctionCodeSourceRollup
		query = functionCodeShapeSQL("rollup_spans_wide_1m", "ts", "function_code", "sum(span_count)")
	} else if col, colArgs, found := promotedAttrResolve(functionCodeAttrKey); found && len(colArgs) == 0 {
		out.Source = FunctionCodeSourceSpans
		query = functionCodeShapeSQL("spans", "time", col, "count()")
	} else {
		return out, nil
	}
	rows, err := s.telemetryReadConn().Query(ctx, query, from, to)
	if err != nil {
		return out, fmt.Errorf("fonksiyon kodu biçimi (%s): %w", out.Source, err)
	}
	defer rows.Close()
	for rows.Next() {
		var r FunctionCodeShapeRow
		var ok uint8
		if err := rows.Scan(&ok, &r.Len, &r.Distinct, &r.Spans, &r.Example); err != nil {
			return out, err
		}
		r.Conforms = ok == 1
		out.Rows = append(out.Rows, r)
	}
	return out, rows.Err()
}
