package rollout

// origin.go — v0.10.1445 — Rollouts: geri almanın KAYNAĞI (git). SAF: G/Ç yok.
//
// KSM dedektörü (v2detect.go) `change_type=rollback`'i yalnız pod şablonu bilinen
// bir revizyona döndüğünde basar; Kubernetes şablon bayt bayt aynıysa eski
// ReplicaSet'i yeniden kullanır, bu yüzden `kubectl rollout undo` ile GitOps
// revert'i KSM'de özdeştir ve "git ileri gitti ama imaj eski" (redeploy of an
// older commit) ayrılamaz. Ayrımı Argo CD senkron kaydı yapar
// (argocd_sync_events; webhook yazıcısı internal/argocd/sync_event.go):
//
//   - rollback       — Argo, DAHA ÖNCE senkronlanmış bir revizyona döndü (git geri)
//   - redeploy_older — YENİ bir git revizyonu, ama yapı eski bir sürüme döndü
//   - forward        — yeni revizyon, yeni yapı (ya da yalnız config)
//   - unknown        — Argo kanıtı yok / belirsiz / aynı revizyon
//
// DEĞİŞMEZ (TestClassifyRolloutOriginNoArgoIsAlwaysUnknown): Argo kanıtı yoksa
// sonuç HER ZAMAN unknown — imaj tag sırası tek başına geri alma kanıtı değildir
// (sha / latest sırasız; tag yalnız kural 6'da eşitlik bozucu). Doğruluk tablosu
// docs/DECISIONS.md v0.10.1445 ve plan §2.2 ile birebir; ilk eşleşen kural kazanır.

import (
	"fmt"
	"regexp"
	"strings"
)

// Origin — rollout'un git kaynağı sınıfı.
type Origin string

const (
	OriginRollback      Origin = "rollback"
	OriginRedeployOlder Origin = "redeploy_older"
	OriginForward       Origin = "forward"
	OriginUnknown       Origin = "unknown"
)

// TagOrder — yeni imaj tag'inin öncekine göre sırası.
type TagOrder string

const (
	TagOlder     TagOrder = "older"
	TagNewer     TagOrder = "newer"
	TagEqual     TagOrder = "equal"
	TagUnordered TagOrder = "unordered"
)

// Origin kanıt tabanları (JSON `basis`).
const (
	OriginBasisArgo = "argocd_webhook"
	OriginBasisNone = "none"
)

// OriginInput — sınıflandırıcının girdileri (api/rollouts_origin.go doldurur).
type OriginInput struct {
	KSMChangeType    string // V2Event.ChangeType: rollout | config | rollback | initial
	PrevTag, NewTag  string // birincil imaj çiftinin tag'leri (PrimaryImagePair + SplitImageRef)
	ArgoNewRevision  string // rollout'a eşlenen senkronun revizyonu ("" = Argo kanıtı yok)
	ArgoPrevRevision string // aynı uygulamanın ondan hemen önceki Succeeded senkronu
	SeenBefore       bool   // ArgoNewRevision, ArgoPrev'den ÖNCEKİ bir Succeeded senkronda görüldü
	HistoryDepth     int    // uygulamanın bilinen önceki Succeeded senkron sayısı (güven girdisi)
	Ambiguous        bool   // iş yüküne >1 uygulama ya da pencerede >1 senkron
	AmbiguousApps    int    // gerekçe metni için
	AmbiguousSyncs   int
	WindowMin        int // eşleşme penceresi (± dk), gerekçe metni için
}

// OriginResult — sınıf + güven (0..100) + tek cümle EN gerekçe + tag sırası.
type OriginResult struct {
	Origin     Origin
	Confidence uint8
	Reason     string
	TagOrder   TagOrder
}

// ClassifyRolloutOrigin — v0.10.1445 — plan §2.2 doğruluk tablosu (kural 0–7).
func ClassifyRolloutOrigin(in OriginInput) OriginResult {
	order := CompareImageTags(in.PrevTag, in.NewTag)
	res := func(o Origin, conf int, reason string) OriginResult {
		if conf < 0 {
			conf = 0
		}
		return OriginResult{Origin: o, Confidence: uint8(conf), Reason: reason, TagOrder: order}
	}
	newRev := strings.TrimSpace(in.ArgoNewRevision)
	prevRev := strings.TrimSpace(in.ArgoPrevRevision)
	ct := strings.TrimSpace(in.KSMChangeType)
	switch {
	case in.Ambiguous: // 0
		return res(OriginUnknown, 0, fmt.Sprintf("ambiguous: %d Argo apps / %d syncs matched this workload in the window", in.AmbiguousApps, in.AmbiguousSyncs))
	case newRev == "": // 1 — DEĞİŞMEZ: Argo kanıtı yoksa tag'e bakılmaz
		return res(OriginUnknown, 0, fmt.Sprintf("no Argo sync matched this rollout (±%dm); git source unknown", in.WindowMin))
	case newRev == prevRev: // 2
		return res(OriginUnknown, 0, "same git revision re-synced (self-heal / restart); no version movement")
	}
	depth := fmt.Sprintf("history depth %d", in.HistoryDepth)
	if in.SeenBefore {
		if ct == V2ChangeRollback { // 3
			return res(OriginRollback, 95, "Argo re-pointed to an earlier synced revision and Kubernetes re-activated the known ReplicaSet")
		}
		if ct == V2ChangeRollout || ct == V2ChangeConfig || ct == V2ChangeInitial { // 4
			return res(OriginRollback, 75, "Argo re-pointed to an earlier synced revision (old ReplicaSet already garbage-collected)")
		}
		return res(OriginUnknown, 0, fmt.Sprintf("Argo re-pointed to an earlier synced revision but the Kubernetes change type %q is unknown", ct))
	}
	switch ct {
	case V2ChangeRollback: // 5
		conf := 85
		if in.HistoryDepth < 3 {
			conf -= 15
		}
		return res(OriginRedeployOlder, conf, "new git revision, but the pod template returned to a previously seen ReplicaSet: a forward commit shipping an older build ("+depth+")")
	case V2ChangeRollout:
		if order == TagOlder { // 6
			return res(OriginRedeployOlder, 60, "new git revision; image tag ordered older than the previous one (tag ordering only; "+depth+")")
		}
		if order == TagEqual { // 7
			return res(OriginForward, 80, "new git revision, same image tag")
		}
		return res(OriginForward, 80, "new git revision, new build") // 7
	case V2ChangeConfig: // 7
		return res(OriginForward, 80, "new git revision, config-only change")
	case V2ChangeInitial: // 7
		return res(OriginForward, 80, "new git revision; first rollout seen for this workload")
	}
	return res(OriginUnknown, 0, fmt.Sprintf("new git revision but the Kubernetes change type %q is unknown", ct))
}

