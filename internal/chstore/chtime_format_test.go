// v0.10.1412 — "CH DateTime parse error in the demo stack": table-driven pin
// for the two CH time-bind formatters and for the operation-catalog bounds
// that mixed them up (span/endpoint time_bucket is plain DateTime and got a
// nanosecond DateTime64 string → code 53 on every catalog tick).
package chstore

import (
	"regexp"
	"testing"
	"time"
)

var (
	chDateTimeShape   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)
	chDateTime64Shape = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d{1,9})?$`)
)

func TestCHTimeArgFormatters(t *testing.T) {
	plus3 := time.FixedZone("UTC+3", 3*3600)
	cases := []struct {
		name   string
		in     time.Time
		want   string // chDateTimeArg
		want64 string // chDateTime64Arg
	}{
		{"nanoseconds", time.Date(2026, 10, 19, 8, 26, 15, 176482289, time.UTC),
			"2026-10-19 08:26:15", "2026-10-19 08:26:15.176482289"},
		{"microsecond wall clock (darwin)", time.Date(2026, 10, 19, 8, 26, 15, 176482000, time.UTC),
			"2026-10-19 08:26:15", "2026-10-19 08:26:15.176482"},
		{"milliseconds", time.Date(2026, 10, 19, 8, 26, 15, 714000000, time.UTC),
			"2026-10-19 08:26:15", "2026-10-19 08:26:15.714"},
		{"whole second", time.Date(2026, 10, 19, 8, 26, 15, 0, time.UTC),
			"2026-10-19 08:26:15", "2026-10-19 08:26:15"},
		{"just under next second is truncated, not rounded", time.Date(2026, 10, 19, 23, 59, 59, 999999999, time.UTC),
			"2026-10-19 23:59:59", "2026-10-19 23:59:59.999999999"},
		{"non-UTC normalised (day rollback)", time.Date(2026, 10, 20, 1, 30, 0, 500000000, plus3),
			"2026-10-19 22:30:00", "2026-10-19 22:30:00.5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, got64 := chDateTimeArg(c.in), chDateTime64Arg(c.in)
			if got != c.want {
				t.Errorf("chDateTimeArg = %q, want %q", got, c.want)
			}
			if got64 != c.want64 {
				t.Errorf("chDateTime64Arg = %q, want %q", got64, c.want64)
			}
			if !chDateTimeShape.MatchString(got) {
				t.Errorf("chDateTimeArg %q is not a CH DateTime literal (no 'T', no 'Z', no fraction)", got)
			}
			if !chDateTime64Shape.MatchString(got64) {
				t.Errorf("chDateTime64Arg %q is not a CH DateTime64 literal (no 'T', no 'Z')", got64)
			}
		})
	}
}

func TestOperationCatalogBounds(t *testing.T) {
	cases := []struct {
		name               string
		now                time.Time
		wantBucket, wantTS string
	}{
		{"fractional now", time.Date(2026, 10, 20, 8, 26, 15, 176482289, time.UTC),
			"2026-10-19 08:26:15", "2026-10-19 08:26:15.176482289"},
		{"whole-second now", time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC),
			"2026-10-19 00:00:00", "2026-10-19 00:00:00"},
		{"non-UTC now", time.Date(2026, 10, 20, 3, 0, 0, 250000000, time.FixedZone("UTC+3", 3*3600)),
			"2026-10-19 00:00:00", "2026-10-19 00:00:00.25"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bucket, ts := operationCatalogBounds(c.now)
			if bucket != c.wantBucket {
				t.Errorf("bucketFrom = %q, want %q", bucket, c.wantBucket)
			}
			if ts != c.wantTS {
				t.Errorf("timeFrom = %q, want %q", ts, c.wantTS)
			}
			// The time_bucket bind must never carry a fraction (code 53).
			if !chDateTimeShape.MatchString(bucket) {
				t.Errorf("bucketFrom %q would be rejected by a DateTime column", bucket)
			}
		})
	}
}
