package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1495 regresyon testi — operatör (prod): `runtime:jvm-gc:<svc>:<pod>`
// problemi "Where errors concentrate — top attribute over the error spans"
// gösterdi; problem hatayla ilgili değildi. Kıyas bölümü problem TÜRÜNDEN
// seçilir: errors (hata) / window (gecikme, trafik…) / none (runtime, db,
// monitor, self, servis olmayan özne — tarama yok). Servis adları sentetik.

func TestRootCauseBubbleModeByKind(t *testing.T) {
	cases := []struct {
		name string
		p    *chstore.Problem
		want string
	}{
		{"hata oranı kuralı", &chstore.Problem{ID: "r1", RuleID: "r1", Service: "orders-api", Metric: "error_rate"}, bubbleModeErrors},
		{"http hata oranı", &chstore.Problem{ID: "r2", Service: "orders-api", Metric: "http_error_rate"}, bubbleModeErrors},
		{"p99 gecikme — hata dili YOK", &chstore.Problem{ID: "r3", Service: "orders-api", Metric: "p99_ms"}, bubbleModeWindow},
		{"trafik", &chstore.Problem{ID: "r4", Service: "orders-api", Metric: "request_rate"}, bubbleModeWindow},
		{"yaygın yavaşlama", &chstore.Problem{ID: "svc-slowdown:orders-api", Service: "orders-api", Metric: "p99_ms"}, bubbleModeWindow},
		{"prod: JVM GC tek pod", &chstore.Problem{ID: "runtime:jvm-gc:core-api:core-api-7d9f-abcde", RuleID: "runtime:jvm-gc:core-api:core-api-7d9f-abcde",
			Service: "core-api", Pod: "core-api-7d9f-abcde", Metric: "jvm_gc_pause_ratio"}, bubbleModeNone},
		{"runtime ailesi rule id ile", &chstore.Problem{ID: "x", RuleID: "runtime:heap:core-api", Service: "core-api", Metric: "heap_used_pct"}, bubbleModeNone},
		{"runtime ama metrik adı error içerse de none", &chstore.Problem{ID: "runtime:thread-errors:core-api", Service: "core-api", Metric: "thread_error_count"}, bubbleModeNone},
		{"db kapasite", &chstore.Problem{ID: "db-capacity:db1", Service: "db.example.test", Kind: "database", Metric: "conn_pct"}, bubbleModeNone},
		{"db sağlık", &chstore.Problem{ID: "db-health:x", Service: "orders-api", Metric: "error_count"}, bubbleModeNone},
		{"monitor", &chstore.Problem{ID: "monitor:abc", Service: "orders-api", Metric: "up"}, bubbleModeNone},
		{"self-health", &chstore.Problem{ID: "self-ingest", Service: "coremetry", Metric: "lag"}, bubbleModeNone},
		{"servissiz özne", &chstore.Problem{ID: "exception-storm", Metric: "error_count"}, bubbleModeNone},
		{"nil", nil, bubbleModeNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rootCauseBubbleMode(c.p); got != c.want {
				t.Errorf("rootCauseBubbleMode = %q, want %q", got, c.want)
			}
		})
	}
	if anomalyBubbleMode("trace_op") != bubbleModeErrors || anomalyBubbleMode("log_pattern") != bubbleModeWindow ||
		anomalyBubbleMode("trace_op_latency") != bubbleModeWindow {
		t.Error("anomali çıpası: trace_op → errors, diğerleri → window")
	}
}

// TestRootCauseBubbleNoneSkipsScan — runtime problemi: /rootcause/bubbleup ve
// tam /rootcause ham spans kıyasını HİÇ koşmaz, cevap mode=none taşır;
// çekirdek bubbleMode=none. Hata problemi errors alt kümesiyle koşar.
func TestRootCauseBubbleNoneSkipsScan(t *testing.T) {
	start := time.Now().Add(-90 * time.Minute).UnixNano()
	st := &fakeRootCauseStore{
		problem: &chstore.Problem{ID: "runtime:jvm-gc:core-api:core-api-0", Service: "core-api", Pod: "core-api-0", Metric: "jvm_gc_pause_ratio", StartedAt: start},
		bubble:  sampleBubble(),
	}
	s := newRootCauseTestServer(t, st)
	w := callRootCause(s, (*Server).getProblemRootCauseBubbleUp, "/x", st.problem.ID, context.Background())
	if w.Code != 200 {
		t.Fatalf("code %d %s", w.Code, w.Body.String())
	}
	m := decodeMap(t, w.Body.Bytes())
	if m["mode"] != bubbleModeNone || m["bubbleUp"] != nil {
		t.Fatalf("bubbleup cevabı mode=none ve bubbleUp'sız olmalı: %v", m)
	}
	core := decodeMap(t, callRootCause(s, (*Server).getProblemRootCauseCore, "/x", st.problem.ID, context.Background()).Body.Bytes())
	if core["bubbleMode"] != bubbleModeNone {
		t.Fatalf("çekirdek bubbleMode = %v", core["bubbleMode"])
	}
	full := decodeMap(t, callRootCause(s, (*Server).getProblemRootCause, "/x", st.problem.ID, context.Background()).Body.Bytes())
	if full["bubbleUp"] != nil || full["bubbleMode"] != bubbleModeNone {
		t.Fatalf("tam demet runtime'da kıyas taşımamalı: %v", full["bubbleUp"])
	}
	if n := atomic.LoadInt32(&st.bubbleCalls); n != 0 {
		t.Fatalf("runtime problemi ham spans kıyası koştu: %d çağrı", n)
	}

	st2 := &fakeRootCauseStore{
		problem: &chstore.Problem{ID: "p-err", Service: "orders-api", Metric: "error_rate", StartedAt: start},
		bubble:  sampleBubble(),
	}
	s2 := newRootCauseTestServer(t, st2)
	m2 := decodeMap(t, callRootCause(s2, (*Server).getProblemRootCauseBubbleUp, "/x", "p-err", context.Background()).Body.Bytes())
	if m2["mode"] != bubbleModeErrors || m2["bubbleUp"] == nil {
		t.Fatalf("hata problemi: mode=errors + bubbleUp beklenir: %v", m2)
	}
	if st2.bubbleArgs[1] != true {
		t.Fatalf("hata problemi hatalı alt kümeyle koşmalı: %v", st2.bubbleArgs)
	}
}
