package argocd

// sync_event.go — v0.10.1445 — Argo CD Notifications webhook gövdesi →
// argocd_sync_events satırları (docs/rollouts/v2-audit.md §10.3.4; DDL
// chstore/rollout_v2_schema.go ≡ migrations/0015). SAF: ağ yok, CH yok.
// Yazıcı api/argocd_sync_webhook.go (POST /api/argocd/sync-events), okuyucu
// api/rollouts_origin.go (geri almanın kaynağı sınıflandırması).
//
// Bir webhook İKİ tür satır üretir:
//   - canlı satır: operationState (faz, başlangıç/bitiş, başlatan, revizyon,
//     senkronlanan + yönetilen iş yükleri), version = now (ns);
//   - tohum satırları: status.history[] — her girdi (revizyon + deployStartedAt,
//     yoksa deployedAt) bir Succeeded satırı. "Bu revizyon daha önce senkronlandı
//     mı" sorusu API işçisi olmadan cevaplanır; Argo 10 girdi tutar, Coremetry
//     TTL boyunca biriktirir.
//
// NEDEN tohum version'ı ms ölçeğinde (plan "now−1" DEĞİL): tohumun anahtarı
// (instance, ns, app, op_started_at) aynı senkronun canlı satırıyla AYNI
// (Argo history.deployStartedAt = operationState.startedAt). now−1 yalnız AYNI
// webhook içinde canlı satırı korur; bir SONRAKİ webhook'un tohumu (now₂−1 >
// now₁) önceki canlı satırı ezip automated/initiator/iş yükü kolonlarını
// silerdi. Tohum version'ı = op_started_at Unix ms (~1.7e12) her canlı
// version'ın (Unix ns, ~1.7e18) altındadır — saat kaymasından bağımsız —
// ve aynı tohumun tekrarı bayt bayt aynı satırdır (RMT tekilleştirir).

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

// Webhook gövdesinin tavanları (plan §2.4).
const (
	SyncWebhookMaxBody      = 256 << 10
	SyncWebhookMaxHistory   = 20
	SyncWebhookMaxResources = 500
	SyncWebhookMaxRevisions = 20
	SyncWebhookMaxString    = 256
)

// Argo operationState.phase sözlüğü.
var syncPhases = map[string]bool{"Running": true, "Succeeded": true, "Failed": true, "Error": true, "Terminating": true}

// SyncPhaseSucceeded — başarılı senkron fazı.
const SyncPhaseSucceeded = "Succeeded"

// syncWorkloadKinds — synced/managed listelerinde tutulan iş yükü türleri.
var syncWorkloadKinds = map[string]bool{"Deployment": true, "StatefulSet": true, "DaemonSet": true, "DeploymentConfig": true, "Rollout": true}

// SyncEventRow — argocd_sync_events TAM satırı; alan sırası ve `ch` etiketleri
// DDL ile BİREBİR (TestSyncEventRowMirrorsDDL).
type SyncEventRow struct {
	InstanceID       string    `ch:"instance_id"`
	AppNamespace     string    `ch:"app_namespace"`
	AppName          string    `ch:"app_name"`
	OpStartedAt      time.Time `ch:"op_started_at"`
	Phase            string    `ch:"phase"`
	FinishedAt       time.Time `ch:"finished_at"` // sıfır = yok (DEFAULT 0)
	Automated        uint8     `ch:"automated"`
	Initiator        string    `ch:"initiator"`
	SyncRevision     string    `ch:"sync_revision"`
	Revisions        []string  `ch:"revisions"`
	RepoURL          string    `ch:"repo_url"`
	TargetRevision   string    `ch:"target_revision"`
	HistoryID        int64     `ch:"history_id"`
	SyncedWorkloads  []string  `ch:"synced_workloads"`
	ManagedWorkloads []string  `ch:"managed_workloads"`
	DestServer       string    `ch:"dest_server"`
	DestNamespace    string    `ch:"dest_namespace"`
	ClusterID        string    `ch:"cluster_id"`
	RetryCount       uint32    `ch:"retry_count"`
	Message          string    `ch:"message"`
	Version          uint64    `ch:"version"`
}

// SyncEventColumns — INSERT/SELECT kolon listesi, SyncEventRow sırasıyla.
func SyncEventColumns() []string {
	rt := reflect.TypeOf(SyncEventRow{})
	out := make([]string, rt.NumField())
	for i := range out {
		out[i] = rt.Field(i).Tag.Get("ch")
	}
	return out
}

// Key — satırın uygulama anahtarı.
func (r SyncEventRow) Key() AppKey { return AppKey{r.InstanceID, r.AppNamespace, r.AppName} }

