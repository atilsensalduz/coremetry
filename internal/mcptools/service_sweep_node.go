package mcptools

// service_sweep_node.go — v0.10.1288 (AI SRE Faz 2): TEMBEL node / gürültülü
// komşu kontrolü (follow-up B'nin "node" satırı; 1b'de ertelenmişti).
//
// Kim çağırır: YALNIZ internal/sre orkestratörü ve yalnız doygunluk
// hipotezi isterse (pod düzeyinde kanıt doygunluğu ne doğruluyor ne
// çürütüyorsa, ya da operatör "doygunluk" hipotezini doğrula dediyse).
// service_health_sweep'in bölümü DEĞİL — sohbet taraması bu okumayı yapmaz
// (cluster başına +3 Thanos sorgusu; sweep bütçesine eklenmez).
//
// Okuma (Deps.ServicePods, ServicePodsOpts{Nodes: true}): pod metrikleri
// okunmaz; kube_pod_info → servis pod'larının node'u (≤3), node CPU %
// allocatable, o node'lardaki en işlek 5 pod. Cluster kümesi pod bölümüyle
// AYNI (sweepPodClusterRefs).
//
// classifyNode: node CPU ≥ %90 → uyarı; servis DIŞI bir pod node'un ≥ %40'ı →
// uyarı "gürültülü komşu". Node etiketi yok → bölüm skipped (okunamadı ≠
// temiz). Kubernetes Events ingest edilmiyor; yalnız metrik.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	sweepHopNode  = "node"
	sweepKindNode = "node"

	// NodeCPUWarnPct — node CPU kullanımı (allocatable'ın %'si) uyarı eşiği.
	NodeCPUWarnPct = 90.0
	// NodeNeighbourWarnPct — servis dışı tek pod'un node CPU payı uyarı eşiği.
	NodeNeighbourWarnPct = 40.0
	nodeFindingsMax      = 3
)

// NodeCheck — tembel node kontrolünün sonucu (DeployCheck emsali).
type NodeCheck struct {
	Section   SweepSection   `json:"section"`
	Findings  []SweepFinding `json:"findings"`
	Nodes     []NodePressure `json:"nodes"`
	Evidences []ChatEvidence `json:"evidences,omitempty"`
}

// classifyNode — SAF: bir node'un baskısı → seviye + gerekçeler + etki.
// CPU okunamadıysa (nil) node puanlanmaz ama komşu payı yine bakılır.
func classifyNode(n NodePressure) (level string, reasons []string, impact float64) {
	level = levelOK
	if n.CPUPct != nil && *n.CPUPct >= NodeCPUWarnPct {
		level = levelWarn
		reasons = append(reasons, fmt.Sprintf("node CPU %%%.0f (allocatable'ın)", *n.CPUPct))
		impact = *n.CPUPct / 100
	}
	for _, p := range n.TopPods {
		if p.Service || p.Pct < NodeNeighbourWarnPct {
			continue
		}
		level = levelWarn
		name := p.Pod
		if p.Namespace != "" {
			name = p.Namespace + "/" + p.Pod
		}
		reasons = append(reasons, fmt.Sprintf("gürültülü komşu: %s node CPU'sunun %%%.0f'ini kullanıyor", name, p.Pct))
		if v := p.Pct / 100; v > impact {
			impact = v
		}
	}
	return level, reasons, math.Round(impact*1e4) / 1e4
}

// ReadNodeCheck — servis pod'larının node'larını okur ve sınıflar. Hata
// yalnız çağıran iptal ettiğinde; okuma hatası bölüm durumuna yazılır.
func ReadNodeCheck(ctx context.Context, d Deps, service string, from, to time.Time) (NodeCheck, error) {
	return readNodeCheck(ctx, sweepReadersFrom(d), service, from, to)
}

