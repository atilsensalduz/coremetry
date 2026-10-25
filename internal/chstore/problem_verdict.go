package chstore

// problem_verdict.go — v0.10.1015 — Problems sekmesinde ÖĞRETME: operatörün bir
// satırın İMZASINA verdiği karar ("gerçek problem" / "problem değil").
//
// Operatör (2026-10-01): "Problems sekmesinde bütün hepsi gelsin, ben hangisi
// gerçek problem hangisi değil zamanla öğretelim." v0.10.1014 varsayılanı her
// şeye açtı; bu dosya öğretmenin kalıcı yarısı.
//
// Karar OLAYA değil İMZAYA bağlıdır: aynı kural + servis (ya da aynı exception
// grubu) yeniden geldiğinde kendiliğinden aynı sınıfa düşer — "öğrenme" budur,
// deterministik ve geri alınabilir (model yok). İmzayı FE üretir
// (lib/problemVerdict.ts inboxSignature); sunucu yalnız biçimini doğrular.
//
// Anomali OLAYI kararından (anomaly_verdict.go, v0.10.181: «anomali / değil»,
// event_id başına, dedektör istatistiği için) AYRIDIR: o tek bir olayı etiketler,
// bu triage listesinde bir imzanın yerini belirler. Problem yaşam döngüsüne
// DOKUNMAZ; bildirimlere de — yönetici politikayı açmadıkça (v0.10.1016,
// dosya sonu: ProblemVerdictPolicy; varsayılan kapalı = yalnız görünüm).
//
// Depolama: ortak durum tablosu saved_views (mimari değişmez 5: "kayıtlı durum
// için tek tablo, yüzey başına yeni şema yok"). Satır = bir imza:
//
//	id            "pv:" + imza        (aynı imza → aynı satır; RMT son yazanı tutar)
//	owner_id      ""                  (ekip ortak)
//	page          "problem-verdict"
//	name          karar: real | noise ("" = silinmiş; DeleteSavedView mezar taşı)
//	query_string  JSON {signature, label, kind, service, by}
//	created_at    karar anı
//
// v0.10.1024 — kod incelemesi (prod'da gözlenmedi): genel kayıtlı-görünüm ucu
// (POST /api/views, rol kapısız) her page değerini kabul ediyordu; viewer
// page="problem-verdict" ile bir satır yazınca okuma onu ekip kararı sayıyordu
// (editor kapısı + denetim atlanıyor; susturma politikası açıksa bildirim de
// susuyordu). Okuma artık yalnız SİSTEM satırına güvenir: owner_id = '' VE
// id = "pv:" + gövdedeki imza. Genel uç sistem sayfalarını ayrıca reddeder
// (IsSystemSavedViewPage, saved_view.go).

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const (
	ProblemVerdictPage  = "problem-verdict"
	ProblemVerdictReal  = "real"  // gerçek problem
	ProblemVerdictNoise = "noise" // problem değil

	// ProblemVerdictMax — saklanan en çok imza (liste okuması bu tavanla sınırlı).
	ProblemVerdictMax = 5000
	// problemVerdictSigMax — imza uzunluk tavanı (kural kimliği + servis adı sığar).
	problemVerdictSigMax = 400
)

// ProblemVerdict — bir imzanın kararı.
type ProblemVerdict struct {
	Signature string `json:"signature"`
	Verdict   string `json:"verdict"`           // real | noise
	Label     string `json:"label,omitempty"`   // işaretlenen satırın başlığı (gösterim)
	Kind      string `json:"kind,omitempty"`    // problem | exception | httperror | anomaly
	Service   string `json:"service,omitempty"` // işaretlenen satırın öznesi (gösterim)
	By        string `json:"by,omitempty"`
	At        int64  `json:"at"` // unix ns

	// v0.10.1531 (Problem standardı R3, D5) — "problem değil"in gerekçesi ve
	// etkisi. Reason ∈ NoiseReasons ("" = R3 öncesi işaret, gerekçesiz).
	// Permanent: yönetici kalıcı gizleme — bildirim HİÇ gitmez (NotifiableAt
	// hold "hidden"); değilse susturma zaman sınırlı (notify_policy
	// noiseMuteHours) ve baypaslı (öncelik yükseldi / etki ≥ 2× / yüksek
	// güvenli P1). Priority / Magnitude: işaret anındaki görünüm önceliği ve
	// standart büyüklük (baypas kıyası; bilinmiyorsa boş / 0).
	Reason    string  `json:"reason,omitempty"`
	Note      string  `json:"note,omitempty"`
	Permanent bool    `json:"permanent,omitempty"`
	Priority  string  `json:"priority,omitempty"`
	Magnitude float64 `json:"magnitude,omitempty"`
}

