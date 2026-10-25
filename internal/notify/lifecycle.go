package notify

// lifecycle.go — v0.10.1532 (Problem standardı R4; operatör kararları D9
// "bildirimler tamamen Türkçe" + "AÇILDI / GÜNCELLEME / YENİDEN BAŞLADI /
// ÇÖZÜLDÜ, parmak izi başına tek konu").
//
// Bir bildirim artık bir KONUNUN parçası: aynı imzanın (aynı kural + özne /
// aynı exception grubu — verdictSignature) açılışı, güncellemeleri, yeniden
// başlaması ve çözülmesi tek e-posta konuşmasında (sabit Konu + References)
// toplanır. Durum etiketi mesajın başında, konu satırı DEĞİŞMEZ — Outlook
// konuşmayı normalize edilmiş konuya göre gruplar; durumu konuya koymak her
// geçişte yeni konuşma açardı.
//
// Karar SAF (nextLifecycle, tablo testli); konu kaydı chstore.NotifyThread
// (saved_views). Kayıt yalnız durum değişince yazılır.
//
// Yeniden başlama penceresi 24 sa: çözüldükten sonra 24 sa içinde aynı imza
// yeniden açılırsa AYNI konuya "YENİDEN BAŞLADI" düşer (flap tek konuşmada
// görünür); daha geç gelirse yeni bir konu açılır (eski olayın konuşmasına
// haftalar sonra eklenmek yanıltır).

import (
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Yaşam döngüsü etiketleri (mesajın başındaki durum).
const (
	LifeOpened   = "AÇILDI"
	LifeUpdate   = "GÜNCELLEME"
	LifeReopened = "YENİDEN BAŞLADI"
	LifeResolved = "ÇÖZÜLDÜ"
)

// threadReopenWindow — çözülmüş konuya "YENİDEN BAŞLADI" ile dönme penceresi.
const threadReopenWindow = 24 * time.Hour

// threadKey — SAF: konu anahtarı. Öğretme imzası varsa o (kural + özne /
// exception grubu); yoksa (olay) problem kimliği.
func threadKey(p chstore.Problem) string {
	if sig := verdictSignature(p); sig != "" {
		return sig
	}
	return "id:" + p.ID
}

// prioRankTR — SAF: P1 > P2 > P3 > "".
func prioRankTR(p string) int {
	switch p {
	case "P1":
		return 3
	case "P2":
		return 2
	case "P3":
		return 1
	}
	return 0
}

// sevRankTR — SAF: critical > warning > info > "".
func sevRankTR(s string) int {
	switch s {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	}
	return 0
}

// nextLifecycle — SAF: önceki konu + bu bildirim → durum etiketi, konunun yeni
// hâli ve yazılması gerekip gerekmediği.
//
//	çözülme                              → ÇÖZÜLDÜ  (konu yoksa da; ilk kez yazılır)
//	açık, konu yok                       → AÇILDI   (yeni konu, topic = manşet)
//	açık, konu çözülmüş ≤ 24 sa önce     → YENİDEN BAŞLADI (aynı konu)
//	açık, konu çözülmüş > 24 sa önce     → AÇILDI   (yeni konu)
//	açık, konu açık                      → GÜNCELLEME (öncelik / ciddiyet
//	                                       yükseldiyse yazılır, değilse yazım yok)
func nextLifecycle(prev *chstore.NotifyThread, key, topic string, p chstore.Problem, now time.Time) (string, chstore.NotifyThread, bool) {
	nowNs := now.UnixNano()
	fresh := chstore.NotifyThread{Signature: key, Topic: topic, OpenedAt: nowNs, State: chstore.NotifyThreadOpen,
		Priority: p.Priority, Severity: p.Severity, UpdatedAt: nowNs}
	if p.Status == "resolved" {
		if prev == nil {
			fresh.State, fresh.ResolvedAt = chstore.NotifyThreadResolved, nowNs
			return LifeResolved, fresh, true
		}
		next := *prev
		if prev.State == chstore.NotifyThreadResolved {
			return LifeResolved, next, false
		}
		next.State, next.ResolvedAt, next.UpdatedAt = chstore.NotifyThreadResolved, nowNs, nowNs
		return LifeResolved, next, true
	}
	if prev == nil {
		return LifeOpened, fresh, true
	}
	if prev.State == chstore.NotifyThreadResolved {
		if prev.ResolvedAt > 0 && nowNs-prev.ResolvedAt <= int64(threadReopenWindow) {
			next := *prev
			next.State, next.ResolvedAt, next.Priority, next.Severity, next.UpdatedAt =
				chstore.NotifyThreadOpen, 0, p.Priority, p.Severity, nowNs
			return LifeReopened, next, true
		}
		return LifeOpened, fresh, true
	}
	next := *prev
	changed := false
	if prioRankTR(p.Priority) > prioRankTR(prev.Priority) {
		next.Priority, changed = p.Priority, true
	}
	if sevRankTR(p.Severity) > sevRankTR(prev.Severity) {
		next.Severity, changed = p.Severity, true
	}
	if changed {
		next.UpdatedAt = nowNs
	}
	return LifeUpdate, next, changed
}

// threadMessageRoot — SAF: konunun kök Message-ID'si (RFC 5322 msg-id). İmza
// serbest metin taşır (servis adı) — başlığa ham girmez, FNV özetine iner;
// açılış anı aynı imzanın ardışık konularını ayırır.
func threadMessageRoot(t chstore.NotifyThread) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(t.Signature))
	return fmt.Sprintf("<cm-thread-%016x-%d@coremetry>", h.Sum64(), t.OpenedAt/int64(time.Second))
}

// threadMessageID — SAF: konudaki tek bir mesajın kimliği. AÇILDI mesajı
// kökün kendisidir (References zinciri onu bulur); diğerleri kök + an.
func threadMessageID(t chstore.NotifyThread, life string, now time.Time) string {
	root := threadMessageRoot(t)
	if life == LifeOpened {
		return root
	}
	return fmt.Sprintf("<cm-%d-%s", now.UnixNano(), root[1:])
}

// threadBook — konu kayıtlarının süreç-içi önbelleği + yazım sırası.
type threadBook struct {
	mu sync.Mutex
	m  map[string]*chstore.NotifyThread
}

// advanceThread — konuyu okur (önbellek → kalıcı kayıt), kararı verir,
// gerekiyorsa yazar. Okuma/yazma hatası bildirimi DURDURMAZ: konu "yok"
// sayılır (en kötü ihtimal yeni bir e-posta konuşması — bildirim kaybı değil).
func (n *Notifier) advanceThread(ctx context.Context, p chstore.Problem, topic string, now time.Time) (string, chstore.NotifyThread) {
	key := threadKey(p)
	n.threads.mu.Lock()
	defer n.threads.mu.Unlock()
	if n.threads.m == nil {
		n.threads.m = map[string]*chstore.NotifyThread{}
	}
	prev, ok := n.threads.m[key]
	if !ok && n.store != nil {
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second) // kilit altında: yavaş CH tüm bildirimleri bekletmesin
		t, err := n.store.GetNotifyThread(rctx, key)
		cancel()
		if err != nil {
			log.Printf("[notify] konu okunamadı %s: %v — yeni konu sayılır", key, err)
		} else {
			prev = t
		}
	}
	life, next, persist := nextLifecycle(prev, key, topic, p, now)
	cp := next
	n.threads.m[key] = &cp
	if persist && n.store != nil {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if err := n.store.PutNotifyThread(wctx, next); err != nil {
			log.Printf("[notify] konu yazılamadı %s: %v", key, err)
		}
		cancel()
	}
	return life, next
}
