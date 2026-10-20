package api

// service_neighbors.go — v0.10.1408 — GET /api/services/{name}/neighbors
// (Service → Overview → Bağlam: "Upstream / downstream for <svc>").
//
// Operatör (prod ekran görüntüsü): "Downstream düzgün çalışmıyor" — panel
// "12 upstream · 0 downstream · sampled from 50 traces" diyordu, oysa aynı
// Overview'un Top DB statements tablosu servisin oracle'ı ~938 kez
// çağırdığını gösteriyordu.
//
// KÖK NEDEN (eski chstore/neighbors.go ServiceNeighbors): downstream YALNIZ
// "çocuk span'ın service_name'i != bu servis" kenarlarını sayıyordu. Bir
// DB / dış HTTP / messaging çağrısı servisin KENDİ CLIENT/PRODUCER span'ı —
// enstrümante bir callee'si olmadığı için çocuğu yok, service_name aynı;
// kenar hiç oluşmuyordu. Üstüne örneklem ham `spans` üstünde en büyük 50
// trace'ti (MV-first ihlali).
//
// DÜZELTME: Topology sekmesiyle AYNI okuma ve AYNI düğüm kimliği —
// ReadServiceTopologyAggForFocus(hops=1) (topology_edges_5m: servis-çifti +
// infra pass'leri db:/queue:/ext: kenarlarını zaten yazıyor) →
// filterHiddenTopologyEdges → buildServiceGraphWith("neighborhood", 1,
// knownServiceSet) (ext-merge dahil). Örneklem yok; başlık bunu söylüyor.
//
// v0.10.1410 — okuma serviceNeighborsRollup'a çıkarıldı; CoSRE'nin LLM
// bağlamları (servis bağlamı, problem/anomali açıklaması) da aynı yardımcıyı
// kullanıyor ve chstore.ServiceNeighbors silindi.
//
// Rota kaydı init()'te (api.go büyümez; /api-route).

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("service-neighbors", (*Server).registerServiceNeighborsRoutes) }

func (s *Server) registerServiceNeighborsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{name}/neighbors", s.getServiceNeighbors)
}

const (
	// serviceNeighborsMaxSince — pencere tavanı. MV okuması (5 dk kovalar,
	// odak-filtreli) olduğu için eski 24 saatlik ham-span tavanı gerekmiyor;
	// 30 gün sayfanın en geniş preset'i.
	serviceNeighborsMaxSince = 30 * 24 * time.Hour
	// serviceNeighborsEdgeLimit — odağa dokunan kenar okuma tavanı (LIMIT).
	serviceNeighborsEdgeLimit = 5000
	// serviceNeighborsSideCap — yön başına dönen komşu sayısı; toplamlar
	// ayrıca döner, UI "+N more → topology" der.
	serviceNeighborsSideCap = 50
	// serviceNeighborsSource — yanıtın veri kaynağı (UI başlığı).
	serviceNeighborsSource = "topology_edges_5m"
)

// serviceNeighbor — bir komşu düğüm + odakla arasındaki kenarın RED'i.
// Kimlik alanları GraphNode'unkiyle aynı (Topology sekmesiyle aynı etiket,
// aynı nodeDetailHref hedefi); sayılar düğümün değil KENARIN.
type serviceNeighbor struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	System    string   `json:"system,omitempty"`
	Env       string   `json:"env,omitempty"`
	Cluster   string   `json:"cluster,omitempty"`
	Calls     uint64   `json:"calls"`
	Errors    uint64   `json:"errors"`
	ErrorRate float64  `json:"errorRate"`
	AvgMs     float64  `json:"avgMs"`
	P99Ms     float64  `json:"p99Ms"`
	Protocols []string `json:"protocols,omitempty"`
}

type serviceNeighborsResponse struct {
	Service         string            `json:"service"`
	From            int64             `json:"from"`
	To              int64             `json:"to"`
	Source          string            `json:"source"`
	Upstream        []serviceNeighbor `json:"upstream"`
	Downstream      []serviceNeighbor `json:"downstream"`
	UpstreamTotal   int               `json:"upstreamTotal"`
	DownstreamTotal int               `json:"downstreamTotal"`
}

