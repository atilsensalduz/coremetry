package api

// copilot_topology_focus_test.go — v0.10.1392. Operatör (prod ekran
// görüntüleri, v0.10.1388 sohbet topolojisi): "Bunu galiba eski service
// map'ten getiriyor. Onun yerine 1 hop topology getirebilir."
//
// Pinlenen sözleşme: sohbet komşuluğu Topology sekmesinin görünümüdür
// (topoChatNeighbourhood = assignFocusColumns + capPerColumn'un Go ikizi);
// çizilen küme ile sayılan küme AYNI (1 hop = yalnız odağa değen kenarlar,
// okumada 2. adım kenarı olsa bile ne çizilir ne sayılır); kesim yalnız bir
// sütun MAX_PER_COL'u aşarsa ve gerçek gizlenen sayılarla; "via <host>"
// hedefin kendi k8s adresiyse basılmaz.

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func topoEdge(p, c string, calls uint64) chstore.ServiceTopologyEdge {
	kind := "service"
	if strings.HasPrefix(c, "db:") {
		kind = "db"
	}
	return chstore.ServiceTopologyEdge{ParentService: p, ChildNode: c, NodeKind: kind, Protocol: "http", Calls: calls}
}

// TestTopoChatNeighbourhoodOneHopSet — okuma 2. adım kenarları ve komşular
// arası bir kenar taşısa da 1 hop görünümü yalnız odak + doğrudan çağıran /
// bağımlılık ve odağa değen kenarlardır; sayılar bu kümeden.
func TestTopoChatNeighbourhoodOneHopSet(t *testing.T) {
	edges := []chstore.ServiceTopologyEdge{
		topoEdge("checkout-service", "payment-service", 1200),
		topoEdge("cart-service", "payment-service", 800),
		topoEdge("payment-service", "ledger-service", 900),
		topoEdge("payment-service", "db:postgresql", 5000),
		// 2. adım: çağıranın çağıranı, bağımlılığın bağımlılığı, çağıranın başka bağımlılığı.
		topoEdge("web-frontend", "checkout-service", 3000),
		topoEdge("ledger-service", "audit-service", 400),
		topoEdge("checkout-service", "inventory-service", 700),
		// komşular arası (yan bağlantı): çağıran → bağımlılık.
		topoEdge("checkout-service", "ledger-service", 50),
	}
	// İki kaynak: neighborhood kurucusu (sekmeyle aynı) VE süzgeçsiz global
	// kurucu (okumadaki 2. adım kenarlarının hepsi grafta) — sonuç aynı olmalı.
	for _, scope := range []string{"neighborhood", "global"} {
		g := buildServiceGraphWith(edges, "payment-service", scope, 1, nil, 60, nil)
		nb := topoChatNeighbourhood(g, "payment-service", 1, false, topoChatMaxPerCol)
		var ids []string
		for _, n := range nb.Graph.Nodes {
			ids = append(ids, n.ID)
		}
		got := strings.Join(sortedCopy(ids), ",")
		if want := "cart-service,checkout-service,db:postgresql,ledger-service,payment-service"; got != want {
			t.Errorf("%s: düğümler = %s, want %s", scope, got, want)
		}
		for _, e := range nb.Graph.Edges {
			if e.Source != "payment-service" && e.Target != "payment-service" {
				t.Errorf("%s: 1 hop'ta odağa değmeyen kenar çizildi: %s → %s", scope, e.Source, e.Target)
			}
		}
		if nb.Neighbours != 4 || nb.Callers != 2 || nb.Callees != 2 || nb.Edges != 4 || len(nb.Graph.Edges) != nb.Edges {
			t.Errorf("%s: sayılar çizilen kümeyle tutarsız: %+v (çizilen kenar %d)", scope, nb, len(nb.Graph.Edges))
		}
		if nb.SideLinks != 1 {
			t.Errorf("%s: checkout → ledger yan bağlantı sayılmalı: %d", scope, nb.SideLinks)
		}
		if nb.Truncated != (topoTruncation{}) {
			t.Errorf("%s: tavan aşılmadı, kesim olmamalı: %+v", scope, nb.Truncated)
		}
		text := renderTopologyEvidenceTR("payment-service", nb, 1, 3600, "", false, len(g.Edges))
		if !strings.Contains(text, "1 hop · 4 komşu: 2 çağıran, 2 bağımlılık · 4 kenar)") {
			t.Errorf("%s: başlık sayıları:\n%s", scope, text)
		}
		for _, bad := range []string{"2. ADIM", "web-frontend", "audit-service", "inventory-service", "Tavan", "gösterilmedi"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s: 1 hop metninde %q olmamalı:\n%s", scope, bad, text)
			}
		}
	}
}

