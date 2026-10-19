package api

// v0.10.1369 — regresyon (operatör, prod: Exceptions › Oracle grubu; "Sample
// traces kısmında trace'ler Coremetry'de yok diyor, sample etmediği için. Ama
// sanki Tempo üzerinden search yapsa güzel olmaz mı."). Kök neden: örnek
// trace'in "Coremetry'de" kararı yalnız ClickHouse'a bakıyordu; /trace sayfası
// Tempo'dan açabildiği trace'ler linksiz kalıyordu. Çiviler: CH + Tempo
// birleşimi (CH'de var → dokunulmaz; CH'de yok + Tempo'da var → açılır+tempo;
// Tempo'da da yok → yok; Tempo hata/bütçe → bilinmiyor; Tempo yok → bugünkü),
// işçi tavanı + toplam bütçe, önbellek anahtarı tüm id'leri kapsar.

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func bp(b bool) *bool { return &b }

func TestTempoProbeCandidates(t *testing.T) {
	samples := []chstore.ExceptionSample{
		{TraceID: "a", TraceInCoremetry: bp(false)},
		{TraceID: "a", TraceInCoremetry: bp(false)}, // tekrar
		{TraceID: "b", TraceInCoremetry: bp(true)},  // CH'de var
		{TraceID: "c"}, // bakılmadı (span grubu)
		{TraceID: " ", TraceInCoremetry: bp(false)}, // boş id
		{TraceID: "d", TraceInCoremetry: bp(false)},
		{TraceID: "e", TraceInCoremetry: bp(false)},
	}
	for _, tc := range []struct {
		name string
		max  int
		want string
	}{
		{"hepsi", 10, "a,d,e"},
		{"tavan", 2, "a,d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(tempoProbeCandidates(samples, tc.max), ","); got != tc.want {
				t.Fatalf("want %q got %q", tc.want, got)
			}
		})
	}
}

func TestMergeTempoSampleTraces(t *testing.T) {
	type want struct {
		in  *bool
		src string
	}
	for _, tc := range []struct {
		name   string
		sample chstore.ExceptionSample
		asked  []string
		found  map[string]bool
		want   want
	}{
		{"CH'de var → dokunulmaz", chstore.ExceptionSample{TraceID: "a", TraceInCoremetry: bp(true)}, []string{"a"}, map[string]bool{"a": false}, want{bp(true), ""}},
		{"bakılmadı (nil) → dokunulmaz", chstore.ExceptionSample{TraceID: "a"}, []string{"a"}, map[string]bool{"a": true}, want{nil, ""}},
		{"CH yok + Tempo var → açılır+tempo", chstore.ExceptionSample{TraceID: "a", TraceInCoremetry: bp(false)}, []string{"a"}, map[string]bool{"a": true}, want{bp(true), "tempo"}},
		{"CH yok + Tempo yok → yok", chstore.ExceptionSample{TraceID: "a", TraceInCoremetry: bp(false)}, []string{"a"}, map[string]bool{"a": false}, want{bp(false), ""}},
		{"Tempo hata/bütçe → bilinmiyor", chstore.ExceptionSample{TraceID: "a", TraceInCoremetry: bp(false)}, []string{"a"}, map[string]bool{}, want{nil, ""}},
		{"sorulmayan (tavan dışı) → yok kalır", chstore.ExceptionSample{TraceID: "z", TraceInCoremetry: bp(false)}, []string{"a"}, map[string]bool{}, want{bp(false), ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := []chstore.ExceptionSample{tc.sample}
			mergeTempoSampleTraces(s, tc.asked, tc.found)
			got := s[0]
			switch {
			case tc.want.in == nil && got.TraceInCoremetry != nil:
				t.Fatalf("bilinmiyor (nil) beklenirdi, %v", *got.TraceInCoremetry)
			case tc.want.in != nil && (got.TraceInCoremetry == nil || *got.TraceInCoremetry != *tc.want.in):
				t.Fatalf("want %v got %v", *tc.want.in, got.TraceInCoremetry)
			}
			if got.TraceSource != tc.want.src {
				t.Fatalf("source want %q got %q", tc.want.src, got.TraceSource)
			}
		})
	}
}

