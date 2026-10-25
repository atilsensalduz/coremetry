package chstore

// notify_thread.go — v0.10.1532 (Problem standardı R4: Türkçe şablon + yaşam
// döngüsü, "parmak izi başına tek konu"). Bir imzanın (aynı kural + özne /
// aynı exception grubu) BİLDİRİM KONUSU: hangi başlıkla açıldı, hangi durumda
// (AÇILDI / GÜNCELLEME / YENİDEN BAŞLADI / ÇÖZÜLDÜ), en son hangi öncelik ve
// ciddiyetle gitti. internal/notify/lifecycle.go okur ve yazar.
//
// Neden kalıcı: konu bir e-posta konuşmasıdır (sabit Konu + References
// başlığı). Süreç-içi tutulsa her yeniden başlatma yeni bir konu açar ve
// "tek konu" sözü deploy'da kırılır.
//
// Depolama: ortak durum tablosu saved_views (mimari değişmez 5).
//
//	id            "nt:" + imza
//	owner_id      ""              (sistem satırı)
//	page          "notify-thread" (sistem sayfası — genel /api/views yazamaz)
//	name          durum (open | resolved)
//	query_string  JSON NotifyThread
//	created_at    son güncelleme
//
// Yazım yalnız durum DEĞİŞİNCE (açılış, yeniden açılış, çözülme, öncelik /
// ciddiyet yükselişi): her tikte yeniden çağrılan açık problem satır yazmaz.

import (
	"context"
	"encoding/json"
	"time"
)

// NotifyThreadPage — saved_views page değeri.
const NotifyThreadPage = "notify-thread"

const notifyThreadIDPrefix = "nt:"

// Konu durumları (saved_views name).
const (
	NotifyThreadOpen     = "open"
	NotifyThreadResolved = "resolved"
)

// NotifyThread — bir imzanın bildirim konusu.
type NotifyThread struct {
	Signature  string `json:"signature"`
	Topic      string `json:"topic"`    // açılıştaki standart manşet (Konu satırı sabit kalır)
	OpenedAt   int64  `json:"openedAt"` // unix ns — konu kimliği bundan türer
	ResolvedAt int64  `json:"resolvedAt,omitempty"`
	State      string `json:"state"`
	Priority   string `json:"priority,omitempty"`
	Severity   string `json:"severity,omitempty"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// notifyThreadFromRow — SAF: satır → konu; kimlik/sayfa/sahip tutmazsa ok=false
// (genel uçtan yazılmış sahte satır konu sayılmaz; problem_verdict.go deseni).
func notifyThreadFromRow(v *SavedView) (NotifyThread, bool) {
	if v == nil || v.Page != NotifyThreadPage || v.OwnerID != "" || v.Name == "" {
		return NotifyThread{}, false
	}
	var t NotifyThread
	if err := json.Unmarshal([]byte(v.QueryString), &t); err != nil || t.Signature == "" {
		return NotifyThread{}, false
	}
	if v.ID != notifyThreadIDPrefix+t.Signature {
		return NotifyThread{}, false
	}
	return t, true
}

// GetNotifyThread — imzanın konusu (yoksa nil, nil).
func (s *Store) GetNotifyThread(ctx context.Context, sig string) (*NotifyThread, error) {
	v, err := s.GetSavedView(ctx, notifyThreadIDPrefix+sig)
	if err != nil || v == nil {
		return nil, err
	}
	t, ok := notifyThreadFromRow(v)
	if !ok {
		return nil, nil
	}
	return &t, nil
}

// PutNotifyThread — konuyu yazar (aynı imza → aynı satır).
func (s *Store) PutNotifyThread(ctx context.Context, t NotifyThread) error {
	if t.UpdatedAt == 0 {
		t.UpdatedAt = time.Now().UnixNano()
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return s.UpsertSavedView(ctx, SavedView{ID: notifyThreadIDPrefix + t.Signature, Name: t.State,
		Page: NotifyThreadPage, QueryString: string(raw), CreatedAt: t.UpdatedAt})
}
