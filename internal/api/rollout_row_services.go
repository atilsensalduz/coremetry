package api

// rollout_row_services.go — v0.10.1420 (operatör, /rollouts: "Deployment
// Workload basınca service'e girsin, şu an bulamıyor.")
//
// Workload hücresinin BİRİNCİL tıkı o iş yükünün koştuğu servisin sayfasını
// açar. Eşleme span gerçeğinden: workload_revision_activity_1m (cluster span
// değeri, k8s_namespace, workload) → service_name — çekmecenin ve problem
// rozetinin zaten kullandığı MV (chstore.RolloutWorkloadServicesBatch; PK
// öneki demet IN, LIMIT + max_execution_time). service_metadata.Deployment
// tersi DEĞİL: elle bakım ister ve çoktan-çoğa.
//
// N+1 YOK: listenin TÜM satırları için TEK sorgu (demetler tekilleşir), liste
// cevabıyla birlikte serveCached'te (anahtar zaten tüm girdileri taşır).
// Hata = alan yok (rozet emsali: liste düşmez, hücre yedek bağlantıya düşer).
//
// Seçim deterministik (pickRolloutService): servis adı workload adına eşitse
// (büyük/küçük harf duyarsız) o; değilse alfabetik ilk. Aday kümesi zaten
// AYNI cluster + namespace'ten (MV anahtarı) — "aynı namespace+cluster önce"
// kuralı yapıdan gelir. Birden çok aday varsa hepsi `services`te gider (FE
// title'ında listeler). Eşleşme yoksa alan boş → FE entity sayfasına düşer
// (entities.go wl: yedeği — "Kayıt bulunamadı" çıkmaz).

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// rolloutRowServicesMax — satır başına JSON'a giden en çok servis adı.
const rolloutRowServicesMax = 20

// rolloutServicesLookback — servis aramasının en geniş geriye bakışı.
const rolloutServicesLookback = 30 * 24 * time.Hour

type rolloutServiceLookup func(ctx context.Context, keys []chstore.RolloutServiceKey, since time.Time) (map[chstore.RolloutServiceKey][]string, error)

// attachRolloutServices — listRollouts yükleyicisi çağırır.
func (s *Server) attachRolloutServices(ctx context.Context, rows []chstore.RolloutRow) {
	if s.store == nil || len(rows) == 0 {
		return
	}
	clustersOf := func(id string) []string {
		if c, ok := s.resolveCluster(id); ok {
			return c.SpanClusterKeys()
		}
		return nil
	}
	if err := attachRolloutServicesWith(ctx, rows, clustersOf, s.store.RolloutWorkloadServicesBatch); err != nil {
		log.Printf("[rollouts] workload services: %v", err)
	}
}

// attachRolloutServicesWith — test dikişi: cluster çözümü + TEK toplu arama.
func attachRolloutServicesWith(ctx context.Context, rows []chstore.RolloutRow, clustersOf func(string) []string, lookup rolloutServiceLookup) error {
	clustersByRow := make([][]string, len(rows))
	var keys []chstore.RolloutServiceKey
	seen := map[chstore.RolloutServiceKey]bool{}
	since := time.Time{}
	for i, row := range rows {
		clustersByRow[i] = clustersOf(row.ClusterID)
		for _, cv := range clustersByRow[i] {
			k := chstore.RolloutServiceKey{Cluster: cv, Namespace: row.Namespace, Workload: row.Workload}
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
		if !row.StartedAt.IsZero() && (since.IsZero() || row.StartedAt.Before(since)) {
			since = row.StartedAt
		}
	}
	if len(keys) == 0 {
		return nil
	}
	// Alt sınır: en eski başlangıç − 1 sa (rozet ile aynı pay); damgasız
	// satırda ya da çok eski pencerede 30 güne kelepçe (MV taraması sınırlı).
	floor := time.Now().Add(-rolloutServicesLookback)
	if since.IsZero() || since.Before(floor) {
		since = floor
	}
	m, err := lookup(ctx, keys, since.Add(-time.Hour))
	if err != nil {
		return err
	}
	for i := range rows {
		svcs := rolloutRowServices(clustersByRow[i], rows[i], m)
		rows[i].Services = svcs
		rows[i].Service = pickRolloutService(rows[i].Workload, svcs)
	}
	return nil
}

// rolloutRowServices — SAF: satırın span-cluster değerleri üzerinden servis
// birleşimi (tekil, alfabetik, rolloutRowServicesMax'ta kesik); yoksa nil.
func rolloutRowServices(spanClusters []string, row chstore.RolloutRow, m map[chstore.RolloutServiceKey][]string) []string {
	set := map[string]bool{}
	for _, cv := range spanClusters {
		for _, svc := range m[chstore.RolloutServiceKey{Cluster: cv, Namespace: row.Namespace, Workload: row.Workload}] {
			if svc != "" {
				set[svc] = true
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for svc := range set {
		out = append(out, svc)
	}
	sort.Strings(out)
	if len(out) > rolloutRowServicesMax {
		out = out[:rolloutRowServicesMax]
	}
	return out
}

// pickRolloutService — SAF: birincil tıkın servisi. Adı workload'a eşit
// olan (harf duyarsız) kazanır; yoksa alfabetik ilk (svcs sıralı gelir);
// aday yoksa "".
func pickRolloutService(workload string, svcs []string) string {
	for _, svc := range svcs {
		if strings.EqualFold(svc, workload) {
			return svc
		}
	}
	if len(svcs) == 0 {
		return ""
	}
	sorted := append([]string(nil), svcs...)
	sort.Strings(sorted)
	return sorted[0]
}
