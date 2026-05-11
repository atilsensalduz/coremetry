package notify

import (
	"testing"
	"time"
)

// TestZoomHTTPClientHasTimeout locks in the per-request deadline
// guard on the package-level Zoom client. Pre-v0.4.79 the Zoom
// integration used http.DefaultClient (no timeout) — a regional
// outage at Zoom would hang every alert-sender goroutine and
// stack the evaluator's queue.
//
// The unit test stays cheap (no network) so it runs in every
// `go test ./...` cycle and catches anyone who reverts to the
// default client by accident.
func TestZoomHTTPClientHasTimeout(t *testing.T) {
	if zoomHTTPClient == nil {
		t.Fatal("zoomHTTPClient must be non-nil")
	}
	if zoomHTTPClient.Timeout <= 0 {
		t.Fatal("zoomHTTPClient must have a positive Timeout — a regional Zoom outage would otherwise stall every alert send")
	}
	if zoomHTTPClient.Timeout > 60*time.Second {
		t.Fatalf("zoomHTTPClient.Timeout=%s is too long; evaluator runs every minute, send batches must clear faster",
			zoomHTTPClient.Timeout)
	}
}
