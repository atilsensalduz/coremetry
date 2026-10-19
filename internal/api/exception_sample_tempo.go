package api

// exception_sample_tempo.go — v0.10.1369 (operatör, prod: Exceptions › Oracle
// grubu `ora:…`; "Sample traces kısmında trace'ler Coremetry'de yok diyor,
// sample etmediği için. Ama sanki Tempo üzerinden search yapsa güzel olmaz mı.").
//
// Kök neden: örneğin `traceInCoremetry` kararı YALNIZ ClickHouse'a bakıyordu
// (chstore/oracle_sample_traces.go, TraceFactsByIDs). Coremetry'nin örneklemeye
// almadığı bir trace Grafana Tempo'da tam duruyor ve /trace?id= sayfası onu
// Tempo'dan açıyor (resolveTraceSpans, v0.9.632) — ama örnek listesi "Coremetry'de
// yok" deyip link basmıyordu. chstore tempo'yu import edemez; karar örnekleri
// sunan api katmanında, bu dosyada verilir.
//
// Kural: Tempo yapılandırılmışsa CH'nin "yok" dediği (TraceInCoremetry=false)
// ayrık id'lerden en çok excSampleTempoMax tanesi Tempo'da aranır —
// excSampleTempoWorkers paralel, TOPLAM excSampleTempoBudget bütçe. Sonuç id
// başına:
//   - Tempo'da var   → TraceInCoremetry=true + TraceSource="tempo" (link + rozet);
//   - Tempo'da yok   → false kalır ("Coremetry'de yok");
//   - hata / bütçe   → nil (bilinmiyor → link; bugünkü sözleşme, trace sayfası
//                       zaten önce Tempo'ya bakar).
// Kesin sonuçlar (var/yok) hash-all-inputs anahtarıyla excSampleTempoTTL
// önbelleklenir (cachedJSON: L1 + Redis, singleflight); bilinmeyen içeren
// kısmi sonuç uncacheable — bir Tempo hıçkırığı 10 dk "bilinmiyor" diye
// donmasın. Örnek çağrısı ASLA düşmez. Tempo yoksa davranış bugünkü.
//
// Hafif uç yok: internal/tempo yalnız LookupTrace'i (GET /api/traces/{id})
// sarar; bütçe ve id tavanı maliyeti sınırlar.

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// excSampleTempoMax — Tempo'da aranacak id tavanı (örnek kartı ≤14 satır;
	// oracleTempoLookupMax ile aynı rakam).
	excSampleTempoMax = 10
	// excSampleTempoWorkers — eşzamanlı Tempo isteği tavanı.
	excSampleTempoWorkers = 4
	// excSampleTempoBudget — TÜM aramaların toplam bütçesi; aşan id bilinmiyor.
	excSampleTempoBudget = 2500 * time.Millisecond
	// excSampleTempoTTL — kesin sonuçların önbellek ömrü.
	excSampleTempoTTL = 10 * time.Minute
)

// tempoTraceExists — tek id için Tempo varlık sorusu (test dikişi).
// (true,nil)=var, (false,nil)=yok, err=bilinmiyor.
type tempoTraceExists func(ctx context.Context, traceID string) (bool, error)

// tempoProbeCandidates — SAF: CH'nin "yok" dediği (TraceInCoremetry=false)
// ayrık, boş olmayan id'ler; sıra korunur (en yeni örnek önce), ≤max.
func tempoProbeCandidates(samples []chstore.ExceptionSample, max int) []string {
	seen := map[string]bool{}
	var out []string
	for _, sm := range samples {
		id := strings.TrimSpace(sm.TraceID)
		if id == "" || seen[id] || sm.TraceInCoremetry == nil || *sm.TraceInCoremetry {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) >= max {
			break
		}
	}
	return out
}

// probeTempoTraces — en çok `workers` paralel, TOPLAM `budget` içinde her id'yi
// sorar. Dönüş yalnız KESİN sonuçları taşır (id → var/yok); haritada olmayan
// id bilinmiyor (hata, bütçe aşımı ya da hiç başlatılamadı). Bütçe dolunca
// beklemeden döner — bağlamı dinlemeyen bir yoklama sayfayı tutamaz.
func probeTempoTraces(ctx context.Context, ids []string, exists tempoTraceExists, workers int, budget time.Duration) map[string]bool {
	out := map[string]bool{}
	if len(ids) == 0 || exists == nil {
		return out
	}
	if workers < 1 {
		workers = 1
	}
	if workers > len(ids) {
		workers = len(ids)
	}
	bctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var mu sync.Mutex
	res := map[string]bool{}
	jobs := make(chan string, len(ids))
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				if bctx.Err() != nil {
					return
				}
				ok, err := exists(bctx, id)
				if err != nil || bctx.Err() != nil {
					continue
				}
				mu.Lock()
				res[id] = ok
				mu.Unlock()
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-bctx.Done():
	}
	mu.Lock()
	for k, v := range res {
		out[k] = v
	}
	mu.Unlock()
	return out
}

