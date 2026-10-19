package api

// copilot_topology_focus.go — v0.10.1392 (CoSRE topoloji kartı). Operatör (prod
// ekran görüntüleri): "Bunu galiba eski service map'ten getiriyor. Onun yerine
// 1 hop topology getirebilir."
//
// Kök neden (v0.10.1388 copilot_service_topology.go): sohbet cevabı servis
// komşuluğunu Topology sekmesiyle AYNI okumadan (ReadServiceTopologyAggForFocus
// + buildServiceGraphWith "neighborhood") alıyordu ama sonra kendi 15 düğüm /
// 30 kenar bütçesini (topoChatBudget) uyguluyordu ve sayıları ÜÇ ayrı kümeden
// basıyordu: başlıktaki "N komşu, M kenar" KESİLMİŞ graftan, "+X düğüm, +Y
// kenar" kesilmemiş graftan, "ilk 15 düğüm / 30 kenar" ise sabitlerden (gerçekte
// çizilen değil). 230 doğrudan çağıranı olan bir hub "14 komşu · +216 düğüm"
// diye okunuyor, 2. adım sanılıyordu. Ayrıca 1 hop'ta komşular arası kenarlar
// (neighborhoodKeepSet "links among them") "2. ADIM" başlığıyla basılıyordu.
//
// Düzeltme: Topology sekmesinin (FocusedNeighborhood.tsx) yerleşim + tavan
// kuralının Go ikizi — assignFocusColumns (yönlü, işaretli sütunlar) +
// capPerColumn (sütun başına MAX_PER_COL=12; aşarsa 11 + "+N more"). Çizilen
// ve sayılan küme AYNI: yalnız akış (flow) kenarları — 1 hop'ta tam olarak odağa
// değen kenarlar; yan bağlantılar (peer/skip/back) sekmede de varsayılan gizli,
// burada çizilmez, ayrı sayılır. Kesim yalnız bir sütun tavanı aşarsa olur.
// SAF, tablo testli (copilot_topology_focus_test.go).

import (
	"fmt"
	"sort"
	"strings"
)

// topoChatMaxPerCol — v0.10.1392: FocusedNeighborhood.tsx MAX_PER_COL'un ikizi
// (TestTopoChatMaxPerColPinnedToTab iki değeri eşit tutar).
const topoChatMaxPerCol = 12

// topoMorePrefix — lib/topoFocusLayout.ts MORE_PREFIX ("+N more" sentetik düğümü).
const topoMorePrefix = "more:"

func topoMoreID(col int) string { return fmt.Sprintf("%s%d", topoMorePrefix, col) }

func topoIsMore(id string) bool { return strings.HasPrefix(id, topoMorePrefix) }

// topoFocusCols — SAF: assignFocusColumns'un (FocusedNeighborhood.tsx) Go ikizi.
// 0 = odak, negatif = çağıranlar (IN kenarlarıyla yürünür), pozitif =
// bağımlılıklar (OUT). İki yön AYRI yürünür; iki yoldan ulaşılan düğüm küçük
// |sütun|'u alır, eşitlikte önce gelen (aşağı akış) kalır.
func topoFocusCols(edges []GraphEdge, focus string, hops int) map[string]int {
	if hops < 1 {
		hops = 1
	}
	out := map[string][]string{}
	in := map[string][]string{}
	for _, e := range edges {
		out[e.Source] = append(out[e.Source], e.Target)
		in[e.Target] = append(in[e.Target], e.Source)
	}
	col := map[string]int{focus: 0}
	setCloser := func(id string, v int) {
		c, ok := col[id]
		if !ok || topoAbs(v) < topoAbs(c) {
			col[id] = v
		}
	}
	for dir := 0; dir < 2; dir++ {
		adj, sign := out, 1
		if dir == 1 {
			adj, sign = in, -1
		}
		frontier := []string{focus}
		seen := map[string]bool{focus: true}
		for h := 1; h <= hops && len(frontier) > 0; h++ {
			var next []string
			for _, id := range frontier {
				for _, nb := range adj[id] {
					if !seen[nb] {
						seen[nb] = true
						setCloser(nb, sign*h)
						next = append(next, nb)
					}
				}
			}
			frontier = next
		}
	}
	return col
}

func topoAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// topoIsFlow — SAF: lib/topoFocusLayout.ts pairClass(...) === 'flow'. Çift
// yönlü çift soldan sağa yönünden sınıflanır.
func topoIsFlow(cu, cv int, hasReverse bool) bool {
	if hasReverse && cv < cu {
		cu, cv = cv, cu
	}
	return cv == cu+1
}