// TestTopoChatNeighbourhoodCapOnlyWhenExceeding — sütun tavanı (sekmenin
// MAX_PER_COL'u) yalnız aşılınca keser; kesimde hatalı komşu çağrısı az olsa
// da kalır, gizlenenler tek "+N more" düğümüne ve toplu kenara iner, kesim
// sayıları gerçek gizlenenlerdir.
func TestTopoChatNeighbourhoodCapOnlyWhenExceeding(t *testing.T) {
	build := func(callers int, erroredLow bool) ServiceGraphResponse {
		var edges []chstore.ServiceTopologyEdge
		for i := 0; i < callers; i++ {
			e := topoEdge(fmt.Sprintf("caller-%02d", i), "payment-service", uint64(1000+i))
			if erroredLow && i == 0 {
				e.Calls, e.Errors, e.ErrorRate = 10, 5, 50
			}
			edges = append(edges, e)
		}
		edges = append(edges, topoEdge("payment-service", "ledger-service", 900))
		return buildServiceGraphWith(edges, "payment-service", "neighborhood", 1, nil, 60, nil)
	}
	// Tam tavan (12 çağıran): kesim yok, "+N more" yok.
	at := topoChatNeighbourhood(build(topoChatMaxPerCol, false), "payment-service", 1, false, topoChatMaxPerCol)
	if at.Truncated != (topoTruncation{}) || at.Callers != 12 || len(at.Graph.Nodes) != 14 {
		t.Errorf("tavanda kesim olmamalı: %+v düğüm=%d", at.Truncated, len(at.Graph.Nodes))
	}
	for _, n := range at.Graph.Nodes {
		if topoIsMore(n.ID) {
			t.Errorf("tavanda +N more olmamalı: %s", n.ID)
		}
	}
	if text := renderTopologyEvidenceTR("payment-service", at, 1, 3600, "", false, 13); strings.Contains(text, "Tavan") {
		t.Errorf("kesim yokken tavan notu basıldı:\n%s", text)
	}

	// 14 çağıran (biri az çağrılı ama hatalı): 11 kalır + more:-1 ("+3 more").
	over := topoChatNeighbourhood(build(14, true), "payment-service", 1, false, topoChatMaxPerCol)
	if over.Callers != 14 || over.Neighbours != 15 || over.Edges != 15 {
		t.Errorf("toplamlar kesimden ÖNCEKİ kümeden: %+v", over)
	}
	if over.Truncated != (topoTruncation{Nodes: 3, Edges: 3, Callers: 3}) {
		t.Errorf("kesim sayıları: %+v", over.Truncated)
	}
	if over.Graph.TotalNodes != 16 || over.Graph.ShownNodes != 13 {
		t.Errorf("Total/Shown: %d/%d", over.Graph.TotalNodes, over.Graph.ShownNodes)
	}
	var more *GraphNode
	real := 0
	hasErrored := false
	for i, n := range over.Graph.Nodes {
		switch {
		case topoIsMore(n.ID):
			more = &over.Graph.Nodes[i]
		case n.ID == "caller-00":
			hasErrored = true
			real++
		default:
			real++
		}
	}
	if more == nil || more.ID != "more:-1" || more.Name != "+3 more" {
		t.Fatalf("+N more düğümü: %+v", more)
	}
	if !hasErrored {
		t.Error("hatalı (>%1) çağıran az çağrılı olsa da kalmalı")
	}
	if real != 13 { // odak + 11 çağıran + ledger
		t.Errorf("gerçek düğüm = %d, want 13", real)
	}
	// Gizlenen 3 çağıran en az çağrılı hatasızlar: caller-01..03 → 1001+1002+1003.
	var agg *GraphEdge
	for i, e := range over.Graph.Edges {
		if e.Source == "more:-1" {
			agg = &over.Graph.Edges[i]
		}
	}
	if agg == nil || agg.Target != "payment-service" || agg.Calls != 1001+1002+1003 {
		t.Errorf("toplu kenar: %+v", agg)
	}
	text := renderTopologyEvidenceTR("payment-service", over, 1, 3600, "", false, 15)
	for _, want := range []string{
		"1 hop · 15 komşu: 14 çağıran, 1 bağımlılık · 15 kenar)",
		"sütun başına en çok 12 komşu; aşan sütunda ilk 11",
		"+3 çağıran (+3 kenar) \"+N more\" altında",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("metinde yok: %q\n%s", want, text)
		}
	}
	if strings.Contains(text, "+3 more") || strings.Contains(text, "+0 bağımlılık") {
		t.Errorf("sentetik düğüm satır olarak / sıfır kesim basılmamalı:\n%s", text)
	}
}

