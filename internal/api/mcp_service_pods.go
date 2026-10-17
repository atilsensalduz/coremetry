package api

// mcp_service_pods.go — v0.10.1276 (CoSRE adım 7): service_health_sweep'in pod
// bölümünün Thanos adaptörü (Deps.ServicePods).
//
// Pod kümesi guidedThanosPods ile AYNI (thanosPodRegexFor: deployment | servis
// | ortam eksiz ad, Infrastructure sekmesinin seçicisi); cluster başına:
//   - thanos.PodMetrics — anlık CPU/bellek, limit %, faz, son sonlanma (kube-state);
//   - increase(kube_pod_container_status_restarts_total[pencere]) — penceredeki
//     restart ARTIŞI (KSM sayacı ömür boyu; ham sayaç puanlanmaz), PodFamilyQuery
//     şablonu, InstantSamples (paylaşımlı querier'da cluster matcher enjekte).
// Cluster referansları mcptools'tan gelir (servisin eşlenen cluster'ları, yoksa
// tüm etkinler); burada ClusterByRef ile çözülür, etkin olmayan/bilinmeyen düşer.
// Paralellik ve cluster başına 6 sn tavan guidedThanosPods'unkiyle aynı.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// mcpServicePodsMaxClusters — adaptörün kendi tavanı (mcptools ≤6 yollar).
const mcpServicePodsMaxClusters = 6

// mcpServicePodsOrNil — Thanos yoksa nil (bölüm dürüst skipped).
func (s *Server) mcpServicePodsOrNil() mcptools.ServicePodsFunc {
	if s.thanos == nil {
		return nil
	}
	return s.mcpServicePods
}

func (s *Server) mcpServicePods(ctx context.Context, service string, refs []string, from, to time.Time) (mcptools.ServicePodsResult, error) {
	var out mcptools.ServicePodsResult
	if s.thanos == nil || !s.thanos.HasEnabledClusters() {
		return out, nil
	}
	out.Thanos = true
	if service == "" {
		return out, fmt.Errorf("service gerekli")
	}
	var clusters []thanos.ClusterConfig
	seen := map[string]bool{}
	if len(refs) == 0 {
		clusters = s.thanosEnabledClusters()
	}
	for _, ref := range refs {
		if c, ok := s.thanos.ClusterByRef(ref); ok && c.Enabled && !seen[c.EffectiveID()] {
			seen[c.EffectiveID()] = true
			clusters = append(clusters, c)
		}
	}
	if len(clusters) > mcpServicePodsMaxClusters {
		clusters = clusters[:mcpServicePodsMaxClusters]
	}
	re := s.thanosPodRegexFor(ctx, service)
	out.PodRegex = re
	window := to.Sub(from)
	if window < time.Minute {
		window = time.Minute
	}
	var offset time.Duration
	if now := time.Now(); now.Sub(to) >= time.Minute {
		offset = now.Sub(to).Truncate(time.Second)
	}
	out.Clusters = make([]mcptools.ServicePodsCluster, len(clusters))
	var wg sync.WaitGroup
	sem := make(chan struct{}, thanosPodsParallel)
	for i, c := range clusters {
		wg.Add(1)
		go func(i int, c thanos.ClusterConfig) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out.Clusters[i] = s.mcpServicePodsCluster(ctx, c, re, window, offset)
		}(i, c)
	}
	wg.Wait()
	return out, nil
}

// mcpServicePodsCluster — tek cluster'ın okuması (yalnız kendi dönüşüne yazar).
func (s *Server) mcpServicePodsCluster(ctx context.Context, c thanos.ClusterConfig, re string, window, offset time.Duration) mcptools.ServicePodsCluster {
	cl := mcptools.ServicePodsCluster{ID: c.EffectiveID(), Name: c.Name}
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, thanosPodsPerCluster)
	defer cancel()
	rows, truncated, err := s.thanos.PodMetrics(cctx, c, re)
	restartExpr, qerr := thanos.PodFamilyQuery(thanos.PodFamilyRestarts, c.NamespaceFilter, re, window, offset, true, 500)
	cl.Query = fmt.Sprintf("PodMetrics pod=~%q (cAdvisor CPU/bellek + kube-state limit/faz/son sonlanma; anlık)", re)
	if qerr == nil {
		cl.Query += "\n" + restartExpr
	}
	if err != nil {
		cl.Err = err.Error()
		cl.DurationMs = time.Since(start).Milliseconds()
		return cl
	}
	for i := range rows {
		if rows[i].Cluster == "" {
			rows[i].Cluster = c.Name
		}
	}
	cl.Rows, cl.Truncated = rows, truncated
	if qerr == nil && len(rows) > 0 {
		if samples, rerr := s.thanos.InstantSamples(cctx, c, restartExpr); rerr == nil {
			cl.Restarts = make(map[string]float64, len(samples))
			for _, smp := range samples {
				cl.Restarts[smp.Labels["namespace"]+"\x00"+smp.Labels["pod"]] = smp.Value
			}
			cl.RestartsOK = true
		}
	}
	cl.DurationMs = time.Since(start).Milliseconds()
	return cl
}
