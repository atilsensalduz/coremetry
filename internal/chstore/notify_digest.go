package chstore

// notify_digest.go — v0.10.1533 (Problem standardı R5; D7 "P3 özeti 09:00 ve
// 16:00 yerel"). enforce kipinde P3 bildirimleri anında gitmez; özet kuyruğuna
// düşer ve günün özet saatlerinde TEK mesajda gider (internal/notify/digest.go).
//
// Kuyruk: ortak durum tablosu saved_views (mimari değişmez 5).
//
//	id            "nd:" + imza     (aynı imza bir özette bir kez — en son hâli)
//	owner_id      ""               (sistem satırı)
//	page          "notify-digest"  (sistem sayfası)
//	name          "pending"        ("" = gönderildi/silindi, mezar taşı)
//	query_string  JSON DigestItem
//	created_at    kuyruğa giriş
//
// Son özet anı system_settings "notify_digest_last" ("YYYY-MM-DD HH:MM" yerel):
// çok-pod / yeniden başlatmada aynı slot iki kez gitmez.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// NotifyDigestPage — saved_views page değeri.
const NotifyDigestPage = "notify-digest"

const (
	notifyDigestIDPrefix = "nd:"
	notifyDigestPending  = "pending"
	// NotifyDigestMax — bir özetin en çok kalemi (okuma LIMIT'i).
	NotifyDigestMax = 500
	// NotifyDigestLastKey — system_settings: son gönderilen slot.
	NotifyDigestLastKey = "notify_digest_last"
	// notifyDigestCatchUp — kaçırılmış slotun en geç bu kadar sonra
	// gönderilmesi (yeniden başlatma örtüşmesi); daha eskisi atlanır, kalemler
	// bir sonraki slota kalır.
	notifyDigestCatchUp = 2 * time.Hour
)

// DigestItem — özetin bir satırı.
type DigestItem struct {
	Signature  string `json:"signature"`
	ProblemID  string `json:"problemId"`
	Service    string `json:"service"`
	Severity   string `json:"severity"`
	Priority   string `json:"priority"`
	Kind       string `json:"kind"` // ProblemNotifyKind (kanal tür süzgeci)
	Headline   string `json:"headline"`
	Impact     string `json:"impact"`
	Confidence string `json:"confidence"`
	StartedAt  int64  `json:"startedAt"`
	QueuedAt   int64  `json:"queuedAt"`
	Count      int    `json:"count"` // aynı imzanın bu özet penceresindeki tekrar sayısı
}

func notifyDigestID(sig string) string { return notifyDigestIDPrefix + sig }

// digestItemFromRow — SAF: satır → kalem (sistem satırı değilse ok=false).
func digestItemFromRow(id, owner, name, page, query string) (DigestItem, bool) {
	if page != NotifyDigestPage || owner != "" || name != notifyDigestPending {
		return DigestItem{}, false
	}
	var it DigestItem
	if err := json.Unmarshal([]byte(query), &it); err != nil || it.Signature == "" || id != notifyDigestID(it.Signature) {
		return DigestItem{}, false
	}
	return it, true
}

// EnqueueDigest — kalemi kuyruğa yazar; aynı imza bekliyorsa sayacı artırıp
// en son hâliyle değiştirir (ilk kuyruğa giriş anı korunur).
func (s *Store) EnqueueDigest(ctx context.Context, it DigestItem) error {
	if it.QueuedAt == 0 {
		it.QueuedAt = time.Now().UnixNano()
	}
	if it.Count == 0 {
		it.Count = 1
	}
	if prev, err := s.GetSavedView(ctx, notifyDigestID(it.Signature)); err == nil && prev != nil {
		if old, ok := digestItemFromRow(prev.ID, prev.OwnerID, prev.Name, prev.Page, prev.QueryString); ok {
			it.Count = old.Count + 1
			it.QueuedAt = old.QueuedAt
		}
	}
	raw, err := json.Marshal(it)
	if err != nil {
		return err
	}
	return s.UpsertSavedView(ctx, SavedView{ID: notifyDigestID(it.Signature), Name: notifyDigestPending,
		Page: NotifyDigestPage, QueryString: string(raw), CreatedAt: it.QueuedAt})
}

const notifyDigestListSQL = `
		SELECT id, owner_id, name, page, query_string
		FROM saved_views FINAL
		WHERE page = ? AND owner_id = '' AND startsWith(id, '` + notifyDigestIDPrefix + `') AND name = '` + notifyDigestPending + `'
		ORDER BY created_at
		LIMIT ?
		SETTINGS max_execution_time = 5`

// ListDigestPending — bekleyen kalemler (en eski önce, ≤ NotifyDigestMax).
func (s *Store) ListDigestPending(ctx context.Context) ([]DigestItem, error) {
	rows, err := s.conn.Query(ctx, notifyDigestListSQL, NotifyDigestPage, NotifyDigestMax)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DigestItem{}
	for rows.Next() {
		var id, owner, name, page, query string
		if err := rows.Scan(&id, &owner, &name, &page, &query); err != nil {
			return nil, err
		}
		if it, ok := digestItemFromRow(id, owner, name, page, query); ok {
			out = append(out, it)
		}
	}
	return out, rows.Err()
}

// ClearDigest — gönderilen kalemleri kaldırır (mezar taşı).
func (s *Store) ClearDigest(ctx context.Context, sigs []string) error {
	for _, sig := range sigs {
		if err := s.DeleteSavedView(ctx, notifyDigestID(sig)); err != nil {
			return err
		}
	}
	return nil
}

// ReadDigestLast / WriteDigestLast — son gönderilen slot ("" = hiç).
func (s *Store) ReadDigestLast(ctx context.Context) (string, error) {
	raw, err := s.GetSetting(ctx, NotifyDigestLastKey)
	if err != nil {
		return "", err
	}
	return strings.Trim(strings.TrimSpace(string(raw)), `"`), nil
}

func (s *Store) WriteDigestLast(ctx context.Context, slot string) error {
	raw, _ := json.Marshal(slot)
	return s.PutSetting(ctx, NotifyDigestLastKey, raw)
}

// DigestSlotDue — SAF: now (yerel) için gönderilmesi gereken slot. Bugünün
// geçmiş slotlarından en yenisi; son gönderilen değilse ve en çok
// notifyDigestCatchUp kadar geride kaldıysa due=true. Slot dizgisi
// "YYYY-MM-DD HH:MM" (yerel) — kalıcı işaretle kıyaslanır.
func DigestSlotDue(times []string, last string, now time.Time) (string, bool) {
	mins := make([]int, 0, len(times))
	for _, t := range times {
		if m, ok := hhmmMinutes(t); ok {
			mins = append(mins, m)
		}
	}
	if len(mins) == 0 {
		return "", false
	}
	sort.Ints(mins)
	cur := now.Hour()*60 + now.Minute()
	best := -1
	for _, m := range mins {
		if m <= cur {
			best = m
		}
	}
	if best < 0 {
		return "", false // bugünün ilk slotu henüz gelmedi (dünün kaçırılanı kovalanmaz)
	}
	slotAt := time.Date(now.Year(), now.Month(), now.Day(), best/60, best%60, 0, 0, now.Location())
	slot := slotAt.Format("2006-01-02 15:04")
	if slot == last || now.Sub(slotAt) > notifyDigestCatchUp {
		return slot, false
	}
	return slot, true
}
