package chstore

// v0.10.1278 — operatör 2026-10-17: CoSRE karşılaması tüm hatalı span'leri
// sayıyordu ("Son 1 saatte 445.394 hata · 812 exception grubu" —
// "occurrences mı bu sayı?"). Pinler: hatalı istek = GİRİŞ span'i
// (server+consumer) spanmetrics_1m'den, ortam yüklemi ortam MV'sinde,
// sınırlar; açık grup = durum süzgeci (resolved/ignored dışarıda) + son
// pencere; yeni grup = first_seen eşiği.

import (
	"strings"
	"testing"
	"time"
)

func TestFleetFailedRequestsSQL_EntrySpansBounded(t *testing.T) {
	for name, q := range map[string]string{"all": fleetFailedRequestsSQL(), "env": fleetFailedRequestsEnvSQL()} {
		for _, want := range []string{
			"FROM spanmetrics_1m",
			"kind IN ('server','consumer')",
			"status_code = 'error'",
			"countMerge(error_state)",
			"time_bucket >= ? AND time_bucket < ?",
			"LIMIT 1",
			"max_execution_time = 10",
		} {
			if !strings.Contains(q, want) {
				t.Errorf("%s: %q yok:\n%s", name, want, q)
			}
		}
		// Eski kaynak (tüm-span) ve ham tablo geri gelmesin.
		for _, bad := range []string{"service_summary_5m", "FROM spans"} {
			if strings.Contains(q, bad) {
				t.Errorf("%s: %q olmamalı", name, bad)
			}
		}
	}
	if strings.Contains(fleetFailedRequestsSQL(), "deploy_env") {
		t.Error("tüm-ortam sorgusunda ortam yüklemi olmamalı")
	}
}

func TestFleetFailedRequestsEnvSQL_EnvPredicate(t *testing.T) {
	q := fleetFailedRequestsEnvSQL()
	for _, want := range []string{
		"FROM service_env_summary_5m",
		"sumIf(errs, deploy_env = ?)",
		"countIfMerge(error_count_state)",
		"HAVING env_errs > 0 AND all_errs > 0", // payda sıfır olamaz
		"e.failed * a.env_errs / a.all_errs",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("%q yok:\n%s", want, q)
		}
	}
	// Bağ sırası sözleşmesi: from,to (spanmetrics) · env · from,to (env MV).
	if got := strings.Count(q, "?"); got != 5 {
		t.Errorf("5 yer tutucu bekleniyordu, %d", got)
	}
	if strings.Index(q, "spanmetrics_1m") > strings.Index(q, "deploy_env = ?") ||
		strings.Index(q, "deploy_env = ?") > strings.Index(q, "FROM service_env_summary_5m") {
		t.Error("yer tutucu sırası FleetFailedRequests'in bağ sırasıyla uyuşmuyor")
	}
}

func TestRecentExceptionGroupWhere_OpenStatusAndWindow(t *testing.T) {
	from := time.Unix(1_800_000_000, 0)
	wc := recentExceptionGroupWhere(from, nil)
	w := wc.sql()
	if !strings.Contains(w, "state IN ('new','acknowledged','regressed')") {
		t.Errorf("açık durum süzgeci yok: %s", w)
	}
	for _, bad := range []string{"resolved", "ignored"} {
		if strings.Contains(w, bad) {
			t.Errorf("%q açık kümeye girmemeli: %s", bad, w)
		}
	}
	if !strings.Contains(w, "last_seen >= fromUnixTimestamp64Nano(?)") {
		t.Errorf("son-pencere süzgeci yok: %s", w)
	}
	if strings.Contains(w, "service IN") {
		t.Error("ortamsız çağrıda servis kısıtı olmamalı")
	}
	if len(wc.args) != 1 || wc.args[0] != from.UnixNano() {
		t.Errorf("args = %v", wc.args)
	}
	scoped := recentExceptionGroupWhere(from, []string{"a", "b"})
	if !strings.Contains(scoped.sql(), "service IN (?)") {
		t.Errorf("ortam üyeleri süzgeci yok: %s", scoped.sql())
	}
}

func TestRecentExceptionGroupCountsSQL_NewIsFirstSeen(t *testing.T) {
	q := recentExceptionGroupCountsSQL("WHERE x")
	for _, want := range []string{
		"count() AS open_groups",
		"countIf(first_seen >= fromUnixTimestamp64Nano(?)) AS new_groups",
		"FROM exception_groups FINAL WHERE x",
		"LIMIT 1",
		"max_execution_time = 10",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("%q yok:\n%s", want, q)
		}
	}
	// İlk yer tutucu SELECT'teki first_seen eşiği (RecentExceptionGroupCounts
	// args'ı from.UnixNano() ile başlatır).
	if strings.Index(q, "?") > strings.Index(q, "FROM exception_groups") {
		t.Error("first_seen eşiği WHERE'den önce bağlanmalı")
	}
}
