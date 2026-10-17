package sre

// orchestrator.go — v0.10.1287 (AI SRE Faz 1b: araştırma orkestratörü —
// tarama + deploy + k8s + bağımlılık + pivot + log artışı + özet).
//
// Akış (iş bütçesi ItemBudgetSec, varsayılan 90 s; drain'in bctx'i):
//
//	0. problem satırı + sentezleyici hipotezi (salt-okunur, 3 s) → plan
//	1. DALGA 1 paralel: sweep 17 s · deploys 3 s · deps 8 s
//	2. DALGA 2 paralel: pivot 17 s (rollup exemplar → trace) · log_spike 15 s
//	3. TEK anlatım çağrısı (summary, ≤45 s, arka plan AI kapıları)
//
// Kurallar:
//   - Her adım bitince kayıt yazılır (checkpoint, koordinatör goroutine'inden).
//   - Başarısız adım yalnız kendi durumunu yazar; koşu ASLA kesilmez (kısmi).
//   - Resume: 10 dk'dan taze ok/skipped adım yeniden koşmaz (bulguları kayıtta).
//   - Okumalar sohbetle AYNI çekirdekler ve AYNI önbellek anahtarları
//     (ReadServiceSweepCached / RunTracePivotCached / RunLogSpike → Deps.Memo):
//     drain'in sonucu sohbetin önbelleğini tohumlar; kısmi sonuç saklanmaz.
//   - YALNIZ ÖNERİ: tek yazım kendi kaydı (QueueStore). problems /
//     root_cause_hypotheses / öncelik okunur, ASLA yazılmaz.

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	readCap       = 3 * time.Second
	waveStragglar = 2 * time.Second // bütçe dolduktan sonra adımlara tanınan ek süre
	depsGraphTopN = 20
)

// readers — orkestratörün okumaları (test dikişi). nil alan = okuyucu yok →
// adım dürüst "skipped/error".
type readers struct {
	problem    func(ctx context.Context, id string) (*chstore.Problem, error)
	hypothesis func(ctx context.Context, id string) (*chstore.RootCauseHypothesis, error)
	sweep      func(ctx context.Context, req mcptools.SweepRequest) (mcptools.SweepResult, error)
	deploys    func(ctx context.Context, service string, anchor, to time.Time) (mcptools.DeployCheck, error)
	graph      func(ctx context.Context, service string, from, to time.Time) ([]chstore.ServiceEdge, error)
	changes    func(ctx context.Context, at time.Time, windowSec int) ([]chstore.ChangedService, error)
	blast      func(ctx context.Context, services []string, from, to time.Time) (map[string]chstore.BlastRadius, error)
	exemplar   func(ctx context.Context, req chstore.ExemplarReq) (*chstore.Exemplar, error)
	pivot      func(ctx context.Context, req mcptools.PivotRequest) (mcptools.PivotResult, error)
	spike      func(ctx context.Context, req mcptools.LogSpikeRequest) (mcptools.LogSpikeResult, error)
	narrator   Narrator
	budget     *llmBudget
	settings   func() Settings
}

