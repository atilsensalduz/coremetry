package api

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.707 — etkilenen varlıklar: sıra, tekrarsız, özne servis dışarıda,
// tavan 25; pencere onset−1h..çözüm|şimdi.
//
// v0.10.1495 regresyon tablosu — operatör (prod, `runtime:jvm-gc:<svc>:<pod>`):
// "AFFECTED ENTITIES (37)" hata görmeyen onlarca servisi etkilenen diye
// sayıyordu. Etkilenen = bağı doğrulanmış (çağrı kenarı / pod / cluster
// sahipliği) VE etkisi gözlenmiş; hata görmeyen çağıran `Linked`'e ayrılır,
// pod kapsamlı problemde cluster yalnız tekse. Servis adları sentetik.
func TestBuildAffectedEntities(t *testing.T) {
	keys := func(es []affectedEntity) []string {
		out := []string{}
		for _, e := range es {
			out = append(out, e.Kind+"|"+e.ID+"|"+e.Relation)
		}
		return out
	}
	callers := &chstore.BlastRadius{Callers: []chstore.BlastRadiusCaller{
		{Service: "shop-cart", Calls: 10, Errors: 2, ErrorRate: 20},
		{Service: "shop-web", Calls: 500, HasOpenProblem: true},
		{Service: "shop-payment", Calls: 3, Errors: 1}, // özne — girmez
		{Service: "shop-bff", Calls: 900},              // bağlı, etkisiz
		{Service: "shop-crm", Calls: 40},               // bağlı, etkisiz
	}}
	cases := []struct {
		name               string
		in                 affectedInput
		entities, linked   []string
		topoKnown          bool
		podScoped          bool
		clustersUnverified bool
	}{
		{
			name: "servis kapsamlı: etkilenen çağıranlar + pod kanıtı + cluster; etkisizler ayrı",
			in: affectedInput{Subject: "shop-payment", Callers: callers, CallersKnown: true,
				Pods:     []chstore.PodHit{{Pod: "p-2", Count: 1}, {Pod: "p-1", Count: 9}, {Pod: "", Count: 3}},
				Clusters: []string{"prod-eu", "prod-eu", "", "prod-us"}},
			entities:  []string{"service|shop-web|caller", "service|shop-cart|caller", "pod|p-1|evidence-pod", "pod|p-2|evidence-pod", "cluster|prod-eu|cluster", "cluster|prod-us|cluster"},
			linked:    []string{"service|shop-bff|caller", "service|shop-crm|caller"},
			topoKnown: true,
		},
		{
			name: "prod: JVM GC tek pod — hata görmeyen 25 çağıran etkilenen DEĞİL, özne pod'u başta",
			in: affectedInput{Subject: "core-api", Pod: "core-api-7d9f-x", CallersKnown: true,
				Callers:  &chstore.BlastRadius{Callers: []chstore.BlastRadiusCaller{{Service: "shop-bff", Calls: 900}, {Service: "infra-agent", Calls: 80}}},
				Clusters: []string{"prod-eu"}},
			entities:  []string{"pod|core-api-7d9f-x|subject-pod", "cluster|prod-eu|cluster"},
			linked:    []string{"service|shop-bff|caller", "service|infra-agent|caller"},
			topoKnown: true, podScoped: true,
		},
		{
			name:     "pod kapsamlı, servis çok cluster'da — pod'un cluster'ı doğrulanamaz, listelenmez",
			in:       affectedInput{Subject: "core-api", Pod: "core-api-0", CallersKnown: true, Clusters: []string{"prod-eu", "prod-us"}},
			entities: []string{"pod|core-api-0|subject-pod"}, linked: []string{},
			topoKnown: true, podScoped: true, clustersUnverified: true,
		},
		{
			name:     "topoloji okunamadı — yalnız doğrulanabilen (pod/cluster), bilgi bayrağı",
			in:       affectedInput{Subject: "core-api", Pod: "core-api-0", Clusters: []string{"prod-eu"}},
			entities: []string{"pod|core-api-0|subject-pod", "cluster|prod-eu|cluster"}, linked: []string{},
			podScoped: true,
		},
		{
			name: "boş girdi → boş (nil değil) listeler", in: affectedInput{Subject: "x", CallersKnown: true},
			entities: []string{}, linked: []string{}, topoKnown: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildAffectedEntities(c.in)
			if got.Entities == nil || got.Linked == nil {
				t.Fatal("listeler nil olmamalı (JSON null)")
			}
			if g := keys(got.Entities); strings.Join(g, ",") != strings.Join(c.entities, ",") {
				t.Errorf("entities\n got %v\nwant %v", g, c.entities)
			}
			if g := keys(got.Linked); strings.Join(g, ",") != strings.Join(c.linked, ",") {
				t.Errorf("linked\n got %v\nwant %v", g, c.linked)
			}
			if got.TopologyKnown != c.topoKnown || got.PodScoped != c.podScoped || got.ClustersUnverified != c.clustersUnverified {
				t.Errorf("bayraklar topo=%v pod=%v cu=%v", got.TopologyKnown, got.PodScoped, got.ClustersUnverified)
			}
		})
	}
	// alanlar taşınır
	got := buildAffectedEntities(affectedInput{Subject: "shop-payment", Callers: callers, CallersKnown: true})
	if !got.Entities[0].HasOpenProblem || got.Entities[1].Errors != 2 {
		t.Fatalf("alanlar taşınmalı: %+v", got.Entities)
	}
	// tavan: ≤25 çağıran (etkilenen + bağlı toplamı)
	many := &chstore.BlastRadius{}
	for i := 0; i < 40; i++ {
		many.Callers = append(many.Callers, chstore.BlastRadiusCaller{Service: "s" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Calls: uint64(i), Errors: uint64(i % 2)})
	}
	if s := buildAffectedEntities(affectedInput{Subject: "x", Callers: many, CallersKnown: true}); len(s.Entities)+len(s.Linked) != affectedCallersMax {
		t.Fatalf("tavan %d, geldi %d", affectedCallersMax, len(s.Entities)+len(s.Linked))
	}
}

