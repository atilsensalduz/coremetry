package chstore

import (
	"regexp"
	"strings"
	"testing"
)

// v0.10.1226 — biçim ölçümü SQL'i sınırlı ve desen Go'da da geçerli.
func TestFunctionCodeShapeSQL(t *testing.T) {
	q := functionCodeShapeSQL("rollup_spans_wide_1m", "ts", "function_code", "sum(span_count)")
	for _, want := range []string{"ts >= ? AND ts < ?", "LIMIT 60", "max_execution_time = 20", FunctionCodeShape} {
		if !strings.Contains(q, want) {
			t.Errorf("sorgu %q içermiyor", want)
		}
	}
	re := regexp.MustCompile(FunctionCodeShape)
	for code, ok := range map[string]bool{"BKRB001": true, "bkrb001": false, "123ABC": false, "A1B2C3D4E5F6G": false, "ABC": false} {
		if re.MatchString(code) != ok {
			t.Errorf("%s: %v bekleniyordu", code, ok)
		}
	}
}
