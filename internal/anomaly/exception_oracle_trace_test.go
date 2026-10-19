package anomaly

// exception_oracle_trace_test.go — v0.10.1103 (operatör: "Oracle hata grubu
// için varsa coremetry üzerindeki trace ve o trace loglarını da
// kullanabilsin"). Pinler: çözülen trace'lerden (en yeni önce) Coremetry'de
// BULUNAN ilki TEK trace olarak yüklenir; loglar YALNIZ trace yüklendiyse ve
// log deposu varsa okunur; prompt trace bloğunu ve log bloğunu yalnız
// yüklendiklerinde taşır (yoksa açık "yok" satırı); EvSpans / LogsBlock /
// DBStatements kardeş yoldaki gibi dolar, Stack BOŞ kalır.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestOracleCoremetryTrace(t *testing.T) {
	refs := []OracleTraceRef{{TraceID: "t1"}, {TraceID: "t2"}, {TraceID: "t3"}}
	cases := []struct {
		name  string
		found map[string]chstore.TraceFact
		want  string
	}{
		{"hiçbiri bulunmadı", map[string]chstore.TraceFact{}, ""},
		{"nil harita (çözüm düştü)", nil, ""},
		{"en yeni bulunan kazanır", map[string]chstore.TraceFact{"t3": {}, "t2": {Service: "svc-orders"}}, "t2"},
		{"servissiz bulunan da sayılır", map[string]chstore.TraceFact{"t1": {}}, "t1"},
		{"listede olmayan id yok sayılır", map[string]chstore.TraceFact{"tX": {}}, ""},
	}
	for _, c := range cases {
		if got := oracleCoremetryTrace(refs, c.found); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
}