// "Problem değil" gerekçeleri (R3). Gerekçe dedektör başına isabet ölçümünün
// etiketidir: "too_sensitive" o dedektörün eşiğine, "wrong_attribution"
// özne çözümüne işaret eder.
const (
	NoiseReasonExpected    = "expected"          // beklenen davranış (batch / bakım / test)
	NoiseReasonSensitive   = "too_sensitive"     // eşik fazla hassas
	NoiseReasonKnown       = "known_accepted"    // bilinen ve kabul edilmiş sorun
	NoiseReasonAttribution = "wrong_attribution" // yanlış servis / kaynak atfı
	NoiseReasonOther       = "other"             // diğer (not ile)
)

// NoiseReasons — kapalı küme (sıra = menü sırası).
var NoiseReasons = []string{NoiseReasonExpected, NoiseReasonSensitive, NoiseReasonKnown, NoiseReasonAttribution, NoiseReasonOther}

// ValidNoiseReason — SAF: "" (gerekçesiz, R3 öncesi uyum) ya da kapalı küme.
func ValidNoiseReason(r string) bool {
	if r == "" {
		return true
	}
	for _, x := range NoiseReasons {
		if x == r {
			return true
		}
	}
	return false
}

// NoiseMarkOf — SAF: karar → bildirim politikasının "problem değil" işareti
// (gerçek karar / nil → nil). Tek tanım: gölge, replay ve gönderim kapısı.
func NoiseMarkOf(v *ProblemVerdict) *NoiseMark {
	if v == nil || v.Verdict != ProblemVerdictNoise {
		return nil
	}
	return &NoiseMark{At: v.At, Permanent: v.Permanent, Priority: v.Priority, Magnitude: v.Magnitude}
}

// ValidProblemVerdict — SAF: yalnız iki karar.
func ValidProblemVerdict(v string) bool { return v == ProblemVerdictReal || v == ProblemVerdictNoise }

