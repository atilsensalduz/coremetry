package chstore

// sre_eval.go — v0.10.1301 — AI SRE Faz 5: geçmiş problemlerle doğruluk ölçümü.
//
// Yeni tablo YOK (invariant 5). İki satır sınıfı, ikisi de saved_views sistem
// satırı (owner_id = ''):
//
//   - Koşu satırı: page='sre-eval', id='sre-eval-<runId>', name = koşu durumu
//     (running | done), query_string = koşu JSON'u (≤64 KB: vaka listesi ≤50 +
//     her vakanın ÖZET sonucu — beklenen kategori, ilk 3 hipotez, kapsam).
//     Liste okuması ≤200 satır.
//   - Tekrar (replay) işi: KUYRUK sayfasında (page='sre-investigation'),
//     id='sre-replay-<runId>-<problemId>'. Kuyruğun claim / kira / reclaim /
//     SIGTERM mekanizması aynen işler; canlı kayıt 'sre-<problemId>' ASLA
//     yazılmaz (farklı kimlik). Tam tekrar kaydı orada 30 gün durur (aynı
//     süpürücü); koşu satırı yalnız özeti taşır.
//
// Neden her vaka KENDİ koşu satırı değil: koşu ≤50 vaka, özet ~0,6 KB → tek
// satır 64 KB'a rahat sığar ve liste okuması koşu başına tek satır kalır.
// Eşzamanlı drain yazımları koşu başına Redis kilidiyle sıralanır (sre paketi);
// kaybolan bir birleştirme okuma anında tekrar satırlarından uzlaştırılır.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	// SREEvalPage — değerlendirme koşusu satırlarının saved_views page değeri.
	SREEvalPage = "sre-eval"
	// SREEvalIDPrefix — koşu satırı kimliği öneki ("sre-eval-" + runId).
	SREEvalIDPrefix = "sre-eval-"
	// SREEvalMaxBytes — koşu gövdesi tavanı.
	SREEvalMaxBytes = 64 << 10
	// SREEvalListMax — koşu listesi okuma tavanı.
	SREEvalListMax = 200
	// SREEvalRetention — koşu satırları bundan eski olunca süpürülür (ölçüm
	// geçmişi araştırma kayıtlarından uzun yaşar; satırlar küçük).
	SREEvalRetention = 180 * 24 * time.Hour
	// SREReplayKeyPrefix — kuyruk anahtarı öneki: tekrar işinin satırı
	// SREInvestigationID("replay-<runId>-<problemId>") = "sre-replay-…".
	SREReplayKeyPrefix = "replay-"
	// SREReplayRowsMax — bir koşunun tekrar satırları okuma tavanı.
	SREReplayRowsMax = 60
)

// SREEvalID — runId → koşu satırı kimliği.
func SREEvalID(runID string) string { return SREEvalIDPrefix + runID }

// SREReplayKey — SAF: tekrar işinin kuyruk anahtarı (canlı kayıt anahtarı
// problemId'dir; bu ondan ayrıdır).
func SREReplayKey(runID, problemID string) string {
	return SREReplayKeyPrefix + runID + "-" + problemID
}

// listSREEvalRunsSQL — koşu listesi: en yeni önce, ≤200 (testte pinli).
const listSREEvalRunsSQL = `
		SELECT id, owner_id, name, page, query_string, pinned, created_at
		FROM saved_views FINAL
		WHERE page = ? AND owner_id = '' AND startsWith(id, '` + SREEvalIDPrefix + `')
		ORDER BY created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`

// listSREReplayRowsSQL — bir koşunun tekrar işleri (kuyruk sayfası, önek =
// "sre-replay-<runId>-"; runId '-' taşımaz → önek tek koşuya aittir).
const listSREReplayRowsSQL = `
		SELECT id, owner_id, name, page, query_string, pinned, created_at
		FROM saved_views FINAL
		WHERE page = ? AND owner_id = '' AND startsWith(id, ?)
		ORDER BY created_at ASC
		LIMIT ?
		SETTINGS max_execution_time = 5`

// listRecentSREInvestigationsSQL — CANLI araştırmalar (tekrar satırları
// hariç), en yeni önce, ≤200: ilk hipoteze varış süresinin örneklemi.
const listRecentSREInvestigationsSQL = `
		SELECT id, owner_id, name, page, query_string, pinned, created_at
		FROM saved_views FINAL
		WHERE page = ? AND owner_id = '' AND startsWith(id, '` + SREInvestigationIDPrefix + `')
		  AND NOT startsWith(id, '` + SREInvestigationIDPrefix + SREReplayKeyPrefix + `') AND name IN ?
		ORDER BY created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`

