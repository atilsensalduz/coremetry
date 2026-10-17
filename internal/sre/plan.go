package sre

// plan.go — v0.10.1287 (AI SRE Faz 1b: araştırma orkestratörü). SAF plan:
// problemin şekli → adımlar. I/O yok, saat dışarıdan; tablo testli
// (TestPlanSteps, TestPickExemplar).
//
// Dalga 1 (paralel): sweep (servis taraması + k8s kontrolü; P1'de derin),
// deploys (list_deployments okuyucusu, çıpa = problem başlangıcı), deps
// (topology_edges_5m aşağı akış + korele değişiklikler + blast radius).
// Dalga 2 (paralel): pivot (temsilci trace → pod → JVM → log; yalnız
// belirtisi trace'e inen problemde), log_spike (explain_log_spike; belirti
// error/down ya da plan aileleri logs/exceptions). Ardından TEK anlatım
// çağrısı (summary). Adım adları önbellek anahtarıdır (resume).

import (
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// Adım adları (kayıttaki Step.Name; FE etiketleri sreInvestigationView).
const (
	stepSweep    = "sweep"
	stepDeploys  = "deploys"
	stepDeps     = "deps"
	stepPivot    = "pivot"
	stepLogSpike = "log_spike"
	stepSummary  = "summary"
)

// Adım tavanları (tasarım: dalga 1 15/3/8 s, dalga 2 15/15 s, anlatım 45 s;
// toplam ≤ 90 s iş bütçesi). Tarama ve pivot kendi 15 s bütçeleriyle koşar;
// +2 s evre-2 / kod çözme payı.
const (
	capSweep    = 17 * time.Second
	capDeploys  = 3 * time.Second
	capDeps     = 8 * time.Second
	capPivot    = 17 * time.Second
	capLogSpike = 15 * time.Second
	capSummary  = 45 * time.Second
)

// Belirtiler (anomaly.SymptomPlan sözlüğü).
const (
	symptomError      = "error"
	symptomLatency    = "latency"
	symptomDown       = "down"
	symptomSaturation = "saturation"
)

// StepSpec — planlanan bir okuma adımı. İstek alanlarından YALNIZ adımın
// türüne ait olan dolu.
type StepSpec struct {
	Name  string
	Wave  int
	Cap   time.Duration
	Sweep *mcptools.SweepRequest
	// Deploy adımı: çıpa (problem başlangıcı) + pencere sonu.
	DeployAnchor, DeployTo time.Time
	// Bağımlılık adımı: pencere + korelasyon çıpası.
	Deps *depsRequest
	// Pivot: istek + (temsilci trace kimliği yoksa) rollup exemplar sorgusu.
	Pivot    *mcptools.PivotRequest
	Exemplar *chstore.ExemplarReq
	Spike    *mcptools.LogSpikeRequest
}

// depsRequest — bağımlılık adımının girdisi.
type depsRequest struct {
	Service   string
	From, To  time.Time
	At        time.Time // korelasyon çıpası (problem başlangıcı)
	WindowSec int
}

// symptomOf — SAF: plan aileleri → belirti (pivot seçimi / log seviyesi).
// anomaly.PlanFamilies sözlüğü: error → exceptions+logs+business; latency →
// saturation+operations+business; down → runtime+logs; saturation →
// saturation+runtime; hacim → boş.
func symptomOf(families []string) string {
	has := map[string]bool{}
	for _, f := range families {
		has[f] = true
	}
	switch {
	case has["exceptions"]:
		return symptomError
	case has["operations"]:
		return symptomLatency
	case has["runtime"] && has["logs"]:
		return symptomDown
	case has["saturation"]:
		return symptomSaturation
	}
	return ""
}

// planInput — planın problem dışı girdileri.
type planInput struct {
	Env      string
	Priority string
	Anchor   Anchor // [başlangıç − 15 dk, min(now, başlangıç + 30 dk)]
	StartNs  int64  // problem başlangıcı (unix ns; 0 → çıpa başı + 15 dk)
	Now      time.Time
}

// planSteps — SAF: problem + (varsa) sentezleyici hipotezi → adımlar.
// Her zaman: sweep (P1'de derin; deploy kendi adımında olduğu için
// Checks=[k8s]), deploys, deps. pivot: belirti trace'e iniyorsa (error /
// latency / down) ya da hipotezin temsilci trace'i varsa. log_spike: aileler
// logs / exceptions ya da belirti error / down.
func planSteps(p chstore.Problem, hyp *chstore.RootCauseHypothesis, in planInput) []StepSpec {
	fam := anomaly.PlanFamilies(p)
	sym := symptomOf(fam)
	from, to := time.Unix(0, in.Anchor.From).UTC(), time.Unix(0, in.Anchor.To).UTC()
	start := time.Unix(0, in.StartNs).UTC()
	if in.StartNs <= 0 {
		start = from.Add(15 * time.Minute)
	}
	svc := p.Service
	steps := []StepSpec{
		{Name: stepSweep, Wave: 1, Cap: capSweep, Sweep: &mcptools.SweepRequest{
			Service: svc, Env: in.Env, From: from, To: to, Deep: in.Priority == "P1",
			Checks: []string{mcptools.SweepCheckK8s}, Anchor: start,
		}},
		{Name: stepDeploys, Wave: 1, Cap: capDeploys, DeployAnchor: start, DeployTo: in.Now.UTC()},
		{Name: stepDeps, Wave: 1, Cap: capDeps, Deps: &depsRequest{Service: svc, From: from, To: to, At: start, WindowSec: depsWindowSec(start, to)}},
	}
	if pr, ex, ok := pickExemplar(p, hyp, sym, in); ok {
		steps = append(steps, StepSpec{Name: stepPivot, Wave: 2, Cap: capPivot, Pivot: &pr, Exemplar: ex})
	}
	logs := sym == symptomError || sym == symptomDown
	for _, f := range fam {
		if f == "logs" || f == "exceptions" {
			logs = true
		}
	}
	if logs {
		level := "all"
		if sym == symptomError || sym == symptomDown {
			level = "error"
		}
		steps = append(steps, StepSpec{Name: stepLogSpike, Wave: 2, Cap: capLogSpike, Spike: &mcptools.LogSpikeRequest{
			Service: svc, Env: in.Env, Level: level, SpikeFrom: from, SpikeTo: to,
		}})
	}
	return steps
}

// depsWindowSec — SAF: korelasyon penceresi (başlangıç → pencere sonu, 5..30 dk).
func depsWindowSec(start, to time.Time) int {
	w := int(to.Sub(start).Seconds())
	switch {
	case w < 300:
		return 300
	case w > 1800:
		return 1800
	}
	return w
}

// pickExemplar — SAF: pivotun trace seçimi.
//   - hipotezin temsilci trace'i (ExemplarTraceID) → PivotRequest{TraceID}.
//   - yoksa belirti trace'e iniyorsa (error / latency / down): rollup
//     exemplar sorgusu (spanmetrics_1m argMax durumları, [başlangıç − 5 dk,
//     başlangıç + 15 dk], error belirtisinde hatalı en yavaş, aksi hâlde en
//     yavaş) + yedek PivotRequest{Service, Pick} (exemplar bulunamazsa pivot
//     kendi seçimini yapar).
//   - doygunluk / hacim / belirtisiz → pivot yok (ok=false).
func pickExemplar(p chstore.Problem, hyp *chstore.RootCauseHypothesis, symptom string, in planInput) (mcptools.PivotRequest, *chstore.ExemplarReq, bool) {
	from, to := time.Unix(0, in.Anchor.From).UTC(), time.Unix(0, in.Anchor.To).UTC()
	if hyp != nil && hyp.ExemplarTraceID != "" {
		return mcptools.PivotRequest{TraceID: hyp.ExemplarTraceID, Service: p.Service, From: from, To: to}, nil, true
	}
	if p.Service == "" {
		return mcptools.PivotRequest{}, nil, false
	}
	switch symptom {
	case symptomError, symptomLatency, symptomDown:
	default:
		return mcptools.PivotRequest{}, nil, false
	}
	start := time.Unix(0, in.StartNs).UTC()
	if in.StartNs <= 0 {
		start = from.Add(15 * time.Minute)
	}
	exTo := start.Add(15 * time.Minute)
	if !in.Now.IsZero() && exTo.After(in.Now) {
		exTo = in.Now.UTC()
	}
	kind, pick := chstore.ExemplarSlow, "slowest"
	if symptom == symptomError || symptom == symptomDown {
		kind, pick = chstore.ExemplarError, "slowest_error"
	}
	ex := &chstore.ExemplarReq{Service: p.Service, From: start.Add(-5 * time.Minute), To: exTo, Kind: kind}
	return mcptools.PivotRequest{Service: p.Service, Env: in.Env, Pick: pick, From: from, To: to}, ex, true
}
