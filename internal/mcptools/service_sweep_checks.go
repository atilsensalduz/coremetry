package mcptools

// service_sweep_checks.go — v0.10.1287 (AI SRE Faz 1b — follow-up B'nin deploy
// + k8s bölümleri): service_health_sweep'in EK kontrolleri (SweepRequest.Checks).
//
//	deploys  ListDeploymentsWindow (list_deployments ile AYNI gövde: span sürüm
//	         geçişi + deploy olayı + K8s rollout), [çıpa − 2 sa, pencere sonu],
//	         servis, ≤10 satır, 3 s. classifyDeploys: çıpanın [−60 dk, +5 dk]
//	         aralığındaki satır → uyarı ("başlangıçtan 12 dk önce rollout rev 42
//	         (1.4.2←1.4.1)"); başarısız rollout (stalled / stuck / rolled_back) →
//	         kritik.
//	k8s      pods bölümünün AYNI okuması + bekleme sebebi
//	         (thanos.PodWaitingReasonQuery; ayrı bir pod okuması yok).
//	         classifyK8s: CrashLoopBackOff / ImagePullBackOff / ErrImagePull /
//	         CreateContainerConfigError … → kritik; OOMKilled YALNIZ pencerede
//	         restart artışı varsa kritik (eski OOM yok sayılır); Pending /
//	         Failed / Unknown → uyarı. Kubernetes Events ingest EDİLMİYOR —
//	         yalnız kube-state-metrics durumu okunur.
//
// Deploy bilgisi önceliğe KARIŞMAZ (CLAUDE.md triage: "fresh deploy"
// tetikleyicisi yok): burada yalnız taramanın bulgusudur, problem satırına ya
// da öncelik hesabına hiçbir yol yok.
//
// Argo CD Degraded/OutOfSync notu (B tasarımı) bu dilimde YOK: servis → Argo
// uygulaması eşlemesi api katmanında (argocd eşleme kenarları); mcptools'tan
// okunamıyor. Node / gürültülü komşu kontrolü Faz 2'de.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/healthverdict"
	"github.com/cilcenk/coremetry/internal/rollout"
)

// Kontrol adları (SweepRequest.Checks).
const (
	SweepCheckDeploys = "deploys"
	SweepCheckK8s     = "k8s"
)

// StandardSweepChecks — Checks nil iken koşanlar.
var StandardSweepChecks = []string{SweepCheckDeploys, SweepCheckK8s}

// Bölüm / bulgu türü adları.
const (
	sweepHopDeploys = "deploys"
	sweepHopK8s     = "k8s"
	sweepKindDeploy = "deploy"
	sweepKindK8s    = "k8s"
)

const (
	sweepCapDeploys   = 3 * time.Second
	deployLookback    = 2 * time.Hour
	deployNearBefore  = 60 * time.Minute
	deployNearAfter   = 5 * time.Minute
	deployCheckLimit  = 10
	deployFindingsMax = 3
)

// sweepChecksOf — SAF: istenen kontroller (nil → standart; bilinmeyen ad düşer).
func sweepChecksOf(req SweepRequest) map[string]bool {
	list := req.Checks
	if list == nil {
		list = StandardSweepChecks
	}
	out := map[string]bool{}
	for _, c := range list {
		switch c = strings.ToLower(strings.TrimSpace(c)); c {
		case SweepCheckDeploys, SweepCheckK8s:
			out[c] = true
		}
	}
	return out
}

// sweepChecksKey — SAF: önbellek anahtarı parçası (nil ≠ boş).
func sweepChecksKey(req SweepRequest) string {
	if req.Checks == nil {
		return "std"
	}
	m := sweepChecksOf(req)
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return "[" + strings.Join(ks, ",") + "]"
}

// sweepAnchor — deploy kontrolünün çıpası (sıfır → pencere sonu).
func sweepAnchor(req SweepRequest) time.Time {
	if req.Anchor.IsZero() {
		return req.To
	}
	return req.Anchor
}

// ── deploys ─────────────────────────────────────────────────────────────

// deployCheckRows — ListDeploymentsWindow'un satırları + rollouts katmanı durumu.
func deployCheckRows(ctx context.Context, d Deps, service string, from, to time.Time) ([]ChangeRow, string, error) {
	out, err := ListDeploymentsWindow(ctx, d, ListDeploymentsArgs{Service: service, Limit: deployCheckLimit}, from, to)
	if err != nil {
		return nil, "", err
	}
	layer := ""
	if src, ok := out["sources"].(map[string]any); ok {
		layer, _ = src["rollouts_layer"].(string)
	}
	return ChangesOf(out), layer, nil
}