func TestProblemPod(t *testing.T) {
	cases := []struct {
		p    chstore.Problem
		want string
	}{
		{chstore.Problem{Pod: "core-api-0", ID: "runtime:jvm-gc:core-api:other"}, "core-api-0"},
		{chstore.Problem{ID: "runtime:jvm-gc:core-api:core-api-1"}, "core-api-1"},
		{chstore.Problem{ID: "runtime:jvm-gc:core-api"}, ""},
		{chstore.Problem{ID: "abc123"}, ""},
	}
	for _, c := range cases {
		if got := problemPod(c.p); got != c.want {
			t.Errorf("problemPod(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestAffectedWindow(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	started := now.Add(-30 * time.Minute)
	from, to := affectedWindow(chstore.Problem{StartedAt: started.UnixNano()}, now)
	if !to.Equal(now) || !from.Equal(started.Add(-time.Hour)) {
		t.Fatalf("açık problem: %v..%v", from, to)
	}
	res := now.Add(-10 * time.Minute).UnixNano()
	_, to = affectedWindow(chstore.Problem{StartedAt: started.UnixNano(), ResolvedAt: &res}, now)
	if !to.Equal(time.Unix(0, res)) {
		t.Fatal("çözülmüş problem çözüm anında biter")
	}
	old := now.Add(-3 * 24 * time.Hour)
	from, to = affectedWindow(chstore.Problem{StartedAt: old.UnixNano()}, now)
	if to.Sub(from) != 24*time.Hour {
		t.Fatal("24 saat tavanı")
	}
	from, to = affectedWindow(chstore.Problem{}, now)
	if to.Sub(from) != time.Hour {
		t.Fatal("onset yoksa son 1 saat")
	}
}