// readersFrom — üretim okuyucuları: srv.MCPDeps() (sohbetle aynı kurucu).
func readersFrom(d mcptools.Deps, n Narrator, c cache.Cache, now func() time.Time) readers {
	r := readers{
		sweep: func(ctx context.Context, req mcptools.SweepRequest) (mcptools.SweepResult, error) {
			return mcptools.ReadServiceSweepCached(ctx, d, req)
		},
		deploys: func(ctx context.Context, service string, anchor, to time.Time) (mcptools.DeployCheck, error) {
			return mcptools.ReadDeployCheck(ctx, d, service, anchor, to)
		},
		pivot: func(ctx context.Context, req mcptools.PivotRequest) (mcptools.PivotResult, error) {
			return mcptools.RunTracePivotCached(ctx, d, req)
		},
		spike: func(ctx context.Context, req mcptools.LogSpikeRequest) (mcptools.LogSpikeResult, error) {
			return mcptools.RunLogSpike(ctx, d, req)
		},
		narrator: n,
		budget:   newLLMBudget(c, now),
		settings: CurrentSettings,
	}
	if st := d.Store; st != nil {
		r.problem = st.GetProblem
		r.hypothesis = func(ctx context.Context, id string) (*chstore.RootCauseHypothesis, error) {
			return st.GetHypothesis(ctx, "problem", id)
		}
		// Aşağı akış: topology_edges_5m (GetServiceGraphTopN) — ServiceNeighbors
		// DEĞİL (ham spans).
		r.graph = func(ctx context.Context, service string, from, to time.Time) ([]chstore.ServiceEdge, error) {
			return st.GetServiceGraphTopN(ctx, service, 0, from, to, depsGraphTopN)
		}
		r.changes = func(ctx context.Context, at time.Time, windowSec int) ([]chstore.ChangedService, error) {
			return st.GetCorrelatedChangesMVTop(ctx, at, windowSec, windowSec*4, chstore.ChangedServicesMarkPool)
		}
		r.blast = st.GetBlastRadiusBatch
		r.exemplar = st.FindExemplarRollup
	}
	return r
}

// stepOutcome — bir adımın kayda girecek çıktısı.
type stepOutcome struct {
	step     Step
	findings []Finding
	warnings []string
}

// fresh — SAF: adım bu koşuda yeniden koşmadan kullanılabilir mi.
func fresh(s *Step, now time.Time) bool {
	return s != nil && (s.State == StepOK || s.State == StepSkipped) && now.Sub(time.Unix(0, s.At)) < stepFreshForReuse
}

// runInvestigation — tam plan. checkpoint her adım bitince çağrılır.
func runInvestigation(ctx context.Context, rd readers, inv *Investigation, checkpoint func(*Investigation), now func() time.Time) {
	t0 := now()
	p := chstore.Problem{ID: inv.ProblemID, Service: inv.Service, StartedAt: inv.ProblemAt}
	if rd.problem != nil {
		c, cancel := context.WithTimeout(ctx, readCap)
		if got, err := rd.problem(c, inv.ProblemID); err == nil && got != nil && got.ID != "" {
			p = *got
			if p.Service == "" {
				p.Service = inv.Service
			}
		}
		cancel()
	}
	var hyp *chstore.RootCauseHypothesis
	if rd.hypothesis != nil {
		c, cancel := context.WithTimeout(ctx, readCap)
		if h, err := rd.hypothesis(c, inv.ProblemID); err == nil && h != nil {
			hyp = h
			inv.SynthRef = &SynthRef{TopSuspect: h.TopSuspect, Confidence: h.Confidence, ComputedAt: h.ComputedAt}
		}
		cancel()
	}
	inv.Anchor = anchorFor(inv.ProblemAt, t0)
	specs := planSteps(p, hyp, planInput{Env: inv.Env, Priority: inv.Priority, Anchor: inv.Anchor, StartNs: inv.ProblemAt, Now: t0})
	inv.Anchor.Families = anomaly.PlanFamilies(p)
	inv.Anchor.Symptom = symptomOf(inv.Anchor.Families)

	groups := groupFindings(inv.Findings)
	apply := func(o stepOutcome) {
		inv.setStep(o.step)
		groups[o.step.Name] = o.findings
		if o.step.Name == stepSweep && o.step.State != StepError {
			inv.Warnings = o.warnings
		}
		inv.Findings = mergeFindings(groups)
		checkpoint(inv)
	}
	for _, wave := range []int{1, 2} {
		var batch []StepSpec
		for _, s := range specs {
			if s.Wave == wave && !fresh(inv.step(s.Name), t0) {
				batch = append(batch, s)
			}
		}
		runWave(ctx, rd, inv.Service, batch, now, apply)
	}
	if !fresh(inv.step(stepSummary), t0) || inv.Summary == nil {
		apply(stepOutcome{step: narrate(ctx, rd, inv, p, now), findings: groups[stepSummary]})
	}
}