// Identity — senkronun git kimliği: sync_revision; çok kaynaklı uygulamada
// (syncResult.revision boş) sıralı revisions[] birleşimi.
func (r SyncEventRow) Identity() string {
	if r.SyncRevision != "" {
		return r.SyncRevision
	}
	if len(r.Revisions) == 0 {
		return ""
	}
	rs := append([]string(nil), r.Revisions...)
	sort.Strings(rs)
	return strings.Join(rs, ",")
}

// syncSeedVersionMax — tohum version'ı Unix ms (~1.7e12), canlı version Unix
// ns (~1.7e18); aradaki her eşik ikisini ayırır (dosya başı).
const syncSeedVersionMax = uint64(1e15)

// IsHistorySeed — satır status.history[] tohumu mu (canlı operationState
// değil: automated/initiator/iş yükü kolonları boş).
func (r SyncEventRow) IsHistorySeed() bool { return r.Version < syncSeedVersionMax }

// SyncWebhookPayload — Argo CD Notifications şablonunun gövdesi
// (docs/DEPLOY-EVENTS.md "Argo CD senkron webhook'u").
type SyncWebhookPayload struct {
	InstanceID string `json:"instanceId"`
	App        struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	} `json:"app"`
	Operation struct {
		Phase      string `json:"phase"`
		StartedAt  string `json:"startedAt"`
		FinishedAt string `json:"finishedAt"`
		Automated  bool   `json:"automated"`
		Initiator  string `json:"initiator"`
		RetryCount int64  `json:"retryCount"`
		Message    string `json:"message"`
	} `json:"operation"`
	Sync struct {
		Revision       string   `json:"revision"`
		Revisions      []string `json:"revisions"`
		RepoURL        string   `json:"repoURL"`
		TargetRevision string   `json:"targetRevision"`
	} `json:"sync"`
	History     []SyncWebhookHistory `json:"history"`
	Resources   []string             `json:"resources"`
	Managed     []string             `json:"managed"`
	Destination struct {
		Server    string `json:"server"`
		Namespace string `json:"namespace"`
	} `json:"destination"`
}

// SyncWebhookHistory — status.history[] girdisi.
type SyncWebhookHistory struct {
	ID              int64  `json:"id"`
	Revision        string `json:"revision"`
	DeployStartedAt string `json:"deployStartedAt"`
	DeployedAt      string `json:"deployedAt"`
}

// ErrSyncWebhookInvalid — 400'e eşlenen doğrulama hatalarının kökü.
var ErrSyncWebhookInvalid = errors.New("argocd sync webhook")

func syncInvalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrSyncWebhookInvalid, fmt.Sprintf(format, a...))
}

// cleanStr — kırp, Go şablonunun eksik anahtar dizesini ("<no value>") boşalt,
// SyncWebhookMaxString'e kes.
func cleanStr(s string) string {
	s = strings.TrimSpace(s)
	if s == "<no value>" || s == "<nil>" {
		return ""
	}
	if len(s) > SyncWebhookMaxString {
		s = s[:SyncWebhookMaxString]
	}
	return s
}

// argoTimeLayouts — RFC3339(Nano) (unstructured JSON) ve Go'nun time.Time
// String() biçimi (metav1.Time şablonda böyle basılabilir).
var argoTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05 -0700 MST",
	"2006-01-02 15:04:05 -0700 -0700",
	"2006-01-02T15:04:05",
}

