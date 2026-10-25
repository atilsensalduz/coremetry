package notify

// verdict_silence.go — v0.10.1016 — "Problem değil" imzalarının BİLDİRİMİ.
//
// v0.10.1015 kararları yalnız görünümdü: operatör bir imzayı "problem değil"
// diye işaretlese de mail / kanal bildirimi gelmeye devam ediyordu. Bu dosya
// öğretmeyi bildirime bağlayan kapı — ama YALNIZ yönetici politikayı açtıysa
// (system_settings["problem_verdict_policy"].muteNotifications; varsayılan
// KAPALI). Kapalıyken tek maliyet 30 sn'de bir küçük ayar okuması; hiçbir
// bildirim değişmez.
//
// Açıkken susan: "problem değil" imzalı alarm kuralı problemi (p:<kural>|
// <servis>) ile exception / HTTP hata grubu (e:<parmak izi>) — ekip maili,
// kanallar, çözüm bildirimi ve P1 exception anonsu dahil. Susmayan: canlı
// akış (SSE), Problem kaydının kendisi, olaylar (incident — imzası yok) ve
// anomali OLAYI imzaları (a:…; bildirim hunisine ayrı bir kimlikle girmezler).
//
// İmza sunucuda Problem'den yeniden üretilir ve FE'nin ürettiğiyle
// (frontend/src/lib/problemVerdict.ts inboxSignature) birebir aynı olmalıdır.
//
// Güvenli yön: ayar ya da karar listesi okunamazsa SUSTURMA YOK — bildirim
// kaybetmek fazladan bir mailden kötü (NotificationIgnored kapısıyla aynı).

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// verdictSilenceTTL — politika + karar listesi önbelleği. Bir imzayı "problem
// değil" yapmak (ya da geri almak) en geç bu kadar sonra bildirime yansır.
const verdictSilenceTTL = 30 * time.Second

// verdictSignature — SAF: bildirim hunisindeki Problem → öğretme imzası.
// "" = öğretilemeyen tür (olay, kural kimliği olmayan sentetik kayıt).
func verdictSignature(p chstore.Problem) string {
	if p.Kind == chstore.NotifyKindIncident {
		return ""
	}
	if fp := exceptionGroupFingerprint(p.ID); fp != "" {
		return exceptionVerdictSignature(fp)
	}
	// v0.10.1027 — kural imzası chstore'daki TEK tanımdan (/api/databases/problems
	// da aynı fonksiyonla "problem değil" işaretlilerini dışlar).
	return chstore.RuleProblemVerdictSignature(p.RuleID, p.Service)
}

// exceptionVerdictSignature — SAF: exception / HTTP hata grubu imzası.
func exceptionVerdictSignature(fingerprint string) string {
	if fingerprint == "" {
		return ""
	}
	return "e:" + fingerprint
}

// noiseVerdictIndex — SAF: karar listesi → "problem değil" kararları.
// v0.10.1531 (R3) — gövde chstore.NoiseVerdictIndex'te (ortak tanım; küme
// yetmez: susturma işaretin zamanına, kalıcılığına ve öncelik/büyüklüğüne bakar).
func noiseVerdictIndex(list []chstore.ProblemVerdict) map[string]chstore.ProblemVerdict {
	return chstore.NoiseVerdictIndex(list)
}

// loadVerdictSilence — politika + (yalnız açıksa) karar listesi. Herhangi bir
// okuma hatası = susturma yok.
func (n *Notifier) loadVerdictSilence(ctx context.Context) (bool, map[string]chstore.ProblemVerdict) {
	pol, err := n.store.GetProblemVerdictPolicy(ctx)
	if err != nil {
		log.Printf("[notify] problem verdict policy fetch: %v", err)
		return false, nil
	}
	if !pol.MuteNotifications {
		return false, nil
	}
	list, err := n.store.ListProblemVerdicts(ctx)
	if err != nil {
		log.Printf("[notify] problem verdicts fetch: %v", err)
		return false, nil
	}
	return true, noiseVerdictIndex(list)
}

// verdictSilenced — bu imza "problem değil" işaretli, politika açık ve işaret
// bu bildirimi hâlâ susturuyor mu (v0.10.1531 R3 / D5: kalıcı yönetici
// gizlemesi her zaman; değilse yalnız noiseMuteHours içinde ve baypas yoksa —
// chstore.NoiseSilences). std yalnız işaret bulunursa kurulur (tembel): imzasız
// ya da işaretsiz bildirim için standart hesaplanmaz. Önbellek
// verdictSilenceTTL'den eskiyse tazeler.
func (n *Notifier) verdictSilenced(ctx context.Context, sig string, std func() (chstore.ProblemStandard, string)) bool {
	if n == nil || n.store == nil || sig == "" {
		return false
	}
	n.pvMu.Lock()
	if time.Since(n.pvAt) > verdictSilenceTTL {
		n.pvMute, n.pvNoise = n.loadVerdictSilence(ctx)
		n.pvAt = time.Now()
	}
	mute := n.pvMute
	v, ok := n.pvNoise[sig]
	n.pvMu.Unlock()
	if !mute || !ok {
		return false
	}
	s, prio := chstore.ProblemStandard{}, ""
	if std != nil {
		s, prio = std()
	}
	return chstore.NoiseSilences(chstore.NoiseMarkOf(&v), s, prio, chstore.CurrentNotifyPolicy(), time.Now())
}

// problemSilenceStd — Problem için tembel standart (trafik okuması YOK: susturma
// kararı gönderim yolunda bütçesiz; büyüklük / öncelik / güven kaydın kendisinden).
func problemSilenceStd(p chstore.Problem) func() (chstore.ProblemStandard, string) {
	return func() (chstore.ProblemStandard, string) {
		return chstore.StandardizeProblem(p, chstore.StdFacts{Now: time.Now()}),
			chstore.NotifyViewPriority(p, chstore.CurrentProblemPriority().InboxKeepSourcePriorityList())
	}
}

// exceptionSilenceStd — exception grubu için tembel standart; prio grubun
// merdiven önceliği (görünüm önceliğiyle aynı, D2).
func exceptionSilenceStd(g chstore.ExceptionGroup, prio string) func() (chstore.ProblemStandard, string) {
	return func() (chstore.ProblemStandard, string) {
		g.Priority = prio
		return chstore.StandardizeException(g, chstore.StdFacts{Now: time.Now()}), prio
	}
}