func TestProbeTempoTraces_WorkerCapAndOutcomes(t *testing.T) {
	var cur, peak int32
	exists := func(ctx context.Context, id string) (bool, error) {
		n := atomic.AddInt32(&cur, 1)
		defer atomic.AddInt32(&cur, -1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		switch id[0] {
		case 'f':
			return true, nil
		case 'm':
			return false, nil
		}
		return false, errors.New("tempo 500")
	}
	ids := []string{"f1", "m1", "e1", "f2", "m2", "e2", "f3", "m3", "e3", "f4"}
	got := probeTempoTraces(context.Background(), ids, exists, excSampleTempoWorkers, time.Second)
	if peak > excSampleTempoWorkers {
		t.Fatalf("eşzamanlı istek %d > tavan %d", peak, excSampleTempoWorkers)
	}
	for _, id := range ids {
		v, known := got[id]
		switch id[0] {
		case 'f':
			if !known || !v {
				t.Errorf("%s: var beklenirdi", id)
			}
		case 'm':
			if !known || v {
				t.Errorf("%s: yok beklenirdi", id)
			}
		case 'e':
			if known {
				t.Errorf("%s: hata → bilinmiyor (haritada yok) beklenirdi", id)
			}
		}
	}
}

func TestProbeTempoTraces_TotalBudget(t *testing.T) {
	// Bağlamı DİNLEMEYEN yavaş Tempo bile toplam bütçeyi aşamaz.
	release := make(chan struct{})
	defer close(release)
	exists := func(ctx context.Context, id string) (bool, error) {
		if id == "fast" {
			return true, nil
		}
		<-release
		return true, nil
	}
	ids := []string{"fast", "s1", "s2", "s3", "s4", "s5"}
	start := time.Now()
	got := probeTempoTraces(context.Background(), ids, exists, 4, 80*time.Millisecond)
	if el := time.Since(start); el > 500*time.Millisecond {
		t.Fatalf("bütçe aşıldı: %v", el)
	}
	if len(got) > 1 || (len(got) == 1 && !got["fast"]) {
		t.Fatalf("yalnız 'fast' kesin olabilir, got %v", got)
	}
	for _, id := range ids[1:] {
		if _, ok := got[id]; ok {
			t.Fatalf("%s bütçe dışı → bilinmiyor olmalı", id)
		}
	}
}

func TestExcSampleTempoKey_CoversAllInputs(t *testing.T) {
	base := excSampleTempoKey("http://tempo:3200", "org1", []string{"a", "b", "c"})
	if !strings.HasPrefix(base, "exc-sample-tempo:v1:") {
		t.Fatalf("önek: %s", base)
	}
	if same := excSampleTempoKey("http://tempo:3200", "org1", []string{"c", "a", "b"}); same != base {
		t.Fatalf("sıra anahtarı değiştirmemeli: %s vs %s", same, base)
	}
	for name, other := range map[string]string{
		"id değişti":    excSampleTempoKey("http://tempo:3200", "org1", []string{"a", "b", "d"}),
		"aynı sayı":     excSampleTempoKey("http://tempo:3200", "org1", []string{"x", "y", "z"}),
		"id eksik":      excSampleTempoKey("http://tempo:3200", "org1", []string{"a", "b"}),
		"uç adresi":     excSampleTempoKey("http://tempo2:3200", "org1", []string{"a", "b", "c"}),
		"kiracı":        excSampleTempoKey("http://tempo:3200", "org2", []string{"a", "b", "c"}),
		"alan sınırı":   excSampleTempoKey("http://tempo:3200", "org1", []string{"ab", "c"}),
		"kiracı sınırı": excSampleTempoKey("http://tempo:3200o", "rg1", []string{"a", "b", "c"}),
	} {
		if other == base {
			t.Errorf("%s anahtarı değiştirmiyor", name)
		}
	}
}

func TestCachedTempoProbe_CachesOnlyDefiniteResults(t *testing.T) {
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(8), stats: newCacheStats()}
	var mu sync.Mutex
	calls := 0
	fail := true
	exists := func(ctx context.Context, id string) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if fail {
			return false, errors.New("tempo down")
		}
		return id == "a", nil
	}
	ids := []string{"a", "b"}
	key := excSampleTempoKey("u", "", ids)
	// Hata → bilinmiyor; önbelleğe YAZILMAZ (bir sonraki çağrı yine sorar).
	if got := s.cachedTempoProbe(context.Background(), key, ids, exists); len(got) != 0 {
		t.Fatalf("hata → boş harita beklenirdi, %v", got)
	}
	fail = false
	got := s.cachedTempoProbe(context.Background(), key, ids, exists)
	if !got["a"] || got["b"] {
		t.Fatalf("a var, b yok beklenirdi: %v", got)
	}
	before := calls
	if again := s.cachedTempoProbe(context.Background(), key, ids, exists); !again["a"] || len(again) != 2 {
		t.Fatalf("önbellekten aynı sonuç beklenirdi: %v", again)
	}
	if calls != before {
		t.Fatalf("kesin sonuç önbellekten gelmeliydi (çağrı %d → %d)", before, calls)
	}
}