// ParseArgoTime — SAF: boş / "<no value>" / sıfır zaman → (sıfır, false).
func ParseArgoTime(s string) (time.Time, bool) {
	s = cleanStr(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, l := range argoTimeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			if t.Unix() <= 0 {
				return time.Time{}, false
			}
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// FilterSyncWorkloads — SAF: "Kind/namespace/name" girdilerinden iş yükü
// türlerini tutar (tekil, girdi sırası), en çok SyncWebhookMaxResources.
func FilterSyncWorkloads(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range in {
		e := cleanStr(raw)
		parts := strings.Split(e, "/")
		if len(parts) != 3 || !syncWorkloadKinds[parts[0]] || parts[1] == "" || parts[2] == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
		if len(out) == SyncWebhookMaxResources {
			break
		}
	}
	return out
}

// RowsFromSyncWebhook — v0.10.1445 — SAF: webhook gövdesi → canlı satır +
// history tohum satırları. clusterOf dest_server'ı Remote Cluster
// EffectiveID'sine çözer (DestClusterID; nil → boş). Doğrulama hatası
// ErrSyncWebhookInvalid sarar (API 400).
func RowsFromSyncWebhook(p SyncWebhookPayload, now time.Time, clusterOf func(destServer string) string) ([]SyncEventRow, error) {
	inst, ns, name := cleanStr(p.InstanceID), cleanStr(p.App.Namespace), cleanStr(p.App.Name)
	if inst == "" {
		return nil, syncInvalid("instanceId required")
	}
	if ns == "" || name == "" {
		return nil, syncInvalid("app.namespace and app.name required")
	}
	started, ok := ParseArgoTime(p.Operation.StartedAt)
	if !ok {
		return nil, syncInvalid("operation.startedAt missing or unparseable: %q", cleanStr(p.Operation.StartedAt))
	}
	phase := cleanStr(p.Operation.Phase)
	if !syncPhases[phase] {
		return nil, syncInvalid("operation.phase %q (Running|Succeeded|Failed|Error|Terminating)", phase)
	}
	rev := cleanStr(p.Sync.Revision)
	var revs []string
	for _, r := range p.Sync.Revisions {
		if r = cleanStr(r); r != "" && len(revs) < SyncWebhookMaxRevisions {
			revs = append(revs, r)
		}
	}
	if phase == SyncPhaseSucceeded && rev == "" && len(revs) == 0 {
		return nil, syncInvalid("Succeeded sync without sync.revision")
	}
	if revs == nil {
		revs = []string{}
	}
	finished, _ := ParseArgoTime(p.Operation.FinishedAt)
	ds, dns := cleanStr(p.Destination.Server), cleanStr(p.Destination.Namespace)
	cluster := ""
	if clusterOf != nil {
		cluster = clusterOf(ds)
	}
	var automated uint8
	if p.Operation.Automated {
		automated = 1
	}
	retry := p.Operation.RetryCount
	if retry < 0 {
		retry = 0
	}
	hist := p.History
	if len(hist) > SyncWebhookMaxHistory {
		hist = hist[len(hist)-SyncWebhookMaxHistory:] // Argo artan id: en yeniler sonda
	}
	historyID := int64(-1)
	for _, h := range hist {
		if rev != "" && cleanStr(h.Revision) == rev && h.ID > historyID {
			historyID = h.ID
		}
	}
	live := SyncEventRow{
		InstanceID: inst, AppNamespace: ns, AppName: name, OpStartedAt: started, Phase: phase, FinishedAt: finished,
		Automated: automated, Initiator: cleanStr(p.Operation.Initiator), SyncRevision: rev, Revisions: revs,
		RepoURL: cleanStr(p.Sync.RepoURL), TargetRevision: cleanStr(p.Sync.TargetRevision), HistoryID: historyID,
		SyncedWorkloads: FilterSyncWorkloads(p.Resources), ManagedWorkloads: FilterSyncWorkloads(p.Managed),
		DestServer: ds, DestNamespace: dns, ClusterID: cluster, RetryCount: uint32(min(retry, 1<<31)),
		Message: cleanStr(p.Operation.Message), Version: uint64(now.UnixNano()),
	}
	out := []SyncEventRow{live}
	seen := map[int64]bool{started.UnixMilli(): true}
	for _, h := range hist {
		hrev := cleanStr(h.Revision)
		at, ok := ParseArgoTime(h.DeployStartedAt)
		if !ok {
			at, ok = ParseArgoTime(h.DeployedAt)
		}
		if hrev == "" || !ok || seen[at.UnixMilli()] {
			continue
		}
		seen[at.UnixMilli()] = true
		done, _ := ParseArgoTime(h.DeployedAt)
		out = append(out, SyncEventRow{
			InstanceID: inst, AppNamespace: ns, AppName: name, OpStartedAt: at, Phase: SyncPhaseSucceeded, FinishedAt: done,
			SyncRevision: hrev, Revisions: []string{}, HistoryID: h.ID, SyncedWorkloads: []string{}, ManagedWorkloads: []string{},
			DestServer: ds, DestNamespace: dns, ClusterID: cluster,
			Version: uint64(at.UnixMilli()), // dosya başı: her canlı satırın (ns) altında
		})
	}
	return out, nil
}

// ValidateSyncEventRow — yazıcı sözleşmesi (SAF): anahtar alanları dolu,
// op_started_at gerçek zaman (TTL çapası), faz sözlükte, açık version.
func ValidateSyncEventRow(r SyncEventRow) error {
	var errs []error
	for _, f := range []struct{ name, v string }{{"instance_id", r.InstanceID}, {"app_namespace", r.AppNamespace}, {"app_name", r.AppName}} {
		if strings.TrimSpace(f.v) == "" {
			errs = append(errs, fmt.Errorf("%s boş", f.name))
		}
	}
	if epochish(r.OpStartedAt) {
		errs = append(errs, errors.New("op_started_at sıfır/epoch (TTL çapası)"))
	}
	if !syncPhases[r.Phase] {
		errs = append(errs, fmt.Errorf("phase %q sözlük dışı", r.Phase))
	}
	if r.Version == 0 {
		errs = append(errs, errors.New("version sıfır"))
	}
	return errors.Join(errs...)
}