// TestTopoFocusColsMirrorsTab — assignFocusColumns ikizi: çağıran −, bağımlılık
// +, çift yönlü komşu aşağı akışta (+1), çağıranın başka bağımlılığı (kardeş)
// yerleşmez; 2 hop'ta ±2.
func TestTopoFocusColsMirrorsTab(t *testing.T) {
	edges := []GraphEdge{
		{Source: "checkout-service", Target: "payment-service"},
		{Source: "payment-service", Target: "ledger-service"},
		{Source: "ledger-service", Target: "payment-service"}, // çift yönlü
		{Source: "checkout-service", Target: "inventory-service"},
		{Source: "web-frontend", Target: "checkout-service"},
		{Source: "ledger-service", Target: "audit-service"},
	}
	c1 := topoFocusCols(edges, "payment-service", 1)
	want1 := map[string]int{"payment-service": 0, "checkout-service": -1, "ledger-service": 1}
	if fmt.Sprint(c1) != fmt.Sprint(want1) {
		t.Errorf("1 hop: %v, want %v", c1, want1)
	}
	c2 := topoFocusCols(edges, "payment-service", 2)
	if c2["web-frontend"] != -2 || c2["audit-service"] != 2 {
		t.Errorf("2 hop: %v", c2)
	}
	if _, ok := c2["inventory-service"]; ok {
		t.Error("kardeş (çağıranın başka bağımlılığı) yerleşmemeli")
	}
	// Çift yönlü çift akış sayılır (soldan sağa); tek yönlü geri kenar değil.
	if !topoIsFlow(1, 0, true) || topoIsFlow(1, 0, false) || topoIsFlow(1, 1, false) || !topoIsFlow(-1, 0, false) {
		t.Error("topoIsFlow pairClass ile ayrıştı")
	}
}

// TestTopoViaInformative — "via" yalnız bilgi taşıyorsa: hedefin kendi k8s
// adı (port / dns:/// önekli, "<ad>-grpc" etiketi dahil) düşer; GSLB/route
// host'u ve başka bir adın k8s adı kalır.
func TestTopoViaInformative(t *testing.T) {
	cases := []struct {
		host, target string
		want         bool
	}{
		{"payment-service.payments.svc.cluster.local", "payment-service", false},
		{"payment-service.payments.svc.cluster.local:8080", "payment-service", false},
		{"dns:///payment-service.payments.svc.cluster.local:50051", "payment-service", false},
		{"payment-service.payments.svc", "payment-service", false},
		{"Payment-Service.payments.svc.cluster.local", "payment-service", false},
		{"payment-service-grpc.payments.svc.cluster.local", "payment-service", false},
		{"payment-service", "payment-service", false},
		{"payment-service-prod-kon.bank.example", "payment-service", true}, // GSLB kanal host'u
		{"gateway.payments.svc.cluster.local", "payment-service", true},    // başka servis adı
		{"payment-servicex.payments.svc.cluster.local", "payment-service", true},
		{"api.example.com", "payment-service", true},
		{"", "payment-service", false},
	}
	for _, c := range cases {
		if got := topoViaInformative(c.host, c.target); got != c.want {
			t.Errorf("topoViaInformative(%q, %q) = %v, want %v", c.host, c.target, got, c.want)
		}
	}
	own := GraphEdge{Source: "checkout-service", Target: "payment-service", Calls: 10, ViaHosts: []string{"payment-service.payments.svc.cluster.local"}}
	if line := topoEdgeLineTR("checkout-service", own); strings.Contains(line, "via") {
		t.Errorf("kendi k8s adı basılmamalı: %s", line)
	}
	mixed := own
	mixed.ViaHosts = []string{"payment-service-prod-kon.bank.example", "payment-service.payments.svc.cluster.local"}
	if line := topoEdgeLineTR("checkout-service", mixed); !strings.HasSuffix(line, " via payment-service-prod-kon.bank.example") {
		t.Errorf("bilgi taşıyan host kalmalı, kendi adı düşmeli: %s", line)
	}
}

// TestTopoChatMaxPerColPinnedToTab — Go tavanı ve sentetik düğüm öneki
// sekmenin sabitleriyle eşit (biri değişirse sohbet kartı sekmeden ayrışır).
func TestTopoChatMaxPerColPinnedToTab(t *testing.T) {
	fn := readSourceFile(t, "../../frontend/src/components/topology/FocusedNeighborhood.tsx")
	m := regexp.MustCompile(`export const MAX_PER_COL = (\d+);`).FindStringSubmatch(fn)
	if m == nil || m[1] != fmt.Sprint(topoChatMaxPerCol) {
		t.Errorf("FocusedNeighborhood MAX_PER_COL = %v, Go topoChatMaxPerCol = %d", m, topoChatMaxPerCol)
	}
	lay := readSourceFile(t, "../../frontend/src/lib/topoFocusLayout.ts")
	if !strings.Contains(lay, "export const MORE_PREFIX = '"+topoMorePrefix+"';") {
		t.Errorf("topoFocusLayout MORE_PREFIX Go topoMorePrefix (%q) ile ayrıştı", topoMorePrefix)
	}
}

func sortedCopy(xs []string) []string {
	out := append([]string(nil), xs...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