// ValidProblemSignature — SAF: imza biçimi. Önek satırın kaynağını söyler
// (p: alarm kuralı, e: exception / HTTP hata grubu, a: anomali); gövde boş
// olamaz, kontrol karakteri taşıyamaz, tavanı aşamaz.
func ValidProblemSignature(sig string) bool {
	if len(sig) < 3 || len(sig) > problemVerdictSigMax {
		return false
	}
	switch sig[:2] {
	case "p:", "e:", "a:":
	default:
		return false
	}
	if strings.TrimSpace(sig[2:]) == "" {
		return false
	}
	for _, r := range sig {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// RuleProblemVerdictSignature — SAF (v0.10.1027): alarm kuralı problemi imzası
// `p:<ruleId>|<özne>` — FE lib/problemVerdict.ts inboxSignature 'problem'
// dalının sunucu ikizi. TEK sunucu tanımı: bildirim hunisi (notify
// verdictSignature) ve /api/databases/problems ikisi de bunu çağırır.
// Kural kimliği yoksa "" (öğretilemez).
func RuleProblemVerdictSignature(ruleID, subject string) string {
	if ruleID == "" {
		return ""
	}
	return "p:" + ruleID + "|" + subject
}

// NoiseVerdictSignatures — SAF (v0.10.1027): karar listesi → "problem değil"
// imza kümesi ("gerçek" kararlar girmez). notify ve /api/databases/problems
// ortak kullanır.
func NoiseVerdictSignatures(list []ProblemVerdict) map[string]struct{} {
	out := make(map[string]struct{})
	for _, v := range list {
		if v.Verdict == ProblemVerdictNoise {
			out[v.Signature] = struct{}{}
		}
	}
	return out
}

// NoiseVerdictIndex — SAF (v0.10.1531, R3): karar listesi → "problem değil"
// kararları (imza → karar). Küme yetmeyen yerler (bildirim susturması:
// zaman, kalıcılık, işaret anındaki öncelik/büyüklük) bunu okur;
// NoiseVerdictSignatures aynı süzgecin anahtar kümesidir.
func NoiseVerdictIndex(list []ProblemVerdict) map[string]ProblemVerdict {
	out := make(map[string]ProblemVerdict)
	for _, v := range list {
		if v.Verdict == ProblemVerdictNoise {
			out[v.Signature] = v
		}
	}
	return out
}

// problemVerdictIDPrefix — sistem karar satırlarının kimlik öneki. Genel
// kayıtlı-görünüm ucu kimliği rastgele üretir (newRandID), bu öneki taşıyamaz.
const problemVerdictIDPrefix = "pv:"

// problemVerdictID — SAF: imza → saved_views satır kimliği.
func problemVerdictID(sig string) string { return problemVerdictIDPrefix + sig }

type problemVerdictPayload struct {
	Signature string  `json:"signature"`
	Label     string  `json:"label,omitempty"`
	Kind      string  `json:"kind,omitempty"`
	Service   string  `json:"service,omitempty"`
	By        string  `json:"by,omitempty"`
	Reason    string  `json:"reason,omitempty"`    // v0.10.1531
	Note      string  `json:"note,omitempty"`      // v0.10.1531
	Permanent bool    `json:"permanent,omitempty"` // v0.10.1531
	Priority  string  `json:"priority,omitempty"`  // v0.10.1531
	Magnitude float64 `json:"magnitude,omitempty"` // v0.10.1531
}

// problemVerdictFromRow — SAF: saved_views satırı → karar. Bozuk gövde ya da
// geçersiz karar ok=false (liste onu atlar; yarım satır karar sayılmaz).
//
// v0.10.1024 — satır kimliği gövdedeki imzanın kimliği DEĞİLSE de ok=false:
// gerçek kararı yalnız SetProblemVerdict yazar ve o kimliği imzadan türetir
// (id = "pv:" + imza). Başka yoldan (genel POST /api/views, rastgele kimlik)
// yazılmış sahte bir satır karar olarak ASLA yüzeye çıkmaz.
func problemVerdictFromRow(id, name, query string, createdAt time.Time) (ProblemVerdict, bool) {
	if !ValidProblemVerdict(name) {
		return ProblemVerdict{}, false
	}
	var p problemVerdictPayload
	if err := json.Unmarshal([]byte(query), &p); err != nil || !ValidProblemSignature(p.Signature) {
		return ProblemVerdict{}, false
	}
	if id != problemVerdictID(p.Signature) {
		return ProblemVerdict{}, false
	}
	v := ProblemVerdict{Signature: p.Signature, Verdict: name, Label: p.Label, Kind: p.Kind,
		Service: p.Service, By: p.By, At: createdAt.UnixNano(),
		Reason: p.Reason, Note: p.Note, Permanent: p.Permanent, Priority: p.Priority, Magnitude: p.Magnitude}
	if v.Verdict != ProblemVerdictNoise || !ValidNoiseReason(v.Reason) {
		// Gerekçe / kalıcılık yalnız "problem değil"de anlamlı; bozuk gerekçe
		// kararı düşürmez, yalnız gerekçeyi boşaltır.
		v.Reason, v.Permanent = "", v.Permanent && v.Verdict == ProblemVerdictNoise
		if v.Verdict != ProblemVerdictNoise {
			v.Note = ""
		}
	}
	return v, true
}

// problemVerdictListSQL — ListProblemVerdicts okuması (testte pinli).
//
// v0.10.1024 — boş owner_id ve `startsWith(id, 'pv:')` koşulu SQL'de, yalnız
// codec'te değil: kişiye ait ya da rastgele kimlikli satırlar LIMIT
// penceresine hiç girmez — sahte satır seli gerçek kararları 5000 tavanının
// dışına itemez, PUT'un tavan denetimini de doldurup kilitleyemez. id ORDER BY
// anahtarıdır; önek koşulu birincil anahtarla budanır.
const problemVerdictListSQL = `
		SELECT id, name, query_string, created_at
		FROM saved_views FINAL
		WHERE page = ? AND owner_id = '' AND startsWith(id, '` + problemVerdictIDPrefix + `') AND name != ''
		ORDER BY created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`

// ListProblemVerdicts — tüm kararlar, en yeni önce (≤ ProblemVerdictMax).
// saved_views küçük bir durum tablosudur; okuma sayfa süzgeçli + LIMIT'li.
// v0.10.1024: yalnız sistem satırları (problemVerdictListSQL + codec kimlik
// denetimi).
func (s *Store) ListProblemVerdicts(ctx context.Context) ([]ProblemVerdict, error) {
	rows, err := s.conn.Query(ctx, problemVerdictListSQL, ProblemVerdictPage, ProblemVerdictMax)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProblemVerdict{}
	for rows.Next() {
		var id, name, query string
		var createdAt time.Time
		if err := rows.Scan(&id, &name, &query, &createdAt); err != nil {
			return nil, err
		}
		if v, ok := problemVerdictFromRow(id, name, query, createdAt); ok {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

// SetProblemVerdict — imzanın kararını yazar (aynı imza → aynı satır).
func (s *Store) SetProblemVerdict(ctx context.Context, v ProblemVerdict) error {
	raw, err := json.Marshal(problemVerdictPayload{Signature: v.Signature, Label: v.Label, Kind: v.Kind, Service: v.Service, By: v.By,
		Reason: v.Reason, Note: v.Note, Permanent: v.Permanent, Priority: v.Priority, Magnitude: v.Magnitude})
	if err != nil {
		return err
	}
	return s.UpsertSavedView(ctx, SavedView{ID: problemVerdictID(v.Signature), Name: v.Verdict,
		Page: ProblemVerdictPage, QueryString: string(raw), CreatedAt: v.At})
}

// ClearProblemVerdict — imzanın kararını kaldırır (mezar taşı; satır yoksa no-op).
func (s *Store) ClearProblemVerdict(ctx context.Context, signature string) error {
	return s.DeleteSavedView(ctx, problemVerdictID(signature))
}

// ── v0.10.1016 — karar POLİTİKASI: "problem değil" bildirimi de sustursun mu ──
//
// Varsayılan KAPALI: kararlar yalnız görünümdür (v0.10.1015). Yönetici açarsa
// "problem değil" imzalı alarm kuralı problemleri ve exception / HTTP hata
// grupları dış kanallara ve ekip mailine GİTMEZ (kapı: internal/notify/
// verdict_silence.go). Problem yine açılır, listede ve canlı akışta görünür.

// ProblemVerdictPolicyKey — system_settings anahtarı.
const ProblemVerdictPolicyKey = "problem_verdict_policy"

// ProblemVerdictPolicy — kararların görünüm dışındaki etkisi.
type ProblemVerdictPolicy struct {
	MuteNotifications bool   `json:"muteNotifications"`
	UpdatedBy         string `json:"updatedBy,omitempty"`
	UpdatedAt         int64  `json:"updatedAt,omitempty"` // unix ns
}

// parseProblemVerdictPolicy — SAF: blob → politika. Boş / bozuk blob = sıfır
// değer (susturma KAPALI): okunamayan ayar alarm kaybettirmez.
func parseProblemVerdictPolicy(raw []byte) ProblemVerdictPolicy {
	var p ProblemVerdictPolicy
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return ProblemVerdictPolicy{}
	}
	return p
}

// GetProblemVerdictPolicy — saklanan politika (yoksa sıfır değer).
func (s *Store) GetProblemVerdictPolicy(ctx context.Context) (ProblemVerdictPolicy, error) {
	raw, err := s.GetSetting(ctx, ProblemVerdictPolicyKey)
	if err != nil {
		return ProblemVerdictPolicy{}, err
	}
	return parseProblemVerdictPolicy(raw), nil
}

// PutProblemVerdictPolicy — politikayı yazar.
func (s *Store) PutProblemVerdictPolicy(ctx context.Context, p ProblemVerdictPolicy) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, ProblemVerdictPolicyKey, raw)
}