// topoNeighbourhood — sohbet komşuluğu: çizilen graf + aynı kümeden sayılar.
type topoNeighbourhood struct {
	// Graph — çizilen küme: gerçek düğümler + "+N more" (more:<sütun>) düğümleri,
	// gerçek akış kenarları + gizlenenlerin toplu kenarları.
	Graph ServiceGraphResponse `json:"graph"`
	// Toplamlar (kesimden ÖNCE, aynı akış kümesi): komşu = odak hariç yerleşen
	// gerçek düğüm; Callers sütun<0, Callees sütun>0; Edges akış kenarı.
	Neighbours int `json:"neighbours"`
	Callers    int `json:"callers"`
	Callees    int `json:"callees"`
	Edges      int `json:"edges"`
	// SideLinks — yerleşen düğümler arası akış OLMAYAN kenarlar (peer/skip/back):
	// sekmede "Yan bağlantılar" altında, burada çizilmez ve Edges'e girmez.
	SideLinks int `json:"sideLinks"`
	// Truncated — tavanın gizledikleri (Nodes/Edges: gerçek düğüm/kenar; 0 = kesim yok).
	Truncated topoTruncation `json:"truncated"`
}

// topoCapPerColumn — SAF: capPerColumn'un (FocusedNeighborhood.tsx) Go ikizi.
// maxPerCol'u aşan sütun maxPerCol−1 düğüm tutar (önce hata >%1, sonra çağrı,
// sonra id); gerisi `more:<sütun>` düğümüne iner, gizli uçlu kenarlar toplu
// kenara katlanır (çağrı/hata/rate toplanır, p99 en büyük, avg ağırlıklı).
// hidden: sütun → gizlenen düğüm; hiddenEdges: gizli uca değen gerçek kenar.
func topoCapPerColumn(nodes []GraphNode, edges []GraphEdge, col map[string]int, focus string, maxPerCol int) (outNodes []GraphNode, outEdges []GraphEdge, hidden map[int]int, hiddenEdges int) {
	hidden = map[int]int{}
	byCol := map[int][]GraphNode{}
	for _, n := range nodes {
		c, ok := col[n.ID]
		if !ok || n.ID == focus || c == 0 {
			continue
		}
		byCol[c] = append(byCol[c], n)
	}
	cols := make([]int, 0, len(byCol))
	for c := range byCol {
		cols = append(cols, c)
	}
	sort.Ints(cols)
	repOf := map[string]string{}
	var more []GraphNode
	for _, c := range cols {
		list := byCol[c]
		if len(list) <= maxPerCol {
			continue
		}
		ranked := append([]GraphNode(nil), list...)
		sort.SliceStable(ranked, func(i, j int) bool {
			ei, ej := ranked[i].ErrorRate > 1, ranked[j].ErrorRate > 1
			if ei != ej {
				return ei
			}
			if ranked[i].Calls != ranked[j].Calls {
				return ranked[i].Calls > ranked[j].Calls
			}
			return ranked[i].ID < ranked[j].ID
		})
		drop := ranked[maxPerCol-1:]
		id := topoMoreID(c)
		m := GraphNode{ID: id, Name: fmt.Sprintf("+%d more", len(drop)), Kind: "service"}
		for _, n := range drop {
			repOf[n.ID] = id
			m.Calls += n.Calls
			m.Errors += n.Errors
			m.Rate += n.Rate
		}
		if m.Calls > 0 {
			m.ErrorRate = float64(m.Errors) / float64(m.Calls) * 100
		}
		hidden[c] = len(drop)
		more = append(more, m)
	}
	placed := map[string]bool{}
	for _, n := range nodes {
		if _, ok := col[n.ID]; ok && repOf[n.ID] == "" {
			outNodes = append(outNodes, n)
			placed[n.ID] = true
		}
	}
	for _, m := range more {
		outNodes = append(outNodes, m)
		placed[m.ID] = true
	}
	type agg struct {
		e    GraphEdge
		wAvg float64
		mix  bool
	}
	aggs := map[string]*agg{}
	for _, e := range edges {
		s, t := e.Source, e.Target
		if r := repOf[s]; r != "" {
			s = r
		}
		if r := repOf[t]; r != "" {
			t = r
		}
		if !placed[s] || !placed[t] {
			continue
		}
		if s == e.Source && t == e.Target {
			outEdges = append(outEdges, e)
			continue
		}
		hiddenEdges++
		if s == t {
			continue
		}
		k := s + "|" + t
		a := aggs[k]
		if a == nil {
			aggs[k] = &agg{e: GraphEdge{Source: s, Target: t, Calls: e.Calls, Errors: e.Errors, Rate: e.Rate, P99Ms: e.P99Ms, Protocol: e.Protocol}, wAvg: e.AvgMs * float64(e.Calls)}
			continue
		}
		a.e.Calls += e.Calls
		a.e.Errors += e.Errors
		a.e.Rate += e.Rate
		a.wAvg += e.AvgMs * float64(e.Calls)
		if e.P99Ms > a.e.P99Ms {
			a.e.P99Ms = e.P99Ms
		}
		if a.e.Protocol != e.Protocol {
			a.mix = true
		}
	}
	keys := make([]string, 0, len(aggs))
	for k := range aggs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := aggs[k]
		if a.mix {
			a.e.Protocol = ""
		}
		if a.e.Calls > 0 {
			a.e.ErrorRate = float64(a.e.Errors) / float64(a.e.Calls) * 100
			a.e.AvgMs = a.wAvg / float64(a.e.Calls)
		}
		outEdges = append(outEdges, a.e)
	}
	return outNodes, outEdges, hidden, hiddenEdges
}

