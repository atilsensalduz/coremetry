package api

// v0.10.1490 — takvim dilimi PUT kapısı: bilinmeyen IANA adı 400 (kelepçe
// değil), mağazaya dokunulmadan.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
)

func TestPutAnomalySensitivityRejectsUnknownCalendarTimezone(t *testing.T) {
	s := &Server{} // mağaza yok: 400 mağazadan ÖNCE dönmeli
	for _, tz := range []string{"Mars/Olympus", "Local", "Europe/Istanbul/x"} {
		body := `{"dwellBuckets":3,"criticalZ":6,"calendar":{"days":[],"timezone":"` + tz + `"}}`
		req := httptest.NewRequest("PUT", "/api/settings/anomaly-sensitivity", strings.NewReader(body))
		req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u1", Role: auth.RoleAdmin}))
		rec := httptest.NewRecorder()
		s.putAnomalySensitivity(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "calendar.timezone") {
			t.Errorf("%s: %d %q", tz, rec.Code, rec.Body.String())
		}
	}
}