// runWave — dalganın adımları paralel; sonuçlar koordinatörde (apply) sırayla.
// Bütçe dolarsa dönmeyen adım "süre bütçesi doldu" hatası yazar.
func runWave(ctx context.Context, rd readers, service string, batch []StepSpec, now func() time.Time, apply func(stepOutcome)) {
	if len(batch) == 0 {
		return
	}
	ch := make(chan stepOutcome, len(batch))
	for _, s := range batch {
		go func(s StepSpec) {
			sctx, cancel := context.WithTimeout(ctx, s.Cap)
			defer cancel()
			t := now()
			o := execStep(sctx, rd, service, s)
			end := now()
			o.step.Name = s.Name
			o.step.DurationMs = end.Sub(t).Milliseconds()
			o.step.At = end.UnixNano()
			ch <- o
		}(s)
	}
	done := map[string]bool{}
	ctxDone := ctx.Done()
	var straggle <-chan time.Time // nil = bütçe sürüyor (bloklar)
	for len(done) < len(batch) {
		select {
		case o := <-ch:
			if !done[o.step.Name] {
				done[o.step.Name] = true
				apply(o)
			}
		case <-ctxDone:
			ctxDone, straggle = nil, time.After(waveStragglar)
		case <-straggle:
			for _, s := range batch {
				if !done[s.Name] {
					done[s.Name] = true
					apply(stepOutcome{step: Step{Name: s.Name, State: StepError, Summary: "süre bütçesi doldu", At: now().UnixNano()}})
				}
			}
		}
	}
}

// execStep — tek adımın okuması (adım goroutine'i; kayda DOKUNMAZ).
func execStep(ctx context.Context, rd readers, service string, s StepSpec) stepOutcome {
	switch s.Name {
	case stepSweep:
		if rd.sweep == nil || s.Sweep == nil {
			return skippedOutcome(s.Name, "tarama okuyucusu bağlı değil")
		}
		res, err := rd.sweep(ctx, *s.Sweep)
		st, f, w := sweepStep(res, err)
		return stepOutcome{step: st, findings: f, warnings: w}
	case stepDeploys:
		if rd.deploys == nil {
			return skippedOutcome(s.Name, "deploy okuyucusu bağlı değil")
		}
		dc, err := rd.deploys(ctx, service, s.DeployAnchor, s.DeployTo)
		st, f := deployStep(dc, err)
		return stepOutcome{step: st, findings: f}
	case stepDeps:
		if s.Deps == nil {
			return skippedOutcome(s.Name, "bağımlılık isteği yok")
		}
		st, f := runDeps(ctx, rd, *s.Deps)
		return stepOutcome{step: st, findings: f}
	case stepPivot:
		if rd.pivot == nil || s.Pivot == nil {
			return skippedOutcome(s.Name, "pivot okuyucusu bağlı değil")
		}
		req := *s.Pivot
		via := "hipotezin temsilci trace'i"
		if req.TraceID == "" {
			via = "pivotun kendi seçimi"
			if s.Exemplar != nil && rd.exemplar != nil {
				c, cancel := context.WithTimeout(ctx, readCap)
				if ex, err := rd.exemplar(c, *s.Exemplar); err == nil && ex != nil && ex.TraceID != "" {
					req.TraceID, via = ex.TraceID, "rollup exemplar ("+string(s.Exemplar.Kind)+")"
				}
				cancel()
			}
		}
		res, err := rd.pivot(ctx, req)
		st, f := pivotStep(res, err, via)
		return stepOutcome{step: st, findings: f}
	case stepLogSpike:
		if rd.spike == nil || s.Spike == nil {
			return skippedOutcome(s.Name, "log okuyucusu bağlı değil")
		}
		res, err := rd.spike(ctx, *s.Spike)
		st, f := spikeStep(res, err)
		return stepOutcome{step: st, findings: f}
	}
	return skippedOutcome(s.Name, "bilinmeyen adım")
}

func skippedOutcome(name, why string) stepOutcome {
	return stepOutcome{step: Step{Name: name, State: StepSkipped, Summary: why}}
}

// groupFindings — SAF: kayıttaki bulgular → adım başına (0a kaydı: Step boş = sweep).
func groupFindings(in []Finding) map[string][]Finding {
	out := map[string][]Finding{}
	for _, f := range in {
		k := f.Step
		if k == "" {
			k = stepSweep
			f.Step = stepSweep
		}
		out[k] = append(out[k], f)
	}
	return out
}