// topoChatNeighbourhood — SAF: buildServiceGraphWith("neighborhood") çıktısını
// Topology sekmesinin görünümüne indirir. eonly: hatasız kenarlar düşer (önce).
// Sütunlar topoFocusCols; yerleşmeyen düğüm düşer (sekmeyle aynı); yalnız akış
// kenarları çizilir ve sayılır; sütun tavanı aşılırsa "+N more".
func topoChatNeighbourhood(g ServiceGraphResponse, focus string, hops int, eonly bool, maxPerCol int) topoNeighbourhood {
	edges := make([]GraphEdge, 0, len(g.Edges))
	for _, e := range g.Edges {
		if eonly && e.Errors == 0 {
			continue
		}
		if e.Source == e.Target {
			continue
		}
		edges = append(edges, e)
	}
	col := topoFocusCols(edges, focus, hops)
	dir := map[string]bool{}
	for _, e := range edges {
		dir[e.Source+"|"+e.Target] = true
	}
	var res topoNeighbourhood
	flow := make([]GraphEdge, 0, len(edges))
	for _, e := range edges {
		cu, oku := col[e.Source]
		cv, okv := col[e.Target]
		if !oku || !okv {
			continue
		}
		if topoIsFlow(cu, cv, dir[e.Target+"|"+e.Source]) {
			flow = append(flow, e)
		} else {
			res.SideLinks++
		}
	}
	// Yerleşen gerçek düğümler (odak kenarsızsa graf boş kalır).
	var nodes []GraphNode
	for _, n := range g.Nodes {
		c, ok := col[n.ID]
		if !ok {
			continue
		}
		if n.ID == focus && len(flow) == 0 {
			continue
		}
		nodes = append(nodes, n)
		switch {
		case c < 0:
			res.Callers++
		case c > 0:
			res.Callees++
		}
	}
	res.Neighbours = res.Callers + res.Callees
	res.Edges = len(flow)
	shownNodes, shownEdges, hidden, hiddenEdges := topoCapPerColumn(nodes, flow, col, focus, maxPerCol)
	for c, n := range hidden {
		res.Truncated.Nodes += n
		if c < 0 {
			res.Truncated.Callers += n
		} else {
			res.Truncated.Callees += n
		}
	}
	res.Truncated.Edges = hiddenEdges
	out := g
	out.Nodes, out.Edges = shownNodes, shownEdges
	if out.Nodes == nil {
		out.Nodes = []GraphNode{}
	}
	if out.Edges == nil {
		out.Edges = []GraphEdge{}
	}
	out.TotalNodes = len(nodes)
	out.ShownNodes = len(nodes) - res.Truncated.Nodes
	res.Graph = out
	return res
}

// topoViaInformative — SAF (v0.10.1392): "via <host>" bilgi taşıyor mu?
// Operatör: her çağıran satırı odağın KENDİ k8s DNS adıyla ("via
// <svc>.<ns>.svc.cluster.local") bitiyordu — gürültü. Host kenarın HEDEF
// servisinin kendi adresiyse (çıplak ad, ya da küme-içi DNS adı ve ilk etiketi
// hedefin adı / "<ad>-<belirteç>", ör. "<ad>-grpc") false. GSLB / route host'u
// (küme dışı ad, ör. "<ad>-prod-kon.<kurum>") hangi kanaldan gelindiğini
// söyler → true.
func topoViaInformative(host, target string) bool {
	t := strings.ToLower(strings.TrimSpace(target))
	h := topoViaNormHost(host)
	if t == "" || h == "" {
		return h != ""
	}
	if h == t {
		return false
	}
	k8s := false
	for _, suf := range []string{".svc.cluster.local", ".cluster.local", ".svc"} {
		if strings.HasSuffix(h, suf) {
			k8s = true
			break
		}
	}
	if !k8s {
		return true
	}
	label, _, _ := strings.Cut(h, ".")
	return !(label == t || strings.HasPrefix(label, t+"-"))
}

// topoViaNormHost — küçük harf; şema / gRPC çözücü öneki, yol, sayısal port ve
// sondaki nokta atılmış host.
func topoViaNormHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(host, "ext:")))
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	} else if rest, ok := strings.CutPrefix(h, "dns:"); ok {
		h = rest
	}
	h = strings.TrimLeft(h, "/")
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	if i := strings.LastIndexByte(h, ':'); i > 0 && strings.Count(h, ":") == 1 && isAllDigitsAPI(h[i+1:]) {
		h = h[:i]
	}
	return strings.TrimSuffix(h, ".")
}

func isAllDigitsAPI(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// topoInformativeVia — SAF: kenarın bilgi taşıyan via host'ları (hedefin kendi
// adresi düşer).
func topoInformativeVia(e GraphEdge) []string {
	var out []string
	for _, h := range e.ViaHosts {
		if topoViaInformative(h, e.Target) {
			out = append(out, h)
		}
	}
	return out
}