func (s *Server) getServiceNeighbors(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "service name required", http.StatusBadRequest)
		return
	}
	since := parseDuration(r.URL.Query().Get("since"), time.Hour)
	if since <= 0 {
		since = time.Hour
	}
	if since > serviceNeighborsMaxSince {
		since = serviceNeighborsMaxSince
	}
	hidPats := s.topologyHiddenPatterns(r.Context())
	// v0.10.1408 — anahtar "v2": yanıt şekli değişti (örneklemli eski
	// gövde 24 saat SWR ile önbellekte; aynı anahtarı okusaydı yeni UI eski
	// şekli alırdı). Tüm girdiler: servis, pencere, gizli-desen özeti.
	key := fmt.Sprintf("service-neighbors:v2:svc=%s:since=%s:hid=%s", name, since, hiddenDigest(hidPats))
	s.serveCached(w, r, key, 5*time.Minute, func(ctx context.Context) (any, error) {
		to := time.Now()
		from := to.Add(-since)
		up, down, err := s.serviceNeighborsRollupWith(ctx, name, from, to, hidPats)
		if err != nil {
			return nil, err
		}
		resp := serviceNeighborsResponse{
			Service: name, From: from.UnixNano(), To: to.UnixNano(),
			Source:        serviceNeighborsSource,
			UpstreamTotal: len(up), DownstreamTotal: len(down),
		}
		resp.Upstream = capNeighbors(up, serviceNeighborsSideCap)
		resp.Downstream = capNeighbors(down, serviceNeighborsSideCap)
		return resp, nil
	})
}

// serviceNeighborsRollup — v0.10.1410: servisin 1-hop komşularının TEK
// okuması. Bağlam paneli (yukarıdaki uç), CoSRE servis bağlamı
// (buildServiceContext → guided sohbet / servis raporu) ve problem/anomali
// açıklamasının kanıtı (serviceCorrelationContext) hepsi buradan okur —
// Topology sekmesiyle aynı MV, aynı gizli-desen süzgeci, aynı ext-merge,
// aynı düğüm etiketi (oracle@<instance>, kafka:<topic>, dış host). Eski
// chstore.ServiceNeighbors (ham spans, en büyük 50 trace, DB/dış çağrıya
// kör) kaldırıldı. Sınır: hops=1 → tek MV sorgusu, LIMIT
// serviceNeighborsEdgeLimit + max_execution_time 10 sn (chstore).
func (s *Server) serviceNeighborsRollup(ctx context.Context, svc string, from, to time.Time) (up, down []serviceNeighbor, err error) {
	return s.serviceNeighborsRollupWith(ctx, svc, from, to, s.topologyHiddenPatterns(ctx))
}

// serviceNeighborsRollupWith — gizli-desen listesi çağırandan (uç onu
// önbellek anahtarına da koyuyor; ikinci kez okumasın). Kenar okuması
// s.explainReads() üstünden: üretimde *chstore.Store, testte sahte yüzey.
func (s *Server) serviceNeighborsRollupWith(ctx context.Context, svc string, from, to time.Time, hidPats []string) (up, down []serviceNeighbor, err error) {
	edges, err := s.explainReads().ReadServiceTopologyAggForFocus(ctx, from, to, svc, 1, serviceNeighborsEdgeLimit)
	if err != nil {
		return nil, nil, err
	}
	edges = filterHiddenTopologyEdges(edges, hidPats)
	up, down = serviceNeighborsFromEdges(edges, svc, s.knownServiceSet(ctx))
	return up, down, nil
}