// clampSREEvalLimit — SAF: 1..SREEvalListMax (0/negatif → tavan).
func clampSREEvalLimit(limit int) int {
	if limit <= 0 || limit > SREEvalListMax {
		return SREEvalListMax
	}
	return limit
}

func (s *Store) querySavedViews(ctx context.Context, q string, args ...any) ([]SavedView, error) {
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedView{}
	for rows.Next() {
		var v SavedView
		var pinned uint8
		var createdAt time.Time
		if err := rows.Scan(&v.ID, &v.OwnerID, &v.Name, &v.Page, &v.QueryString, &pinned, &createdAt); err != nil {
			return nil, err
		}
		v.Pinned = pinned == 1
		v.CreatedAt = createdAt.UnixNano()
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListSREEvalRuns — değerlendirme koşuları, en yeni önce (≤200).
func (s *Store) ListSREEvalRuns(ctx context.Context, limit int) ([]SavedView, error) {
	return s.querySavedViews(ctx, listSREEvalRunsSQL, SREEvalPage, clampSREEvalLimit(limit))
}

// ListSREReplayRows — bir koşunun kuyruktaki tekrar satırları (≤60).
func (s *Store) ListSREReplayRows(ctx context.Context, runID string, limit int) ([]SavedView, error) {
	if runID == "" || strings.Contains(runID, "-") {
		return []SavedView{}, nil
	}
	if limit <= 0 || limit > SREReplayRowsMax {
		limit = SREReplayRowsMax
	}
	prefix := SREInvestigationID(SREReplayKeyPrefix + runID + "-")
	return s.querySavedViews(ctx, listSREReplayRowsSQL, SREInvestigationPage, prefix, limit)
}

// ListRecentSREInvestigations — verilen durumlardaki CANLI araştırmalar, en
// yeni önce (≤200). Boş durum listesi = okuma yok.
func (s *Store) ListRecentSREInvestigations(ctx context.Context, states []string, limit int) ([]SavedView, error) {
	if len(states) == 0 {
		return []SavedView{}, nil
	}
	return s.querySavedViews(ctx, listRecentSREInvestigationsSQL, SREInvestigationPage, states, clampSREListLimit(limit))
}

// GetSREEvalRun — koşu satırı; yoksa ya da sistem satırı değilse (nil, nil).
func (s *Store) GetSREEvalRun(ctx context.Context, runID string) (*SavedView, error) {
	if runID == "" {
		return nil, nil
	}
	v, err := s.GetSavedView(ctx, SREEvalID(runID))
	if err != nil || v == nil {
		return nil, err
	}
	if v.Page != SREEvalPage || v.OwnerID != "" || v.Name == "" {
		return nil, nil
	}
	return v, nil
}

// PutSREEvalRun — koşu satırını yazar (tam satır; createdAt her yazımda
// TAŞINIR — liste sırası). Gövde tavanı aşarsa yazım reddedilir.
func (s *Store) PutSREEvalRun(ctx context.Context, runID, state, body string, createdAt int64) error {
	if runID == "" || strings.TrimSpace(state) == "" {
		return fmt.Errorf("sre eval: runId ve state zorunlu")
	}
	if len(body) > SREEvalMaxBytes {
		return fmt.Errorf("sre eval: gövde %d B > tavan %d B", len(body), SREEvalMaxBytes)
	}
	return s.UpsertSavedView(ctx, SavedView{ID: SREEvalID(runID), Name: state,
		Page: SREEvalPage, QueryString: body, CreatedAt: createdAt})
}

// SweepSREEvalRuns — son yazımı cutoff'tan eski koşu satırlarını siler
// (StartAIChatSweeper'ın üçüncü kolu; sayım + silme SQL'i araştırma
// süpürmesiyle aynı, sayfa parametreli).
func (s *Store) SweepSREEvalRuns(ctx context.Context, cutoff time.Time) (uint64, error) {
	cutNs := uint64(cutoff.UnixNano())
	var n uint64
	if err := s.conn.QueryRow(ctx, sreInvestigationSweepCountSQL, SREEvalPage, cutNs).Scan(&n); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	if err := s.conn.Exec(ctx, sreInvestigationSweepDeleteSQL, SREEvalPage, cutNs); err != nil {
		return 0, err
	}
	return n, nil
}