func TestBuildOracleExceptionExplainInputCoremetryTrace(t *testing.T) {
	t.Cleanup(func() { oracleFactsFn.Store(nil) })
	SetOracleExplainFacts(func(chstore.ExceptionGroup) (OracleExplainFacts, bool) { return oraFacts(), true })
	g := oraGroup()
	id := func(n int) string { return fmt.Sprintf("%032x", n) } // oraRows: satır i → id(i%traces+1)
	spans := teSpans(12, 3)

	// v0.10.1370 (operatör-bildirimli: "Teknik hatalar için Exceptions
	// sayfasında trace incelemesi gibi loga bakmıyor") — örnek trace'lerin
	// logları span'e bağlı OLMADAN TEK çoklu-trace sorgusuyla okunur: wantMulti
	// o sorgunun id'leri (nil = sorgu yok), wantSingle tek-trace sorgusu.
	const traceHdr, logsHdr, sampleHdr = "Coremetry TRACE'i (", "Bu trace'in ilişkili LOGLARI", "Örnek trace'lerin LOGLARI"
	cases := []struct {
		name       string
		rows       []chstore.OracleErrorRow
		found      []string // nil → yalnız ids[0] (eski sahte)
		spanErr    error
		logs       bool
		wantTrace  string // GetTrace'e giden tek id ("" = çağrı yok)
		wantLoaded bool
		wantSingle bool
		wantMulti  []string
		wantIn     []string
		wantOut    []string
	}{
		{"trace Coremetry'de yok", oraRows(6, 3), []string{}, nil, true, "", false, false, []string{id(1), id(2), id(3)},
			[]string{"Coremetry trace'i: yok (satırların trace id'leri Coremetry'de bulunamadı ya da okunamadı)",
				sampleHdr + " (trace id ile, 3 trace soruldu / 2 trace'te log bulundu; yüksek severity önce):",
				"trace " + id(1)[:12] + "… (Coremetry'de yok, 8 satır)", "trace " + id(3)[:12] + "… (Coremetry'de yok): log bulunamadı",
				"svc-caller", "logu atan servis olası çağırandır"},
			[]string{traceHdr, logsHdr}},
		{"satırlar trace id taşımıyor", oraRowsNoTrace(4), nil, nil, true, "", false, false, nil,
			[]string{"Coremetry trace'i: yok (satırlar trace id taşımıyor)"},
			[]string{traceHdr, logsHdr, sampleHdr, "Çözülen trace'ler"}},
		{"en yeni bulunan yüklenir, log deposu yok", oraRows(6, 3), []string{id(3), id(2)}, nil, false, id(2), true, false, nil,
			[]string{traceHdr, "trace; " + id(2) + ", servis svc-orders, 12 span", "Bu trace'in logları: bulunamadı", "çağıran servisi ve operasyonu"},
			[]string{logsHdr, sampleHdr, "Coremetry trace'i: yok"}},
		{"trace + loglar", oraRows(6, 3), []string{id(1)}, nil, true, id(1), true, true, []string{id(2), id(3)},
			[]string{traceHdr, "trace; " + id(1), logsHdr, "ORA-00001", "svc-ledger",
				sampleHdr + " (trace id ile, 2 trace soruldu / 1 trace'te log bulundu", "trace " + id(2)[:12] + "… (Coremetry'de yok, 1 satır)"},
			[]string{"Bu trace'in logları: bulunamadı", "Coremetry trace'i: yok"}},
		{"trace okunamadı → tek-trace sorgusu YOK, örnek loglar okunur", oraRows(6, 3), []string{id(1)}, errors.New("ch down"), true, id(1), false, false,
			[]string{id(1), id(2), id(3)},
			[]string{"Coremetry trace'i: yok (satırların trace id'leri Coremetry'de bulunamadı ya da okunamadı)",
				"trace " + id(1)[:12] + "… (clickhouse, 8 satır)", "trace " + id(2)[:12] + "… (Coremetry'de yok, 1 satır)"},
			[]string{traceHdr, logsHdr}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rd := &fakeOracleReader{rows: tc.rows, found: tc.found, getTraceErr: tc.spanErr,
				spans: map[string][]chstore.SpanRow{id(1): spans, id(2): spans, id(3): spans}}
			var logs logstore.Store
			var lf *fakeTraceLogs
			if tc.logs {
				lf = &fakeTraceLogs{mkF: oraLogsByFilter}
				logs = lf
			}
			in := BuildOracleExceptionExplainInput(context.Background(), rd, logs, g, time.UTC)

			if tc.wantTrace == "" && len(rd.gotTraces) != 0 || tc.wantTrace != "" && (len(rd.gotTraces) != 1 || rd.gotTraces[0] != tc.wantTrace) {
				t.Fatalf("GetTrace TEK bulunan id'ye gitmeli: %v, beklenen %q", rd.gotTraces, tc.wantTrace)
			}
			if lf != nil {
				var single, multi []logstore.Filter
				for _, f := range lf.all {
					if len(f.TraceIDs) > 0 {
						multi = append(multi, f)
					} else {
						single = append(single, f)
					}
				}
				if tc.wantSingle != (len(single) == 1) || len(single) > 1 {
					t.Fatalf("tek-trace sorgusu %d, beklenen %v", len(single), tc.wantSingle)
				}
				if len(single) == 1 && (single[0].TraceID != tc.wantTrace || single[0].Limit != traceLogsFetch) {
					t.Fatalf("tek-trace süzgeci: %+v", single[0])
				}
				if tc.wantMulti == nil && len(multi) != 0 || tc.wantMulti != nil && len(multi) != 1 {
					t.Fatalf("çoklu-trace sorgusu %d, beklenen %v", len(multi), tc.wantMulti)
				}
				if len(multi) == 1 {
					m := multi[0]
					if fmt.Sprint(m.TraceIDs) != fmt.Sprint(tc.wantMulti) || m.Limit != sampleLogFetch || m.TraceID != "" {
						t.Fatalf("çoklu-trace süzgeci: ids=%v limit=%d", m.TraceIDs, m.Limit)
					}
					// Pencere sorulan id'lerin satır damgalarından ±2 dk (ilk görülen satır).
					var newest, oldest time.Time
					for _, want := range tc.wantMulti {
						for _, r := range tc.rows {
							if r.TraceID == want {
								if newest.IsZero() || r.Time.After(newest) {
									newest = r.Time
								}
								if oldest.IsZero() || r.Time.Before(oldest) {
									oldest = r.Time
								}
								break
							}
						}
					}
					if !m.From.Equal(oldest.Add(-2*time.Minute)) || !m.To.Equal(newest.Add(2*time.Minute)) {
						t.Fatalf("çoklu-trace penceresi: %s → %s", m.From, m.To)
					}
				}
				if want := len(tc.wantMulti); want > 0 && (in.Oracle.SampleLogTraces == 0 || in.Oracle.SampleLogLines == 0) {
					t.Fatalf("örnek log sayaçları: traces=%d lines=%d", in.Oracle.SampleLogTraces, in.Oracle.SampleLogLines)
				}
			}
			for _, w := range tc.wantIn {
				if !strings.Contains(in.User, w) {
					t.Errorf("prompt %q içermeli:\n%s", w, in.User)
				}
			}
			for _, w := range tc.wantOut {
				if strings.Contains(in.User, w) {
					t.Errorf("prompt %q İÇERMEMELİ:\n%s", w, in.User)
				}
			}
			if in.Stack != "" || in.Oracle == nil {
				t.Fatalf("Oracle girdisi stack'siz kalmalı: stack=%q", in.Stack)
			}
			if tc.wantLoaded {
				if in.Oracle.CoremetryTrace != tc.wantTrace || in.TraceID != tc.wantTrace || in.Oracle.TraceSpans != 12 ||
					len(in.EvSpans) != 4 || len(in.DBStatements) == 0 {
					t.Fatalf("yüklenen trace alanları: ctx=%q trace=%q spans=%d ev=%v sql=%v",
						in.Oracle.CoremetryTrace, in.TraceID, in.Oracle.TraceSpans, in.EvSpans, in.DBStatements)
				}
				// Trace bloğu çözülen trace'lerin JSON'undan SONRA, örnek satırlardan ÖNCE.
				if i, j, k := strings.Index(in.User, "Çözülen trace'ler"), strings.Index(in.User, traceHdr), strings.Index(in.User, "En yeni "); !(i < j && j < k) {
					t.Fatalf("blok sırası: çözülen=%d trace=%d örnek=%d", i, j, k)
				}
			} else if in.Oracle.CoremetryTrace != "" || len(in.EvSpans) != 0 || len(in.DBStatements) != 0 {
				t.Fatalf("yüklenmeyen trace kanıt bırakmamalı: %+v", in)
			}
			// Örnek log bloğu trace / "yok" satırından SONRA, örnek satırlardan ÖNCE.
			if k := strings.Index(in.User, sampleHdr); k >= 0 {
				if y, e := strings.Index(in.User, "Coremetry trace'i: yok"), strings.Index(in.User, "En yeni "); !(k < e && (y < 0 || y < k)) {
					t.Fatalf("örnek log bloğu sırası: yok=%d örnek-log=%d örnek-satır=%d", y, k, e)
				}
			}
			switch {
			case tc.wantSingle:
				if !strings.HasPrefix(in.LogsBlock, "\n\n"+logsHdr) || !strings.Contains(in.User, in.LogsBlock) || in.Oracle.LogLines != traceLogsLines {
					t.Fatalf("LogsBlock tek-trace bloğuyla başlayıp User'ın alt dizesi olmalı (%d satır)", in.Oracle.LogLines)
				}
				if tc.wantMulti != nil && !strings.Contains(in.LogsBlock, sampleHdr) {
					t.Fatal("LogsBlock örnek trace loglarını da taşımalı")
				}
			case tc.wantMulti != nil:
				if !strings.HasPrefix(in.LogsBlock, "\n\n"+sampleHdr) || !strings.Contains(in.User, in.LogsBlock) {
					t.Fatalf("LogsBlock örnek trace logları olmalı: %q", in.LogsBlock)
				}
			case in.LogsBlock != "":
				t.Fatalf("log bloğu yalnız loglar okunduysa: %q", in.LogsBlock)
			}
		})
	}
}