// deployFailedStatus — SAF: başarısız rollout durumu mu (v1 + v2 sözlükleri).
func deployFailedStatus(st string) bool {
	switch strings.ToLower(strings.TrimSpace(st)) {
	case rollout.StatusStalled, rollout.StatusRolledBack, rollout.V2StatusStuck, "failed":
		return true
	}
	return false
}

// deployRelTR — SAF: "başlangıçtan 12 dk önce" / "başlangıçtan 3 dk sonra".
func deployRelTR(at, anchor time.Time) string {
	d := at.Sub(anchor)
	m := int(math.Round(math.Abs(d.Minutes())))
	switch {
	case m == 0:
		return "başlangıçla aynı dakikada"
	case d < 0:
		return fmt.Sprintf("başlangıçtan %d dk önce", m)
	}
	return fmt.Sprintf("başlangıçtan %d dk sonra", m)
}

// deploySourceTR — SAF: satır kaynağının adı.
func deploySourceTR(src string) string {
	switch src {
	case "rollout":
		return "rollout"
	case "event":
		return "deploy olayı"
	}
	return "sürüm geçişi"
}

// classifyDeploys — SAF: değişiklik satırları → deploy adayları. Çıpanın
// [−60 dk, +5 dk] aralığı → uyarı; başarısız rollout (pencerenin herhangi
// bir yerinde) → kritik. Etki = çıpaya yakınlık (1 → 0). Aynı ad+sürüm bir kez.
func classifyDeploys(rows []ChangeRow, anchor time.Time) []sweepCand {
	var out []sweepCand
	seen := map[string]bool{}
	for _, r := range rows {
		at := time.Unix(0, r.TimeUnixNs)
		name := firstNonEmpty(r.Workload, r.Service)
		if r.Version != "" {
			name += "@" + r.Version
		}
		if seen[name] {
			continue
		}
		dt := at.Sub(anchor)
		near := dt >= -deployNearBefore && dt <= deployNearAfter
		failed := r.Source == "rollout" && deployFailedStatus(r.Status)
		if !near && !failed {
			continue
		}
		seen[name] = true
		ver := r.Version
		if r.PrevVersion != "" && ver != "" {
			ver += "←" + r.PrevVersion
		}
		why := deployRelTR(at, anchor) + " " + deploySourceTR(r.Source)
		if ver != "" {
			why += " (" + ver + ")"
		}
		c := sweepCand{Kind: sweepKindDeploy, Name: name, Namespace: r.Namespace, Cluster: r.Cluster, Level: levelWarn,
			Reasons: []string{why}, Hop: sweepHopDeploys}
		if near {
			c.Impact = round4(1 - math.Abs(dt.Minutes())/(deployNearBefore+deployNearAfter).Minutes())
		}
		if failed {
			c.Level = levelCrit
			c.Reasons = append(c.Reasons, "rollout "+r.Status)
			c.Impact = round4(c.Impact + 1)
		}
		out = append(out, c)
	}
	return out
}

// sweepRunDeploys — deploy bölümü (tarama ve AI SRE'nin deploy adımı ortak).
func sweepRunDeploys(ctx context.Context, r sweepReaders, service string, anchor, to time.Time) sweepOutcome {
	o := sweepOutcome{section: SweepSection{Name: sweepHopDeploys}}
	if r.deploys == nil {
		o.section.State, o.section.Summary = hopSkipped, "deploy okuyucusu bağlı değil"
		return o
	}
	from := anchor.Add(-deployLookback)
	if !to.After(from) {
		to = anchor.Add(deployNearAfter)
	}
	elapsed := stopwatch()
	rows, layer, err := r.deploys(ctx, service, from, to)
	ev := cosreEvidence{Source: evSourceCH, QueryKind: evKindSummary, Window: evWin(from, to), DurationMs: elapsed(),
		Query: "ListDeploymentsWindow service=" + sqlQuote(service) + " (span sürüm geçişi + deploy olayları + K8s rollouts, ≤" +
			fmt.Sprint(deployCheckLimit) + " satır, yeni → eski)"}
	evh := cosreHopEvidence{Hop: sweepHopDeploys, As: "list_deployments", Request: map[string]any{"service": service}}
	if err != nil {
		o.failed = true
		o.section.State, o.section.Summary = sweepFailState(err), cosreErrMsg("deploy geçmişi okunamadı", err)
		evh.cosreEvidence = ev
		o.evidences = append(o.evidences, evh)
		return o
	}
	ev.Rows = intPtr(len(rows))
	evh.cosreEvidence = ev
	o.evidences = append(o.evidences, evh)
	if layer != "" && layer != "ok" {
		o.section.Note = "rollouts katmanı kapalı/erişilemedi — yalnız span çıkarımı ve deploy olayları"
	}
	o.cands = classifyDeploys(rows, anchor)
	if len(o.cands) > deployFindingsMax {
		sort.SliceStable(o.cands, func(i, j int) bool {
			if a, b := healthverdict.LevelRank(o.cands[i].Level), healthverdict.LevelRank(o.cands[j].Level); a != b {
				return a < b
			}
			return o.cands[i].Impact > o.cands[j].Impact
		})
		o.cands = o.cands[:deployFindingsMax]
	}
	if len(rows) == 0 {
		o.section.State, o.section.Summary = hopEmpty, "veri yok: başlangıçtan önceki 2 saatte bu serviste deploy / rollout yok"
		return o
	}
	o.anyDataFound = true
	o.section.State = hopOK
	if len(o.cands) == 0 {
		o.section.Summary = fmt.Sprintf("%d değişiklik (son 2 sa); başlangıca yakın (−60/+5 dk) deploy yok", len(rows))
		return o
	}
	o.section.Summary = fmt.Sprintf("%d değişiklik (son 2 sa); başlangıca yakın: %s — %s", len(rows), o.cands[0].Name, o.cands[0].Reasons[0])
	return o
}

