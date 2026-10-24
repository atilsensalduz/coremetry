package api

// v0.10.1511 — trace list column filters: cache key hashes every input
// (v0.5.187 class), is order-independent, and clamps limit before keying.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTraceColValuesKey(t *testing.T) {
	base := "col=attr&key=FUNCTION_CODE_X&from=1761300000000000000&to=1761303600000000000&limit=20"
	k := traceColValuesKey(base)
	// every scope input must change the key
	for _, extra := range []string{
		"&q=AB", "&service=orders-api", "&search=pay", "&hasError=true", "&minMs=5", "&maxMs=9",
		"&env=prod", "&cluster=kxzone", `&filters=[{"k":"a","op":"=","v":["b"]}]`,
		`&filterGroup={"join":"OR","filters":[]}`, "&dsl=a%3Db",
	} {
		if traceColValuesKey(base+extra) == k {
			t.Errorf("key ignores %s", extra)
		}
	}
	if traceColValuesKey(strings.Replace(base, "FUNCTION_CODE_X", "CHANNEL_CODE_X", 1)) == k {
		t.Error("key ignores the column key")
	}
	// parameter order does not matter
	if traceColValuesKey("limit=20&to=1761303600000000000&from=1761300000000000000&key=FUNCTION_CODE_X&col=attr") != k {
		t.Error("key depends on parameter order")
	}
	// crafted limits collapse onto the clamp
	if traceColValuesKey(strings.Replace(base, "limit=20", "limit=99999", 1)) != traceColValuesKey(strings.Replace(base, "limit=20", "limit=100", 1)) {
		t.Error("limit not clamped before keying")
	}
	if !strings.HasPrefix(k, "traces-colvals:") {
		t.Errorf("unexpected key prefix %q", k)
	}
}

// Validation runs before any store access: a nil store must never be reached
// on a bad request (unbounded window / unknown column / unsafe key).
func TestTraceColumnValuesRejectsBadInput(t *testing.T) {
	s := &Server{}
	for _, qs := range []string{
		"col=bogus&from=1&to=2",
		"col=attr&from=1&to=2",
		"col=attr&key=a;drop&from=1&to=2",
		"col=service",
		"col=service&from=5&to=5",
	} {
		rr := httptest.NewRecorder()
		s.getTraceColumnValues(rr, httptest.NewRequest(http.MethodGet, "/api/traces/column-values?"+qs, nil))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s → %d, want 400", qs, rr.Code)
		}
	}
}
