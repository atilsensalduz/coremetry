package api

// tour_routes.go — v0.10.1334 (uygulama içi rehberli tur, R1; operatör
// onaylı spec, varsayılanlar seçili). Kullanıcı başına tek bayrak: tur
// bitirildi mi / atlandı mı — oto-başlatma kararının sunucu yarısı.
//
//   GET /api/tour/state → {status:'none'|'completed'|'skipped', version, at, newUser, enabled}
//   PUT /api/tour/state ← {status:'completed'|'skipped', version} (≤1 KiB)
//
// Kayıt api.go'ya DEĞİL, route_registry.go defterine (init) — api.go
// büyümez (TestApiGoDoesNotGrow). Depolama invariant #5: yeni tablo YOK —
// preferences_routes.go'nun birebir ikizi: saved_views satırı page='tour',
// id deterministik 'tour:<uid>', owner_id=claims.UserID, name='tour',
// query_string=kanonik JSON {status,version}; UpsertSavedView doğal upsert
// (ReplacingMergeTree, tam-satır replace), GetSavedView FINAL nokta okuma.
// page='tour' SavedViewsBar listesine (sayfa adlarıyla tam eşitlik) asla
// düşmez.
//
// Rol kapısı YOK (her oturum açmış rol, viewer dahil — kişisel durum).
// s.audit YOK (admin yazımı değil; kullanıcı-kapsamlı, idempotent).
// serveCached YOK (kişisel FINAL nokta okuma; anahtara uid girerdi ve
// oturum başına bir kez okunuyor). Claims yoksa 401 — ASLA owner_id=''
// paylaşımlı satıra düşme.
//
// newUser = users.created_at ≥ now−7g (chstore.User.CreatedAt, unix ns).
// Kullanıcı satırı yoksa (ör. env ile tohumlanmış admin) ya da okuma
// düşerse newUser=false — oto-başlatma susar, menüden başlatma çalışır.
// enabled = branding.onboardingTour ≠ "off" (chstore.BrandingSettings);
// branding okuması düşerse varsayılan (açık) — bayrak yalnız oto-başlatmayı
// kısar, newUser zaten ikinci kapı.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("tour", (*Server).registerTourRoutes) }

func (s *Server) registerTourRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tour/state", s.getTourState)
	mux.HandleFunc("PUT /api/tour/state", s.putTourState)
}

const (
	tourBodyMax       = 1 << 10 // 1 KiB
	tourPage          = "tour"
	tourVersionMax    = 1 << 16
	tourNewUserWindow = 7 * 24 * time.Hour
)

// tourRowID — deterministik satır kimliği (SAF).
func tourRowID(uid string) string { return "tour:" + uid }

// validateTourStatus — SAF enum doğrulaması (PUT yalnız iki son durum yazar;
// 'none' bir satırın YOKLUĞUdur, yazılmaz).
func validateTourStatus(status string) error {
	switch status {
	case "completed", "skipped":
		return nil
	}
	return errors.New("status: completed | skipped")
}

// validateTourVersion — SAF: 1..65536 (FE TOUR_VERSION; tur içeriği
// anlamlı biçimde değişince artar).
func validateTourVersion(v int) error {
	if v < 1 || v > tourVersionMax {
		return errors.New("version 1-65536 olmalı")
	}
	return nil
}

// isNewUser — SAF: hesap son 7 günde açıldıysa yeni. createdAt ≤ 0
// (bilinmiyor) yeni SAYILMAZ — eski bir kurulumda sıfır damgalı satır
// herkese tur açtırmasın.
func isNewUser(createdAtNs int64, now time.Time) bool {
	return createdAtNs > 0 && createdAtNs >= now.Add(-tourNewUserWindow).UnixNano()
}

type tourStateBody struct {
	Status  string `json:"status"`
	Version int    `json:"version"`
}

// canonicalTourJSON — saklanan biçim; alan sırası sabit → aynı durum aynı
// bayt (idempotent upsert).
func canonicalTourJSON(b tourStateBody) string {
	raw, _ := json.Marshal(tourStateBody{Status: b.Status, Version: b.Version})
	return string(raw)
}

// parseStoredTour — SAF: saklanan satır → (status, version). Bozuk/boş/
// tanınmayan içerik 'none' (tur yeniden önerilebilir; asla kilitlenmez).
func parseStoredTour(v *chstore.SavedView, uid string) (string, int, int64) {
	if v == nil || v.Name == "" || v.OwnerID != uid {
		return "none", 0, 0
	}
	var b tourStateBody
	if err := json.Unmarshal([]byte(v.QueryString), &b); err != nil || validateTourStatus(b.Status) != nil {
		return "none", 0, 0
	}
	return b.Status, b.Version, v.CreatedAt
}

func tourClaims(w http.ResponseWriter, r *http.Request) (*auth.Claims, bool) {
	claims := auth.FromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		writeJSONError(w, http.StatusUnauthorized, "kimlik gerekli")
		return nil, false
	}
	return claims, true
}

func (s *Server) getTourState(w http.ResponseWriter, r *http.Request) {
	claims, ok := tourClaims(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	v, err := s.store.GetSavedView(ctx, tourRowID(claims.UserID))
	if err != nil {
		writeErr(w, err)
		return
	}
	status, version, at := parseStoredTour(v, claims.UserID)

	newUser := false
	if u, uerr := s.store.GetUserByID(ctx, claims.UserID); uerr == nil && u != nil {
		newUser = isNewUser(u.CreatedAt, time.Now())
	}
	enabled := true
	if b, berr := s.store.GetBranding(ctx); berr == nil {
		enabled = b.TourEnabled()
	}
	writeJSON(w, map[string]any{
		"status": status, "version": version, "at": at,
		"newUser": newUser, "enabled": enabled,
	})
}

func (s *Server) putTourState(w http.ResponseWriter, r *http.Request) {
	claims, ok := tourClaims(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, tourBodyMax))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "gövde 1 KiB'ı aşamaz")
		return
	}
	var b tourStateBody
	if err := json.Unmarshal(raw, &b); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if err := validateTourStatus(b.Status); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateTourVersion(b.Version); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().UnixNano()
	if err := s.store.UpsertSavedView(r.Context(), chstore.SavedView{
		ID: tourRowID(claims.UserID), OwnerID: claims.UserID, Name: tourPage,
		Page: tourPage, QueryString: canonicalTourJSON(b), CreatedAt: now,
	}); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "status": b.Status, "version": b.Version, "at": now})
}