func readNodeCheck(ctx context.Context, r sweepReaders, service string, from, to time.Time) (NodeCheck, error) {
	out := NodeCheck{Section: SweepSection{Name: sweepHopNode}, Findings: []SweepFinding{}, Nodes: []NodePressure{}}
	var all []ClusterRef
	if r.clusters != nil {
		all = r.clusters()
	}
	if r.servicePods == nil || len(all) == 0 {
		out.Section.State, out.Section.Summary = hopSkipped, "Thanos Remote Cluster yapılandırılmamış — node baskısı okunamaz"
		return out, nil
	}
	elapsed := stopwatch()
	refs, notes := sweepPodClusterRefs(ctx, r, SweepRequest{Service: service, From: from, To: to}, all)
	res, err := r.servicePods(ctx, service, refs, from, to, ServicePodsOpts{Nodes: true})
	if errors.Is(ctx.Err(), context.Canceled) {
		return NodeCheck{}, ctx.Err()
	}
	if err != nil {
		out.Section.State, out.Section.Summary = sweepFailState(err), cosreErrMsg("node'lar okunamadı", err)
		out.Section.DurationMs = elapsed()
		return out, nil
	}
	if !res.Thanos {
		out.Section.State, out.Section.Summary = hopSkipped, "Thanos'ta etkin cluster yok"
		return out, nil
	}
	mapped, failed := 0, 0
	var cands []sweepCand
	for _, cl := range res.Clusters {
		if cl.Err != "" || !cl.NodesOK {
			failed++
			notes = append(notes, "cluster "+cl.Name+": node okuması yapılamadı "+truncateRunes(firstNonEmpty(cl.Err, cl.NodeErr), 120))
			continue
		}
		if cl.NodeErr != "" {
			notes = append(notes, "cluster "+cl.Name+": "+truncateRunes(cl.NodeErr, 120))
		}
		mapped += len(cl.PodNodes)
		for _, n := range cl.Nodes {
			if n.Cluster == "" {
				n.Cluster = cl.Name
			}
			out.Nodes = append(out.Nodes, n)
			lvl, why, imp := classifyNode(n)
			cands = append(cands, sweepCand{Kind: sweepKindNode, Name: n.Node, Cluster: n.Cluster, ClusterID: cl.ID,
				Level: lvl, Reasons: why, Impact: imp, Hop: sweepHopNode})
		}
		if len(out.Evidences) == 0 && cl.Query != "" {
			rows := len(cl.Nodes)
			out.Evidences = append(out.Evidences, ChatEvidence{Tool: ServiceHealthSweepName, Source: evSourceThanos, QueryKind: evKindPromQL,
				Query: cl.Query, Window: evWin(from, to), Rows: &rows, DurationMs: cl.DurationMs, Hop: sweepHopNode, Cluster: cl.Name})
		}
	}
	out.Section.DurationMs = elapsed()
	out.Section.Note = strings.Join(notes, "; ")
	switch {
	case failed > 0 && failed == len(res.Clusters):
		out.Section.State, out.Section.Summary = hopError, "node okuması hiçbir cluster'da yapılamadı (okunamadı ≠ temiz)"
		return out, nil
	case mapped == 0:
		out.Section.State, out.Section.Summary = hopSkipped, "pod'ların node etiketi yok (kube_pod_info) — node kontrolü atlandı"
		return out, nil
	}
	findings, _ := scoreFindings(cands, nodeFindingsMax)
	for i := range findings {
		if len(out.Evidences) > 0 {
			findings[i].EvidenceRefs = []string{"evidences[0]"}
		}
	}
	out.Findings = findings
	sort.SliceStable(out.Nodes, func(i, j int) bool { return nodeCPU(out.Nodes[i]) > nodeCPU(out.Nodes[j]) })
	out.Section.State = hopOK
	if failed > 0 {
		out.Section.State = hopPartial
	}
	parts := make([]string, 0, len(out.Nodes))
	for _, n := range out.Nodes {
		if n.CPUPct != nil {
			parts = append(parts, fmt.Sprintf("%s CPU %%%.0f", n.Node, *n.CPUPct))
		} else {
			parts = append(parts, n.Node+" CPU okunamadı")
		}
	}
	out.Section.Summary = fmt.Sprintf("%d node: %s", len(out.Nodes), strings.Join(parts, ", "))
	if len(findings) > 0 {
		out.Section.Summary += "; " + findings[0].Name + " — " + strings.Join(findings[0].Reasons, "; ")
	} else {
		out.Section.Summary += "; node baskısı / gürültülü komşu yok"
	}
	return out, nil
}

func nodeCPU(n NodePressure) float64 {
	if n.CPUPct == nil {
		return -1
	}
	return *n.CPUPct
}