// mergeTempoSampleTraces — SAF: Tempo sonuçlarını örneklere uygular. Yalnız
// SORULAN id'ler ve yalnız CH'nin "yok" dediği örnekler değişir; CH'de bulunan
// (true) ya da bakılmamış (nil) örneğe dokunulmaz.
func mergeTempoSampleTraces(samples []chstore.ExceptionSample, asked []string, found map[string]bool) {
	askedSet := make(map[string]bool, len(asked))
	for _, id := range asked {
		askedSet[id] = true
	}
	for i := range samples {
		sm := &samples[i]
		id := strings.TrimSpace(sm.TraceID)
		if id == "" || !askedSet[id] || sm.TraceInCoremetry == nil || *sm.TraceInCoremetry {
			continue
		}
		ok, known := found[id]
		switch {
		case !known:
			sm.TraceInCoremetry = nil // bilinmiyor → link (trace sayfası önce Tempo'ya bakar)
		case ok:
			yes := true
			sm.TraceInCoremetry = &yes
			sm.TraceSource = traceSourceTempo
		}
		// known && !ok → false kalır: Tempo'da da yok.
	}
}

// excSampleTempoKey — SAF: hash-all-inputs anahtarı — sıralı id KÜMESİ + Tempo
// uç adresi + kiracı (X-Scope-OrgID). Alanlar 0 baytla ayrılır ("a"+"bc" ≠
// "ab"+"c"); sayı değil içerik (v0.5.187 sınıfı).
func excSampleTempoKey(baseURL, orgID string, ids []string) string {
	cp := append([]string(nil), ids...)
	sort.Strings(cp)
	h := fnv.New64a()
	for _, p := range append([]string{"base=" + baseURL, "org=" + orgID}, cp...) {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return fmt.Sprintf("exc-sample-tempo:v1:%x", h.Sum64())
}

// tempoSampleProbe — önbellekli yoklama gövdesi: kesin sonuçlar haritası.
type tempoSampleProbe struct {
	Found map[string]bool `json:"found"`
}

// withTempoSampleTraces — örnek zarfını Tempo sonucuyla zenginleştirir. Tempo
// yoksa / aranacak id yoksa zarf olduğu gibi döner; hiçbir hata çağrıyı düşürmez.
func (s *Server) withTempoSampleTraces(ctx context.Context, res chstore.ExceptionSamples) chstore.ExceptionSamples {
	if s == nil || s.tempo == nil || !s.tempo.Configured() {
		return res
	}
	ids := tempoProbeCandidates(res.Samples, excSampleTempoMax)
	if len(ids) == 0 {
		return res
	}
	exists := func(ctx context.Context, id string) (bool, error) {
		spans, err := s.tempo.LookupTrace(ctx, id)
		if err != nil {
			return false, err
		}
		return len(spans) > 0, nil
	}
	cfg := s.tempo.CurrentSettings()
	found := s.cachedTempoProbe(ctx, excSampleTempoKey(cfg.BaseURL, cfg.OrgID, ids), ids, exists)
	mergeTempoSampleTraces(res.Samples, ids, found)
	return res
}

// cachedTempoProbe — cachedJSON üstünden (L1 + Redis + singleflight). Önbellek
// katmanı yoksa (test) ya da okunamazsa doğrudan yoklar. Bilinmeyen içeren
// sonuç uncacheable.
func (s *Server) cachedTempoProbe(ctx context.Context, key string, ids []string, exists tempoTraceExists) map[string]bool {
	probe := func(ctx context.Context) (any, error) {
		found := probeTempoTraces(ctx, ids, exists, excSampleTempoWorkers, excSampleTempoBudget)
		body := tempoSampleProbe{Found: found}
		if len(found) < len(ids) {
			return uncacheable(body), nil
		}
		return body, nil
	}
	if s.cache == nil || s.l1 == nil || s.stats == nil {
		v, _ := probe(ctx)
		if ns, ok := v.(noStoreResult); ok {
			v = ns.v
		}
		return v.(tempoSampleProbe).Found
	}
	raw, _, err := s.cachedJSON(ctx, key, excSampleTempoTTL, false, probe)
	if err != nil {
		return map[string]bool{}
	}
	var out tempoSampleProbe
	if json.Unmarshal(raw, &out) != nil || out.Found == nil {
		return map[string]bool{}
	}
	return out.Found
}

// tempoMarkedSamples — sohbetin Exceptions okuyucusu (chat_exception_groups.go)
// için sarmalayıcı: örnekler aynı Tempo kararından geçer, böylece Tempo'da
// bulunan id CoSRE'ye de "kullanılabilir" gider (CoSRE'nin trace okuması zaten
// Tempo-öncelikli resolveTraceSpans).
type tempoMarkedSamples struct {
	exGroupsReader
	mark func(ctx context.Context, res chstore.ExceptionSamples) chstore.ExceptionSamples
}

func (t tempoMarkedSamples) GetExceptionGroupSamples(ctx context.Context, fingerprint string, limit int) (chstore.ExceptionSamples, error) {
	res, err := t.exGroupsReader.GetExceptionGroupSamples(ctx, fingerprint, limit)
	if err != nil || t.mark == nil {
		return res, err
	}
	return t.mark(ctx, res), nil
}