// originPlaceholderTags — chstore/deploys.go placeholderVersionList'in ikizi
// (paket döngüsü: chstore → rollout). Sırasız: gerçek bir yapı sürümü değil.
var originPlaceholderTags = map[string]bool{
	"0.0.1": true, "0.0.1-snapshot": true, "0.1.0-snapshot": true, "1.0-snapshot": true, "1.0.0-snapshot": true,
	"${project.version}": true, "${version}": true, "latest": true, "dev": true, "unknown": true, "snapshot": true,
	"main": true, "master": true, "head": true, "null": true, "none": true, "n/a": true,
}

var (
	tagShaRE    = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	tagLetterRE = regexp.MustCompile(`[a-f]`)
	tagDigitsRE = regexp.MustCompile(`[0-9]+`)
)

// tagSkeleton — rakam grupları '#' ile değiştirilmiş iskelet + gruplar.
func tagSkeleton(tag string) (string, []string) {
	t := strings.TrimPrefix(strings.ToLower(tag), "v")
	return tagDigitsRE.ReplaceAllString(t, "#"), tagDigitsRE.FindAllString(t, -1)
}

// cmpDigits — iki ondalık rakam dizisini sayı olarak karşılaştırır (taşma yok).
func cmpDigits(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// CompareImageTags — v0.10.1445 — SAF: new'in prev'e göre sırası. Sıralanabilir
// yalnız AYNI iskelete sahip ve rakam grubu taşıyan tag'ler (semver `1.4.2`,
// tarih `2026.10.18.3`, `release-2026.08.21.3`); sha (7–40 hex), placeholder
// (`latest`, `0.0.1-SNAPSHOT`…), boş ya da iskeleti farklı (karışık) → unordered.
func CompareImageTags(prev, new string) TagOrder {
	p, n := strings.TrimSpace(prev), strings.TrimSpace(new)
	if p == "" || n == "" || originPlaceholderTags[strings.ToLower(p)] || originPlaceholderTags[strings.ToLower(n)] {
		return TagUnordered
	}
	if p == n {
		return TagEqual
	}
	isSha := func(s string) bool { s = strings.ToLower(s); return tagShaRE.MatchString(s) && tagLetterRE.MatchString(s) }
	if isSha(p) || isSha(n) {
		return TagUnordered
	}
	ps, pd := tagSkeleton(p)
	ns, nd := tagSkeleton(n)
	if ps != ns || len(pd) == 0 {
		return TagUnordered
	}
	for i := range pd {
		switch cmpDigits(nd[i], pd[i]) {
		case -1:
			return TagOlder
		case 1:
			return TagNewer
		}
	}
	return TagEqual
}

// RolloutOrigin — v0.10.1445 — API satırının `origin` nesnesi
// (chstore.RolloutRow.MarshalJSON yalnız nil değilse yazar; lib/types.ts
// RolloutOrigin aynası). Zamanlar ms.
type RolloutOrigin struct {
	Kind           Origin     `json:"kind"`
	Confidence     uint8      `json:"confidence"`
	Reason         string     `json:"reason"`
	Basis          string     `json:"basis"`
	App            *OriginApp `json:"app,omitempty"`
	PrevRevision   string     `json:"prevRevision,omitempty"`
	NewRevision    string     `json:"newRevision,omitempty"`
	PrevRevisionAt int64      `json:"prevRevisionAt,omitempty"`
	NewRevisionAt  int64      `json:"newRevisionAt,omitempty"`
	FirstSyncedAt  int64      `json:"firstSyncedAt,omitempty"` // SeenBefore: yeni revizyonun ilk (daha eski) senkronu
	PrevTag        string     `json:"prevTag,omitempty"`
	NewTag         string     `json:"newTag,omitempty"`
	TagOrder       TagOrder   `json:"tagOrder,omitempty"`
	Automated      *bool      `json:"automated,omitempty"`
	Initiator      string     `json:"initiator,omitempty"`
	HistoryDepth   int        `json:"historyDepth,omitempty"`
}

// OriginApp — eşlenen Argo CD uygulaması (argocd_app_mapping kenarı).
type OriginApp struct {
	InstanceID   string `json:"instanceId"`
	AppNamespace string `json:"appNamespace"`
	AppName      string `json:"appName"`
	MatchMethod  string `json:"matchMethod,omitempty"`
	MatchClass   string `json:"matchClass,omitempty"`
}