// DeployCheck — AI SRE'nin deploy adımının sonucu.
type DeployCheck struct {
	Section   SweepSection   `json:"section"`
	Findings  []SweepFinding `json:"findings"`
	Rows      []ChangeRow    `json:"rows"`
	Evidences []ChatEvidence `json:"evidences,omitempty"`
}

// ReadDeployCheck — v0.10.1287: taramanın deploy bölümü TEK BAŞINA (AI SRE
// orkestratörünün kendi 3 s'lik adımı). Hata yalnız çağıran iptal ettiğinde.
func ReadDeployCheck(ctx context.Context, d Deps, service string, anchor, to time.Time) (DeployCheck, error) {
	return readDeployCheck(ctx, sweepReadersFrom(d), service, anchor, to)
}

func readDeployCheck(ctx context.Context, r sweepReaders, service string, anchor, to time.Time) (DeployCheck, error) {
	var rows []ChangeRow
	if r.deploys != nil {
		inner := r.deploys
		r.deploys = func(c context.Context, svc string, from, to time.Time) ([]ChangeRow, string, error) {
			got, layer, err := inner(c, svc, from, to)
			rows = got
			return got, layer, err
		}
	}
	o := sweepRunDeploys(ctx, r, service, anchor, to)
	if errors.Is(ctx.Err(), context.Canceled) {
		return DeployCheck{}, ctx.Err()
	}
	findings, _ := scoreFindings(o.cands, deployFindingsMax)
	out := DeployCheck{Section: o.section, Findings: findings, Rows: rows}
	if out.Rows == nil {
		out.Rows = []ChangeRow{}
	}
	for i, h := range o.evidences {
		ref := fmt.Sprintf("evidences[%d]", i)
		ev := h.cosreEvidence
		out.Evidences = append(out.Evidences, ChatEvidence{Tool: ServiceHealthSweepName, Source: ev.Source, Query: ev.Query, QueryKind: ev.QueryKind,
			Window: ev.Window, Rows: ev.Rows, DurationMs: ev.DurationMs, Href: EvidenceHref(h.As, ev, h.Request), Hop: h.Hop})
		for j := range out.Findings {
			out.Findings[j].EvidenceRefs = append(out.Findings[j].EvidenceRefs, ref)
		}
	}
	return out, nil
}

// ── k8s ─────────────────────────────────────────────────────────────────

// K8sPodState — k8s sınıflandırıcısının girdisi (pods bölümünün satırı +
// bekleme sebebi + penceredeki restart artışı).
type K8sPodState struct {
	Cluster        string
	ClusterID      string
	Namespace      string
	Pod            string
	Phase          string
	WaitingReason  string
	LastTermReason string
	RestartsWindow *float64 // nil = okunamadı
}

// k8sCritWaiting — kendiliğinden düzelmeyen bekleme sebepleri.
var k8sCritWaiting = map[string]bool{
	"CrashLoopBackOff": true, "ImagePullBackOff": true, "ErrImagePull": true,
	"CreateContainerConfigError": true, "CreateContainerError": true,
	"InvalidImageName": true, "RunContainerError": true,
}