// oraLogsByFilter — v0.10.1370: tek-trace sorgusuna tePage; çoklu-trace
// sorgusunda id(1)'e 10 log (≤8 basılır), id(2)'ye 1 log, id(3)'e log yok.
func oraLogsByFilter(f logstore.Filter) *logstore.Page {
	if f.TraceID != "" {
		return tePage()
	}
	var lgs []*logstore.LogRecord
	for _, id := range f.TraceIDs {
		switch id {
		case fmt.Sprintf("%032x", 1):
			for i := 0; i < 10; i++ {
				lgs = append(lgs, &logstore.LogRecord{TraceID: id, Severity: 9, SeverityText: "INFO", ServiceName: "svc-caller",
					Timestamp: int64(i), Body: fmt.Sprintf("transfer adim %d", i)})
			}
		case fmt.Sprintf("%032x", 2):
			lgs = append(lgs, &logstore.LogRecord{TraceID: strings.ToUpper(id), Severity: 17, SeverityText: "ERROR", ServiceName: "svc-caller",
				Body: "transfer failed: " + teJavaStack, Attributes: map[string]string{"exception.type": "java.sql.SQLException"}})
		}
	}
	return &logstore.Page{Logs: lgs, Total: len(lgs)}
}

// oraRowsNoTrace — trace id'siz Oracle satırları.
func oraRowsNoTrace(n int) []chstore.OracleErrorRow {
	rows := oraRows(n, 1)
	for i := range rows {
		rows[i].TraceID = ""
	}
	return rows
}
