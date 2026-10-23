package chstore

// exception_sample_stacks.go — v0.10.1486: servis exception cevabının
// "örnek stack" sütunu. GetExceptions grup başına örnek (trace_id, span_id)
// ve o örneğin zamanını (max(time) = argMax örneği) zaten döndürüyor; stack
// metni orada YOK (ex_stack ağır kolon — tüm pencerenin exception
// satırlarında okumak gereksiz). Bu okuma yalnız seçilen örnek span'lerin
// stack'ini TEK sorguda çeker: servis + örnek başına ±1 sn'lik zaman dilimi
// (birincil anahtar budaması) + trace/span eşitliği + LIMIT +
// max_execution_time; metin SQL'de kırpılır (taşıma sınırı).

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// ExceptionSpanRef — örnek span işaretçisi (ExceptionRow'dan).
type ExceptionSpanRef struct {
	TraceID string
	SpanID  string
	TimeNs  int64 // örneğin zamanı (ExceptionRow.LastSeen)
}

const (
	// ExceptionSampleStackMax — tek okumada en çok örnek (çağıran da kırpar).
	ExceptionSampleStackMax = 8
	// exceptionSampleStackBytes — örnek başına taşınan stack öneki. Uygulama
	// çerçeveleri derin stack'lerde de ilk birkaç KB içinde değil, "Caused by"
	// bölümündedir; 16 KB tipik JBoss/Spring zincirini kapsar.
	exceptionSampleStackBytes = 16384
	// exceptionSampleSlack — örnek zamanının iki yanındaki pay.
	exceptionSampleSlack = time.Second
)

// ExceptionSampleKey — sonuç haritasının anahtarı.
func ExceptionSampleKey(traceID, spanID string) string { return traceID + "/" + spanID }

// exceptionSampleStacksQuery — SAF: n örneklik sorgu (testli sınırlar).
func exceptionSampleStacksQuery(n int, hasCols bool) string {
	f := exFragments(hasCols)
	ors := make([]string, n)
	for i := range ors {
		ors[i] = "(trace_id = ? AND span_id = ? AND time >= ? AND time <= ?)"
	}
	return `
		SELECT trace_id, span_id, substring(` + f.Stack + `, 1, ` + strconv.Itoa(exceptionSampleStackBytes) + `) AS stacktrace
		FROM spans
		WHERE service_name = ?
		  AND time >= ? AND time <= ?
		  AND (` + strings.Join(ors, " OR ") + `)
		  AND ` + f.Match + `
		LIMIT 1 BY trace_id, span_id
		LIMIT ` + strconv.Itoa(n) + `
		SETTINGS max_execution_time = 5`
}

// ExceptionSampleStacks — örnek span'lerin stack metni (anahtar
// ExceptionSampleKey). Boş/geçersiz işaretçiler atlanır; en çok
// ExceptionSampleStackMax örnek. Bulunamayan örnek haritada yoktur.
func (s *Store) ExceptionSampleStacks(ctx context.Context, service string, refs []ExceptionSpanRef) (map[string]string, error) {
	var use []ExceptionSpanRef
	for _, r := range refs {
		if r.TraceID == "" || r.SpanID == "" || r.TimeNs <= 0 {
			continue
		}
		use = append(use, r)
		if len(use) >= ExceptionSampleStackMax {
			break
		}
	}
	out := map[string]string{}
	if len(use) == 0 || service == "" {
		return out, nil
	}
	lo, hi := time.Unix(0, use[0].TimeNs), time.Unix(0, use[0].TimeNs)
	pairs := make([]any, 0, 4*len(use))
	for _, r := range use {
		t := time.Unix(0, r.TimeNs)
		if t.Before(lo) {
			lo = t
		}
		if t.After(hi) {
			hi = t
		}
		pairs = append(pairs, r.TraceID, r.SpanID, t.Add(-exceptionSampleSlack), t.Add(exceptionSampleSlack))
	}
	args := append([]any{service, lo.Add(-exceptionSampleSlack), hi.Add(exceptionSampleSlack)}, pairs...)
	rows, err := s.conn.Query(ctx, exceptionSampleStacksQuery(len(use), s.hasExCols), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tid, sid, st string
		if err := rows.Scan(&tid, &sid, &st); err != nil {
			return nil, err
		}
		out[ExceptionSampleKey(tid, sid)] = st
	}
	return out, rows.Err()
}