// classifyK8s — SAF: bir pod'un Kubernetes durumu → seviye + gerekçeler + etki.
// Etki sırası: bekleme sebebi (3) > pencerede OOM (2.8) > faz (2).
func classifyK8s(s K8sPodState) (level string, reasons []string, impact float64) {
	level = levelOK
	bump := func(l, why string, imp float64) {
		if l == levelCrit || (l == levelWarn && level == levelOK) {
			level = l
		}
		reasons = append(reasons, why)
		if imp > impact {
			impact = imp
		}
	}
	if w := strings.TrimSpace(s.WaitingReason); w != "" {
		if k8sCritWaiting[w] {
			bump(levelCrit, "bekliyor: "+w, 3)
		} else if w != "ContainerCreating" && w != "PodInitializing" {
			bump(levelWarn, "bekliyor: "+w, 1.5)
		}
	}
	if strings.EqualFold(s.LastTermReason, "OOMKilled") && s.RestartsWindow != nil && math.Round(*s.RestartsWindow) > 0 {
		bump(levelCrit, fmt.Sprintf("pencerede OOMKilled (%.0f restart)", math.Round(*s.RestartsWindow)), 2.8)
	}
	switch s.Phase {
	case "Pending", "Failed", "Unknown":
		bump(levelWarn, "faz "+s.Phase, 2)
	}
	return level, reasons, impact
}

// sweepK8sOutcome — pods bölümünün satırlarından k8s bölümü. Aynı pod'un pod
// adayı varsa k8s gerekçeleri ONA katılır (tek pod iki bulgu olmasın;
// seviye ikisinin kötüsü); yoksa k8s türünde ayrı aday. pods'u yerinde günceller.
func sweepK8sOutcome(pods *sweepOutcome) sweepOutcome {
	o := sweepOutcome{section: SweepSection{Name: sweepHopK8s}}
	switch pods.section.State {
	case hopSkipped:
		o.section.State, o.section.Summary = hopSkipped, "pod okuması yok: "+pods.section.Summary
		return o
	case hopError, hopTimeout:
		if len(pods.k8s) == 0 {
			o.section.State, o.section.Summary, o.failed = pods.section.State, "pod'lar okunamadı — Kubernetes durumu bilinmiyor (okunamadı ≠ temiz)", true
			return o
		}
	}
	idx := map[string]int{}
	for i, c := range pods.cands {
		if c.Kind == sweepKindPod {
			idx[c.Namespace+"\x00"+c.Name] = i
		}
	}
	counts := map[string]int{}
	var order []string
	for _, s := range pods.k8s {
		lvl, why, imp := classifyK8s(s)
		if lvl == levelOK {
			continue
		}
		for _, w := range why {
			k := strings.TrimPrefix(w, "bekliyor: ")
			if strings.HasPrefix(w, "pencerede OOMKilled") {
				k = "OOMKilled"
			}
			if counts[k] == 0 {
				order = append(order, k)
			}
			counts[k]++
		}
		if i, ok := idx[s.Namespace+"\x00"+s.Pod]; ok {
			c := &pods.cands[i]
			for _, w := range why {
				dup := false
				for _, have := range c.Reasons {
					if have == w || (strings.Contains(w, "OOMKilled") && strings.Contains(have, "OOMKilled")) ||
						(strings.HasPrefix(w, "faz ") && have == w) {
						dup = true
					}
				}
				if !dup {
					c.Reasons = append(c.Reasons, w)
				}
			}
			if healthverdict.LevelRank(lvl) < healthverdict.LevelRank(c.Level) {
				c.Level = lvl
			}
			if imp > c.Impact {
				c.Impact = imp
			}
			continue
		}
		o.cands = append(o.cands, sweepCand{Kind: sweepKindK8s, Name: s.Pod, Cluster: s.Cluster, ClusterID: s.ClusterID, Namespace: s.Namespace,
			Level: lvl, Reasons: why, Impact: round4(imp), Hop: sweepHopPods})
	}
	o.section.Note = strings.Join(pods.k8sNotes, "; ")
	n := len(pods.k8s)
	switch {
	case n == 0:
		o.section.State, o.section.Summary = hopEmpty, "veri yok: taranacak pod yok"
		return o
	case pods.k8sWaitAsk && !pods.k8sWaitOK:
		o.section.State, o.failed = hopPartial, true
		if len(order) == 0 {
			o.section.Summary = fmt.Sprintf("%d pod: bekleme sebebi okunamadı — faz / pencerede OOM temiz", n)
			return o
		}
	default:
		o.section.State = hopOK
	}
	o.anyDataFound = true
	if len(order) == 0 {
		o.section.Summary = fmt.Sprintf("%d pod: bekleme sebebi, pencerede OOM ya da Pending/Failed yok", n)
		return o
	}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		parts = append(parts, fmt.Sprintf("%d× %s", counts[k], k))
	}
	o.section.Summary = fmt.Sprintf("%d pod tarandı: %s", n, strings.Join(parts, ", "))
	return o
}