// findingKindRank — eşit seviyede tür sırası (tarama sırası korunur: k8s >
// deploy > pod > JVM > operasyon > fonksiyon; ardından bağımlılık, pivotun
// türleri, log artışı).
var findingKindRank = map[string]int{
	"k8s": 0, "deploy": 1, "pod": 2, "jvm": 3, "operation": 4, "function": 5,
	"dependency": 6, "trace": 7, "log": 8,
}

// stepRank — aynı tür + seviyede adım sırası.
var stepRank = map[string]int{stepSweep: 0, stepDeploys: 1, stepDeps: 2, stepPivot: 3, stepLogSpike: 4}

// mergeFindings — SAF: adım bulguları → kayıt bulguları (≤8). Sıra: seviye
// (crit > warn > diğer) → tür → adım → adımın kendi sırası. Aynı (tür, ad)
// iki adımdan gelirse ilki kalır (ör. tarama ve pivot aynı pod'u bulur).
func mergeFindings(groups map[string][]Finding) []Finding {
	type it struct {
		f   Finding
		pos int
	}
	var all []it
	for step, fs := range groups {
		for i, f := range fs {
			f.Step = step
			all = append(all, it{f, i})
		}
	}
	lvl := func(l string) int {
		switch l {
		case "crit":
			return 0
		case "warn":
			return 1
		}
		return 2
	}
	kind := func(k string) int {
		if r, ok := findingKindRank[k]; ok {
			return r
		}
		return 9
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if lvl(a.f.Level) != lvl(b.f.Level) {
			return lvl(a.f.Level) < lvl(b.f.Level)
		}
		if kind(a.f.Kind) != kind(b.f.Kind) {
			return kind(a.f.Kind) < kind(b.f.Kind)
		}
		if stepRank[a.f.Step] != stepRank[b.f.Step] {
			return stepRank[a.f.Step] < stepRank[b.f.Step]
		}
		return a.pos < b.pos
	})
	out := []Finding{}
	seen := map[string]bool{}
	for _, x := range all {
		if len(out) >= maxFindings {
			break
		}
		k := x.f.Kind + "\x00" + x.f.Name
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, x.f)
	}
	return out
}

// llmBudget — saatlik AI çağrı sayacı (sre_agent.MaxLLMCallsPerHour). Redis
// (pod'lar arası, Get/Set — cache arayüzünde INCR yok) + bellek; ikisinin
// büyüğü. Yarış pod'lar arasında ±1 sapabilir (bilinçli: tavan koruma, fatura
// değil).
type llmBudget struct {
	mu    sync.Mutex
	cache cache.Cache
	now   func() time.Time
	key   string
	mem   int
}

const llmHourPrefix = "coremetry:sre:llm:hour:"

func newLLMBudget(c cache.Cache, now func() time.Time) *llmBudget {
	if now == nil {
		now = time.Now
	}
	return &llmBudget{cache: c, now: now}
}

func (b *llmBudget) hourKey() string { return llmHourPrefix + b.now().UTC().Format("2006010215") }

// used — bu saatin çağrı sayısı.
func (b *llmBudget) used(ctx context.Context) int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usedLocked(ctx)
}

func (b *llmBudget) usedLocked(ctx context.Context) int {
	k := b.hourKey()
	if k != b.key {
		b.key, b.mem = k, 0
	}
	n := b.mem
	if b.cache != nil {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if raw, ok, err := b.cache.Get(c, k); err == nil && ok && len(raw) > 0 {
			if v, err := strconv.Atoi(string(raw)); err == nil && v > n {
				n = v
			}
		}
	}
	return n
}

// inc — bir çağrı say (çağrıdan ÖNCE: başarısız çağrı da kotadan yer).
func (b *llmBudget) inc(ctx context.Context) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.usedLocked(ctx) + 1
	b.mem = n
	if b.cache != nil {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = b.cache.Set(c, b.key, []byte(strconv.Itoa(n)), 2*time.Hour)
	}
}