// neighbourEvidenceLabel — SAF: LLM kanıtı için bir komşunun tek satırlık
// etiketi. Ad Topology düğüm adı; servis olmayan düğümde tür köşeli
// parantezde ([database] / [queue] / [external]) — model DB'yi servis
// sanmasın. Sayılar kenarın (Topology kenar ipucuyla aynı): çağrı, hata
// varsa hata oranı. tr → Türkçe birimler (servis bağlamı), değilse İngilizce
// (problem/anomali açıklaması bloğu İngilizce).
func neighbourEvidenceLabel(n serviceNeighbor, tr bool) string {
	label := n.Name
	if n.Kind != "" && n.Kind != "service" {
		label += " [" + n.Kind + "]"
	}
	switch {
	case tr && n.Errors > 0:
		return fmt.Sprintf("%s (%d çağrı, %%%.1f hata)", label, n.Calls, n.ErrorRate)
	case tr:
		return fmt.Sprintf("%s (%d çağrı)", label, n.Calls)
	case n.Errors > 0:
		return fmt.Sprintf("%s (%d calls, %.1f%% err)", label, n.Calls, n.ErrorRate)
	default:
		return fmt.Sprintf("%s (%d calls)", label, n.Calls)
	}
}

func capNeighbors(ns []serviceNeighbor, n int) []serviceNeighbor {
	if len(ns) > n {
		return ns[:n]
	}
	return ns
}

// serviceNeighborsFromEdges — SAF (tablo testli): MV kenarlarından odağın
// çağıranları (upstream) ve çağırdıkları (downstream). Kenarlar önce
// Topology sekmesinin dönüşümünden geçer (ext-merge + düğüm kimliği), sonra
// komşu düğüm başına katlanır (aynı çift farklı protokolle iki satır
// olabilir). Kendine-kenar atlanır. Sıra: calls DESC, sonra ad.
func serviceNeighborsFromEdges(edges []chstore.ServiceTopologyEdge, focus string, known *chstore.ServiceNameSet) (up, down []serviceNeighbor) {
	g := buildServiceGraphWith(edges, focus, "neighborhood", 1, nil, 1, known)
	nodes := make(map[string]GraphNode, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	type acc struct {
		n      serviceNeighbor
		durMs  float64 // Σ(avg × calls) — ağırlıklı ortalama için
		protos map[string]bool
	}
	upA, downA := map[string]*acc{}, map[string]*acc{}
	add := func(m map[string]*acc, peer string, e GraphEdge) {
		a := m[peer]
		if a == nil {
			gn := nodes[peer]
			a = &acc{n: serviceNeighbor{ID: peer, Name: gn.Name, Kind: gn.Kind,
				System: gn.System, Env: gn.Env, Cluster: gn.Cluster}, protos: map[string]bool{}}
			if a.n.Name == "" {
				a.n.Name = peer
			}
			if a.n.Kind == "" {
				a.n.Kind = "service"
			}
			m[peer] = a
		}
		a.n.Calls += e.Calls
		a.n.Errors += e.Errors
		a.durMs += e.AvgMs * float64(e.Calls)
		if e.P99Ms > a.n.P99Ms {
			a.n.P99Ms = e.P99Ms
		}
		if e.Protocol != "" {
			a.protos[e.Protocol] = true
		}
	}
	for _, e := range g.Edges {
		switch {
		case e.Source == e.Target:
			continue
		case e.Source == focus:
			add(downA, e.Target, e)
		case e.Target == focus:
			add(upA, e.Source, e)
		}
	}
	flat := func(m map[string]*acc) []serviceNeighbor {
		out := make([]serviceNeighbor, 0, len(m))
		for _, a := range m {
			if a.n.Calls > 0 {
				a.n.AvgMs = a.durMs / float64(a.n.Calls)
				a.n.ErrorRate = float64(a.n.Errors) / float64(a.n.Calls) * 100
			}
			for p := range a.protos {
				a.n.Protocols = append(a.n.Protocols, p)
			}
			sort.Strings(a.n.Protocols)
			out = append(out, a.n)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Calls != out[j].Calls {
				return out[i].Calls > out[j].Calls
			}
			return out[i].ID < out[j].ID
		})
		return out
	}
	return flat(upA), flat(downA)
}