func TestWithTempoSampleTraces_NotConfiguredKeepsToday(t *testing.T) {
	s := &Server{} // Tempo yok
	res := chstore.ExceptionSamples{Samples: []chstore.ExceptionSample{{TraceID: "a", TraceInCoremetry: bp(false)}}}
	out := s.withTempoSampleTraces(context.Background(), res)
	if out.Samples[0].TraceInCoremetry == nil || *out.Samples[0].TraceInCoremetry || out.Samples[0].TraceSource != "" {
		t.Fatalf("Tempo yokken bugünkü davranış (false) beklenirdi: %+v", out.Samples[0])
	}
}

// Bağlantı çivileri: örnek ucu ve CoSRE okuyucusu aynı Tempo kararından geçer.
func TestExceptionSampleTempo_Wiring(t *testing.T) {
	for file, want := range map[string]string{
		"api.go":                   "writeJSON(w, s.withTempoSampleTraces(r.Context(), res))",
		"chat_exception_groups.go": "tempoMarkedSamples{exGroupsReader: s.store, mark: s.withTempoSampleTraces}",
	} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), want) {
			t.Errorf("%s %q taşımalı", file, want)
		}
	}
}

// CoSRE: Tempo'da bulunan id sohbetin örnek trace listesine girer; Tempo'da da
// olmayan girmez.
func TestTempoMarkedSamples_CoSREUsesTempoFoundIDs(t *testing.T) {
	fake := &fakeExGroups{samples: map[string][]chstore.ExceptionSample{"ora:x": {
		{TraceID: "missing", TraceInCoremetry: bp(false)},
		{TraceID: "intempo", TraceInCoremetry: bp(false)},
	}}}
	mark := func(_ context.Context, res chstore.ExceptionSamples) chstore.ExceptionSamples {
		ids := tempoProbeCandidates(res.Samples, excSampleTempoMax)
		mergeTempoSampleTraces(res.Samples, ids, map[string]bool{"missing": false, "intempo": true})
		return res
	}
	rd := tempoMarkedSamples{exGroupsReader: fake, mark: mark}
	res, err := rd.GetExceptionGroupSamples(context.Background(), "ora:x", 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, ids := exGroupSampleFacts(res.Samples); strings.Join(ids, ",") != "intempo" {
		t.Fatalf("yalnız Tempo'da bulunan id beklenirdi, %v", ids)
	}
}
