package sre

// contextpack_readers_test.go — v0.10.1494 (CoSRE 2d): Context Pack
// okuyucuları (baseline / anomaliler / hata özeti / deploy etkisi + git
// kaynağı), öncelikli kırpma, bayrak kapalı = kayıt + anlatım bayt bayt,
// paketli değerlendirme tekrarı (LLM'siz) ve compare'in paket bloğu.

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

func packOn() ContextPackSettings {
	c := NormalizeContextPackSettings(DefaultContextPackSettings())
	c.Enabled = true
	return c
}

func metricRED(rate, errPct, p99 float64) mcptools.MetricREDResult {
	pts := func(v float64) []chstore.SpanMetricSeries {
		return []chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{{Time: 1, Value: v}, {Time: 2, Value: v}}}}
	}
	return mcptools.MetricREDResult{Source: "vm", Found: true, Series: map[string][]chstore.SpanMetricSeries{
		"rate": pts(rate), "error_rate": pts(errPct), "p99": pts(p99)}}
}

// packReaders — okReaders + paket okuyucuları (sayaçlı).
func packReaders(c *calls, n Narrator, pk ContextPackSettings) readers {
	rd := okReaders(c, n)
	rd.pack = func() ContextPackSettings { return pk }
	rd.redMetric = func(_ context.Context, _, _ string, from, to time.Time, _ int) (mcptools.MetricREDResult, error) {
		c.hit("red")
		if to.Before(start.Add(-time.Hour)) { // taban pencereleri: daha sakin
			return metricRED(10, 0.5, 200), nil
		}
		return metricRED(11, 4, 420), nil
	}
	rd.anomalies = func(_ context.Context, f chstore.ListAnomalyEventsFilter) ([]chstore.AnomalyEvent, error) {
		c.hit("anomalies:" + strings.Join(f.Services, ","))
		return []chstore.AnomalyEvent{{Kind: "trace_op", Pattern: "POST /pay", Service: f.Services[0], PeakRatio: 4, CurrentRatio: 3, StartedAt: start.UnixNano(), LastSeen: start.UnixNano()}}, nil
	}
	rd.errorOps = func(context.Context, string, time.Time, time.Time, int) ([]chstore.ErrorRank, error) {
		c.hit("errorOps")
		return []chstore.ErrorRank{{Name: "POST /pay", Spans: 1000, Errors: 90}}, nil
	}
	rd.exceptions = func(context.Context, chstore.ExceptionFilter) ([]chstore.ExceptionRow, error) {
		c.hit("exceptions")
		return []chstore.ExceptionRow{{Type: "java.net.SocketTimeoutException", Message: "Read timed out", Count: 42, SampleTraceID: "abc123"}}, nil
	}
	return rd
}

// TestPlanStepsContextPackFlag — bayrak kapalı ⇒ paket adımları planlanmaz.
func TestPlanStepsContextPackFlag(t *testing.T) {
	p := chstore.Problem{ID: "p1", Service: "checkout", Metric: "error_rate", StartedAt: start.UnixNano()}
	in := planInput{Anchor: anchorFor(start.UnixNano(), t0), StartNs: start.UnixNano(), Now: t0}
	names := func(ss []StepSpec) string {
		var out []string
		for _, s := range ss {
			out = append(out, s.Name)
		}
		return strings.Join(out, ",")
	}
	off := names(planSteps(p, nil, in))
	if strings.Contains(off, stepBaseline) || strings.Contains(off, stepErrors) || strings.Contains(off, stepAnomalies) {
		t.Fatalf("kapalı plan paket adımı taşımamalı: %s", off)
	}
	pk := packOn()
	in.Pack = &pk
	on := planSteps(p, nil, in)
	if got := names(on); got != off+","+stepErrors+","+stepBaseline+","+stepAnomalies {
		t.Fatalf("açık plan: %s", got)
	}
	for _, s := range on {
		switch s.Name {
		case stepErrors:
			if s.Wave != 1 || s.Errors == nil {
				t.Fatalf("hata özeti dalga 1: %+v", s)
			}
		case stepBaseline, stepAnomalies:
			if s.Wave != 2 {
				t.Fatalf("%s dalga 2 olmalı", s.Name)
			}
		}
	}
}

// TestContextPackFlagOffRecordByteIdentical — bayrak kapalı: paket
// okuyucuları HİÇ çağrılmaz, kayıt Pack/ContextPack taşımaz, anlatım girdisi
// renderInvestigationTR ile bayt bayt.
func TestContextPackFlagOffRecordByteIdentical(t *testing.T) {
	run := func(rd readers) (*Investigation, *fakeNarrator) {
		n := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: x"}
		rd.narrator = n
		inv := newInv()
		runInvestigation(context.Background(), rd, inv, func(*Investigation) {}, func() time.Time { return t0 })
		return inv, n
	}
	c := &calls{}
	plain, pn := run(okReaders(&calls{}, nil))
	off := NormalizeContextPackSettings(DefaultContextPackSettings())
	withReaders, wn := run(packReaders(c, nil, off))
	if c.get("red")+c.get("errorOps")+c.get("exceptions") != 0 || c.get("anomalies:checkout") != 0 {
		t.Fatalf("kapalıyken paket okuyucusu çağrılmamalı: %v", c.n)
	}
	canon := func(inv *Investigation) { // adımlar dalga içinde tamamlanma sırasıyla yazılır
		sort.Slice(inv.Steps, func(i, j int) bool { return inv.Steps[i].Name < inv.Steps[j].Name })
	}
	canon(plain)
	canon(withReaders)
	a, err := Encode(*plain)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Encode(*withReaders)
	// Summary.CallID / ExchangeID her koşuda rastgele — karşılaştırma dışında.
	// v0.10.1500 — süre alanları DUVAR SAATİ ölçümüdür (adım ve kanıt
	// `durationMs`): CI'da bir koşu 1 ms, diğeri 0 ms ölçebiliyordu ve test
	// zaman yüzünden düşüyordu (v0.10.1496/1497 Release Gate). Karşılaştırma
	// önce ikisini de 0'a sabitler; içerik (kanıt, sorgu, satır sayısı) aynen
	// karşılaştırılır, üretim kodu değişmedi. Diğer zaman alanları (`at`,
	// pencereler) enjekte edilen sabit saatten (t0) gelir — normalleştirilmez.
	durationMs := regexp.MustCompile(`"durationMs":\d+`)
	strip := func(s string, inv *Investigation) string {
		s = durationMs.ReplaceAllString(s, `"durationMs":0`)
		return strings.NewReplacer(inv.Summary.CallID, "", inv.Summary.ExchangeID, "").Replace(s)
	}
	if strip(a, plain) != strip(b, withReaders) {
		t.Fatalf("kapalı bayrak kaydı değiştirmemeli:\n%s\n%s", a, b)
	}
	if strings.Contains(a, `"pack"`) || strings.Contains(a, `"contextPack"`) || withReaders.ContextPack != nil {
		t.Fatal("kapalıyken künye yok")
	}
	// Anlatım girdisi: paket bölümü yok; narrationInput kapalıyken
	// renderInvestigationTR'nin kendisi (altınlar: TestNarrationGoldenByteIdentical).
	_ = pn
	for _, bad := range []string{"BASELINE", "ANOMALİLER", "HATA ÖZETİ", "DEĞİŞİKLİKLER"} {
		if strings.Contains(wn.user, bad) {
			t.Fatalf("kapalıyken %s bölümü olmamalı", bad)
		}
	}
	p := chstore.Problem{ID: "p1", Service: "checkout"}
	if got, meta := narrationInput(*withReaders, p, tzdefault.Location(), 2000); meta != nil || got != renderInvestigationTR(*withReaders, p, tzdefault.Location()) {
		t.Fatal("kapalı: narrationInput = renderInvestigationTR, künye yok")
	}
}

// TestContextPackOnLiveRun — açık bayrak: dört okuyucu koşar, anlatım yeni
// bölümleri taşır, künye kayda yazılır, skor değişmez.
func TestContextPackOnLiveRun(t *testing.T) {
	c := &calls{}
	n := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: x"}
	inv := newInv()
	runInvestigation(context.Background(), packReaders(c, n, packOn()), inv, func(*Investigation) {}, func() time.Time { return t0 })
	if !inv.Pack || inv.ContextPack == nil || inv.ContextPack.Mode != PackModePriority || inv.ContextPack.MaxBytes != 8000 {
		t.Fatalf("künye: pack=%v %+v", inv.Pack, inv.ContextPack)
	}
	for _, s := range []string{stepBaseline, stepAnomalies, stepErrors} {
		if st := inv.step(s); st == nil || st.State != StepOK {
			t.Fatalf("%s adımı: %+v", s, st)
		}
	}
	for _, want := range []string{"BASELINE (veri, kaynak: metrik vm — örneklenmemiş)", "- önceki pencere:", "- 7 gün önce:", "ANOMALİLER (veri, skor sırası",
		"HATA ÖZETİ (veri):", "örnek trace abc123"} { // okReaders deploy satırı / servis komşusu taşımaz (db: düğümü)
		if !strings.Contains(n.user, want) {
			t.Fatalf("anlatım girdisi %q taşımalı:\n%s", want, n.user)
		}
	}
	if c.get("anomalies:checkout") != 1 {
		t.Fatalf("özne anomalisi bir kez okunur: %v", c.n)
	}
	// Skor paketi okumaz: aynı gerçekler (paket parçaları hariç) aynı kartlar.
	base := newInv()
	runInvestigation(context.Background(), okReaders(&calls{}, &fakeNarrator{}), base, func(*Investigation) {}, func() time.Time { return t0 })
	if len(base.Hypotheses) != len(inv.Hypotheses) {
		t.Fatal("kart sayısı")
	}
	for i := range base.Hypotheses {
		if base.Hypotheses[i].Kind != inv.Hypotheses[i].Kind || base.Hypotheses[i].Score != inv.Hypotheses[i].Score {
			t.Fatalf("paket skoru değiştirmemeli: %+v vs %+v", base.Hypotheses[i], inv.Hypotheses[i])
		}
	}
	// Bulgu: hata oranı 4% vs 0.5% (+700%) ve p99 420 vs 200 ms → baseline uyarısı.
	found := false
	for _, f := range inv.Findings {
		if f.Kind == "baseline" && f.Level == "warn" && strings.Contains(strings.Join(f.Reasons, " "), "örneklenmemiş") {
			found = true
		}
	}
	if !found {
		t.Fatalf("baseline bulgusu: %+v", inv.Findings)
	}
	if _, err := Encode(*inv); err != nil {
		t.Fatal(err)
	}
}

// TestContextPackReplay — paketli tekrar: LLM 0, künye var; paketsiz tekrar
// bayrak açıkken bile paketsiz (B ↔ C' kıyası).
func TestContextPackReplay(t *testing.T) {
	for _, withPack := range []bool{true, false} {
		c := &calls{}
		n := &fakeNarrator{active: true, auto: true, answer: "x"}
		inv := replayInvestigation(ReplayRequest{RunID: "e1", CaseID: "c1", ProblemID: "p1", Service: "checkout", ProblemStartedAt: start.UnixNano(), Now: t0, ContextPack: withPack})
		runInvestigation(context.Background(), packReaders(c, n, packOn()), &inv, func(*Investigation) {}, func() time.Time { return t0 })
		if n.calls != 0 {
			t.Fatalf("tekrar LLM çağırmaz: %d", n.calls)
		}
		if withPack != (inv.ContextPack != nil) || withPack != (inv.step(stepBaseline) != nil) || inv.Pack != withPack {
			t.Fatalf("pack=%v: künye %+v baseline %+v", withPack, inv.ContextPack, inv.step(stepBaseline))
		}
		st := evalPackStatOf(inv)
		if withPack && (st == nil || st.ReaderData != 3 || st.EstTokens == 0) {
			t.Fatalf("paket özeti: %+v", st)
		}
		if !withPack && st != nil {
			t.Fatal("paketsiz tekrar özet taşımaz")
		}
	}
}

func baselineReq() baselineRequest {
	return baselineRequest{Service: "checkout", From: start.Add(-15 * time.Minute), To: start.Add(15 * time.Minute),
		Kinds: []string{BaselinePrevious, BaselineWeekBefore, BaselineDayBefore}, StartNs: start.UnixNano(), Now: start.Add(15 * time.Minute)}
}

// TestBaselineStepTable — kaynak seçimi, yedek, veri yok, batch, takvim notu.
func TestBaselineStepTable(t *testing.T) {
	ctx := context.Background()
	// 1) metrik yok → span yedeği, kaynak + neden beyanı.
	rd := readers{redSpan: func(_ context.Context, _ string, from, to time.Time) (chstore.ServiceWindowRED, error) {
		return chstore.ServiceWindowRED{Spans: 1800, Errors: 18, P99Ms: 250}, nil
	}}
	o := runBaseline(ctx, rd, baselineReq())
	bf := o.facts.Baseline
	if o.step.State != StepOK || bf.Source != baselineSourceSpan || !strings.Contains(bf.SourceNote, "metrik okuyucusu bağlı değil") ||
		bf.Cur.Rate != 1 || bf.Cur.ErrPct != 1 || len(bf.Rows) != 3 {
		t.Fatalf("span yedeği: %+v %+v", o.step, bf)
	}
	if o.findings != nil {
		t.Fatal("değişim yok → bulgu yok")
	}
	// 2) metrik Found=false → span; metrik hata → span + neden.
	rd.redMetric = func(context.Context, string, string, time.Time, time.Time, int) (mcptools.MetricREDResult, error) {
		return mcptools.MetricREDResult{Source: "vm"}, nil
	}
	if bf := runBaseline(ctx, rd, baselineReq()).facts.Baseline; bf.Source != baselineSourceSpan || !strings.Contains(bf.SourceNote, "metriği bulunamadı") {
		t.Fatalf("Found=false: %+v", bf)
	}
	rd.redMetric = func(context.Context, string, string, time.Time, time.Time, int) (mcptools.MetricREDResult, error) {
		return mcptools.MetricREDResult{}, errors.New("vm down")
	}
	if bf := runBaseline(ctx, rd, baselineReq()).facts.Baseline; bf.Source != baselineSourceSpan || !strings.Contains(bf.SourceNote, "vm down") {
		t.Fatalf("metrik hatası: %+v", bf)
	}
	// 3) metrik var: TÜM pencereler metrikten; 7 gün önce veri yok (saklama).
	weekAgo := start.Add(-6 * 24 * time.Hour)
	rd.redMetric = func(_ context.Context, _, _ string, from, to time.Time, _ int) (mcptools.MetricREDResult, error) {
		switch {
		case to.Before(weekAgo):
			return mcptools.MetricREDResult{Source: "vm", Found: true}, nil // nokta yok
		case to.Before(start.Add(-time.Minute)):
			return metricRED(100, 0.5, 200), nil
		}
		return metricRED(300, 2, 400), nil
	}
	spanCalled := false
	rd.redSpan = func(context.Context, string, time.Time, time.Time) (chstore.ServiceWindowRED, error) {
		spanCalled = true
		return chstore.ServiceWindowRED{}, nil
	}
	o = runBaseline(ctx, rd, baselineReq())
	bf = o.facts.Baseline
	if spanCalled || bf.Source != baselineSourceMetric || bf.Backend != "vm" {
		t.Fatalf("tek kaynak (metrik): span=%v %+v", spanCalled, bf)
	}
	byRef := map[string]BaselineRow{}
	for _, r := range bf.Rows {
		byRef[r.Ref] = r
	}
	if r := byRef[BaselineWeekBefore]; !r.NoData {
		t.Fatalf("7 gün önce veri yok: %+v", r)
	}
	if r := byRef[BaselinePrevious]; r.NoData || r.ErrDeltaPct == nil || *r.ErrDeltaPct != 300 || *r.RateDeltaPct != 200 || *r.P99DeltaPct != 100 {
		t.Fatalf("önceki pencere Δ: %+v", r)
	}
	if o.findings == nil || len(o.findings[0].Reasons) != 3 {
		t.Fatalf("hata + p99 bulgusu + kaynak satırı: %+v", o.findings)
	}
	// 4) batch servis: rate farkı hiç bulgu değildir (yalnız rate değişirse bulgu yok).
	rd.sensitivity = func() chstore.AnomalySensitivityConfig {
		c := chstore.DefaultAnomalySensitivity()
		pats := []string{"checkout"}
		c.BatchServicePatterns = &pats
		return c
	}
	rd.redMetric = func(_ context.Context, _, _ string, from, to time.Time, _ int) (mcptools.MetricREDResult, error) {
		if to.Before(start.Add(-time.Minute)) {
			return metricRED(10, 1, 200), nil
		}
		return metricRED(900, 1, 200), nil
	}
	o = runBaseline(ctx, rd, baselineReq())
	if !o.facts.Baseline.BatchService || o.findings != nil {
		t.Fatalf("batch: %+v %+v", o.facts.Baseline, o.findings)
	}
	if !strings.Contains(sectionBaselineTR(o.facts.Baseline), "batch servis: yük (rate) farkı anomali sayılmaz") {
		t.Fatal("batch notu pakette")
	}
	// 5) şimdiki pencere veri yok → NoData (okunamadı değil).
	rd.redMetric = nil
	rd.redSpan = func(context.Context, string, time.Time, time.Time) (chstore.ServiceWindowRED, error) {
		return chstore.ServiceWindowRED{}, nil
	}
	if o := runBaseline(ctx, rd, baselineReq()); !o.step.NoData || o.step.State != StepOK || !strings.HasPrefix(o.step.Summary, "veri yok") {
		t.Fatalf("veri yok: %+v", o.step)
	}
	// 6) span okunamadı → error.
	rd.redSpan = func(context.Context, string, time.Time, time.Time) (chstore.ServiceWindowRED, error) {
		return chstore.ServiceWindowRED{}, errors.New("ch down")
	}
	if o := runBaseline(ctx, rd, baselineReq()); o.step.State != StepError {
		t.Fatalf("hata: %+v", o.step)
	}
}

// TestBaselineCalendarNote — taban günü tatilse satır not taşır.
func TestBaselineCalendarNote(t *testing.T) {
	ref := start.Add(-7 * 24 * time.Hour).Format("2006-01-02")
	rd := readers{
		redSpan: func(context.Context, string, time.Time, time.Time) (chstore.ServiceWindowRED, error) {
			return chstore.ServiceWindowRED{Spans: 100, P99Ms: 10}, nil
		},
		sensitivity: func() chstore.AnomalySensitivityConfig {
			return chstore.AnomalySensitivityConfig{Calendar: chstore.AnomalyCalendarConfig{Days: []chstore.AnomalySpecialDay{{From: ref, Kind: chstore.CalendarHoliday, Label: "x"}}}}
		},
	}
	req := baselineReq()
	req.Kinds = []string{BaselineWeekBefore}
	bf := runBaseline(context.Background(), rd, req).facts.Baseline
	if len(bf.Rows) != 1 || bf.Rows[0].Note != "takvim: şimdi cumartesi, taban tatil" {
		t.Fatalf("takvim notu: %+v", bf.Rows)
	}
}

// TestReferenceWindowExportedEqualsTool — tek aritmetik.
func TestReferenceWindowExportedEqualsTool(t *testing.T) {
	cur := chstore.PeriodWindow{From: start, To: start.Add(30 * time.Minute)}
	for kind, want := range map[string]chstore.PeriodWindow{
		BaselinePrevious:   {From: start.Add(-30 * time.Minute), To: start},
		BaselineDayBefore:  {From: start.Add(-24 * time.Hour), To: start.Add(30*time.Minute - 24*time.Hour)},
		BaselineWeekBefore: {From: start.Add(-7 * 24 * time.Hour), To: start.Add(30*time.Minute - 7*24*time.Hour)},
	} {
		got, err := mcptools.ReferenceWindow(kind, cur)
		if err != nil || !got.From.Equal(want.From) || !got.To.Equal(want.To) {
			t.Fatalf("%s: %+v %v", kind, got, err)
		}
	}
	if _, err := mcptools.ReferenceWindow("custom", cur); err == nil {
		t.Fatal("custom reddedilir")
	}
}

// TestDeployImpactNearestDeploy — çıpaya en yakın deploy, aynı kaynak, kısa
// sonra-penceresi dürüst not.
func TestDeployImpactNearestDeploy(t *testing.T) {
	var mu sync.Mutex
	var windows [][2]time.Time
	read := func(_ context.Context, from, to time.Time) (REDStat, bool, error) {
		mu.Lock()
		windows = append(windows, [2]time.Time{from, to})
		mu.Unlock()
		if to.After(start.Add(-3 * time.Minute)) {
			return REDStat{Rate: 10, ErrPct: 5, P99Ms: 500}, true, nil
		}
		return REDStat{Rate: 10, ErrPct: 1, P99Ms: 250}, true, nil
	}
	req := baselineReq()
	req.Deploys = []DeployFact{
		{At: start.Add(-90 * time.Minute).UnixNano(), Name: "old"},
		{At: start.Add(-3 * time.Minute).UnixNano(), Name: "checkout", Version: "1.4.2", Origin: "rollback", OriginConfidence: 70},
		{At: start.Add(-3 * time.Hour).UnixNano(), Name: "too-old"},
	}
	im := deployImpact(context.Background(), read, req)
	if im == nil || im.Name != "checkout" || !im.BeforeOK || !im.AfterOK || im.ErrDeltaPct == nil || *im.ErrDeltaPct != 400 || *im.P99DeltaPct != 100 {
		t.Fatalf("etki: %+v", im)
	}
	if len(windows) != 2 || !windows[0][1].Equal(start.Add(-3*time.Minute)) || !windows[1][0].Equal(start.Add(-3*time.Minute)) {
		t.Fatalf("pencereler: %v", windows)
	}
	txt := sectionChangesTR(&DeployFacts{Rows: req.Deploys}, &BaselineFacts{Impact: im}, func(ns int64) string { return time.Unix(0, ns).UTC().Format("15:04") })
	if !strings.Contains(txt, "14:02 checkout 1.4.2 · kaynak: geri alma (git) %70 · deploy etkisi (±10 dk): hata %1.00→%5.00") {
		t.Fatalf("değişiklik satırı:\n%s", txt)
	}
	// Deploy şimdiye çok yakın → kıyas yok, not.
	req.Now = start.Add(-2 * time.Minute)
	if im := deployImpact(context.Background(), read, req); im == nil || im.BeforeOK || !strings.Contains(im.Note, "yeterli süre yok") {
		t.Fatalf("kısa pencere: %+v", im)
	}
	if deployImpact(context.Background(), read, baselineReq()) != nil {
		t.Fatal("deploy yoksa etki yok")
	}
}

// TestDeployFactsCarryOrigin — list_deployments satırının git kaynağı gerçeğe taşınır.
func TestDeployFactsCarryOrigin(t *testing.T) {
	df := deployFactsOf(mcptools.DeployCheck{Rows: []mcptools.ChangeRow{{Source: "rollout", Workload: "checkout", TimeUnixNs: 5, Origin: "redeploy_older", OriginConfidence: 60}, {Source: "inferred", Service: "x"}}})
	if df.Rows[0].Origin != "redeploy_older" || df.Rows[0].OriginConfidence != 60 || df.Rows[1].Origin != "" {
		t.Fatalf("origin: %+v", df.Rows)
	}
	if originTR("redeploy_older", 60) != "eski yapının yeniden deploy'u %60" || originTR("unknown", 0) != "bilinmiyor" || originTR("", 0) != "" {
		t.Fatal("origin etiketi")
	}
}

// TestAnomalyRankDeterministic — tepe × tazelik; eşitlikte erken başlayan.
func TestAnomalyRankDeterministic(t *testing.T) {
	at := start.Add(15 * time.Minute)
	evs := []chstore.AnomalyEvent{
		{Kind: "log_pattern", Pattern: "b", PeakRatio: 6, LastSeen: at.Add(-60 * time.Minute).UnixNano(), StartedAt: 3}, // 6/(1+2)=2
		{Kind: "trace_op", Pattern: "a", PeakRatio: 3, LastSeen: at.UnixNano(), StartedAt: 2},                           // 3
		{Kind: "trace_op", Pattern: "c", PeakRatio: 2, LastSeen: at.Add(time.Hour).UnixNano(), StartedAt: 1},            // 2 (sonra görüldü = yaş 0)
	}
	rows := rankAnomalies(evs, at)
	if rows[0].Pattern != "a" || rows[1].Pattern != "c" || rows[2].Pattern != "b" || rows[0].Rank != 3 || rows[2].Rank != 2 {
		t.Fatalf("sıra: %+v", rows)
	}
	again := rankAnomalies([]chstore.AnomalyEvent{evs[2], evs[0], evs[1]}, at)
	for i := range rows {
		if rows[i] != again[i] {
			t.Fatal("girdi sırasından bağımsız")
		}
	}
}

// TestNeighborAnomalyJoinCapped — ≤5 servis komşusu, db:/ext: yok, tek sorgu.
func TestNeighborAnomalyJoinCapped(t *testing.T) {
	df := &DepsFacts{Edges: []EdgeFact{{Target: "db:pg"}, {Target: "s1"}, {Target: "ext:x"}, {Target: "checkout"}, {Target: "s2"}, {Target: "s3"}, {Target: "s4"}, {Target: "s5"}, {Target: "s6"}}}
	tg := neighborTargets(df, "checkout")
	if strings.Join(tg, ",") != "s1,s2,s3,s4,s5" {
		t.Fatalf("hedefler: %v", tg)
	}
	var reqs []chstore.ListAnomalyEventsFilter
	rd := readers{anomalies: func(_ context.Context, f chstore.ListAnomalyEventsFilter) ([]chstore.AnomalyEvent, error) {
		reqs = append(reqs, f)
		if f.Services[0] == "checkout" {
			return nil, nil
		}
		return []chstore.AnomalyEvent{{Service: "s2", Kind: "trace_op", Pattern: "GET /a", PeakRatio: 2}, {Service: "s2", Kind: "log_pattern", Pattern: "timeout", PeakRatio: 5}, {Service: "zz", PeakRatio: 9}}, nil
	}}
	req := anomaliesRequest{Service: "checkout", From: start, To: start.Add(15 * time.Minute), TopN: 3, Neighbors: tg}
	o := runAnomalies(context.Background(), rd, req)
	af := o.facts.Anomalies
	if len(reqs) != 2 || len(reqs[1].Services) != 5 || reqs[0].ToNs != req.To.UnixNano() || reqs[0].SinceNs != start.Add(-time.Hour).UnixNano() || reqs[0].Limit != anomalyReadLimit {
		t.Fatalf("sorgular: %+v", reqs)
	}
	if len(af.Neighbors) != 1 || af.Neighbors[0].Target != "s2" || af.Neighbors[0].Count != 2 || af.Neighbors[0].TopPattern != "timeout" {
		t.Fatalf("komşu birleşimi: %+v", af.Neighbors)
	}
	txt := sectionNeighborsTR(&DepsFacts{Edges: df.Edges}, af)
	if !strings.Contains(txt, "- s2 hata %0.00 ort 0 ms · eşzamanlı anomali 2 [log_pattern timeout]") || strings.Contains(txt, "db:pg") || strings.Count(txt, "\n") != 6 {
		t.Fatalf("komşu bölümü:\n%s", txt)
	}
	// Özne okunamadı + komşu yok → error.
	rd.anomalies = func(context.Context, chstore.ListAnomalyEventsFilter) ([]chstore.AnomalyEvent, error) {
		return nil, errors.New("x")
	}
	if o := runAnomalies(context.Background(), rd, anomaliesRequest{Service: "checkout", From: start, To: start}); o.step.State != StepError {
		t.Fatalf("hata: %+v", o.step)
	}
}

// TestErrorDigestTopN — ≤3 operasyon + ≤3 grup, kısmi okuma, veri yok.
func TestErrorDigestTopN(t *testing.T) {
	var gotFilter chstore.ExceptionFilter
	rd := readers{
		errorOps: func(_ context.Context, svc string, _, _ time.Time, limit int) ([]chstore.ErrorRank, error) {
			return []chstore.ErrorRank{{Name: "a", Spans: 10, Errors: 5}, {Name: "b", Errors: 3}, {Name: "c", Errors: 2}, {Name: "d", Errors: 1}}, nil
		},
		exceptions: func(_ context.Context, f chstore.ExceptionFilter) ([]chstore.ExceptionRow, error) {
			gotFilter = f
			return nil, errors.New("timeout")
		},
	}
	o := runErrors(context.Background(), rd, errorsRequest{Service: "checkout", From: start, To: start.Add(time.Minute)})
	ef := o.facts.Errors
	if o.step.State != StepPartial || len(ef.Ops) != 3 || ef.Ops[0].ErrPct != 50 || ef.GroupsOK || gotFilter.GroupBy != "type-service" || gotFilter.Services[0] != "checkout" || gotFilter.Limit != 3 {
		t.Fatalf("kısmi: %+v %+v %+v", o.step, ef, gotFilter)
	}
	rd.exceptions = func(context.Context, chstore.ExceptionFilter) ([]chstore.ExceptionRow, error) { return nil, nil }
	rd.errorOps = func(context.Context, string, time.Time, time.Time, int) ([]chstore.ErrorRank, error) { return nil, nil }
	if o := runErrors(context.Background(), rd, errorsRequest{Service: "checkout"}); !o.step.NoData || sectionErrorsTR(o.facts.Errors, nil) != "" {
		t.Fatalf("veri yok: %+v", o.step)
	}
	txt := sectionErrorsTR(&ErrorFacts{Groups: []ErrorGroup{{Type: "E", Count: 4, SampleTraceID: "t1"}}}, &PivotFacts{TraceID: "t9"})
	if txt != "HATA ÖZETİ (veri):\n- istisna E ×4 · örnek trace t1\n- temsilci trace (pivot): t9\n" {
		t.Fatalf("bölüm:\n%q", txt)
	}
}

// TestRenderTRPriorityTrimTable — en düşük öncelik önce kırpılır, satırı
// biten düşer, öncelik 0–1 dokunulmaz; künye sayar.
func TestRenderTRPriorityTrimTable(t *testing.T) {
	mk := func(key string, prio, rows int) PackSection {
		txt := strings.ToUpper(key) + ":\n" + strings.Repeat("- "+strings.Repeat("x", 18)+"\n", rows)
		return section(key, prio, txt)
	}
	pack := ContextPack{Sections: []PackSection{mk("problem", 0, 0), mk("steps", 1, 4), mk("baseline", 2, 3), mk("anomalies", 4, 5), mk("errors", 5, 4), mk("findings", 1, 3)}}
	full, _ := RenderTR(pack, PackBudget{Mode: PackModePriority})
	if leg, _ := RenderTR(pack, PackBudget{}); leg != full {
		t.Fatal("tavansız öncelik kipi = birleşim")
	}
	for _, c := range []struct {
		name    string
		max     int
		trimmed map[string]int
		dropped map[string]bool
		capped  bool
	}{
		{"sığıyor", len(full), map[string]int{}, map[string]bool{}, false},
		// satır 21 B; işaret satırı "… (+k satır kırpıldı)" kendi baytını getirir.
		{"errors kırpılır", len(full) - 30, map[string]int{"errors": 3}, map[string]bool{}, false},
		{"errors düşer, anomaliler kırpılır", len(full) - 120, map[string]int{"errors": 4, "anomalies": 3}, map[string]bool{"errors": true}, false},
		{"yalnız 0–1 kalır + capBytes", 60, map[string]int{"errors": 4, "anomalies": 5, "baseline": 3}, map[string]bool{"errors": true, "anomalies": true, "baseline": true}, true},
	} {
		out, meta := RenderTR(pack, PackBudget{Mode: PackModePriority, MaxBytes: c.max})
		if len(out) > c.max || meta.Capped != c.capped || meta.Bytes != len(out) || meta.Mode != PackModePriority {
			t.Fatalf("%s: %d B (tavan %d) %+v", c.name, len(out), c.max, meta)
		}
		for _, s := range meta.Sections {
			if s.TrimmedRows != c.trimmed[s.Key] || s.Dropped != c.dropped[s.Key] {
				t.Fatalf("%s: %s trimmed=%d dropped=%v", c.name, s.Key, s.TrimmedRows, s.Dropped)
			}
			if (s.Key == "steps" || s.Key == "findings" || s.Key == "problem") && (s.TrimmedRows > 0 || s.Dropped) {
				t.Fatalf("%s: öncelik 0–1 dokunulmaz", c.name)
			}
		}
		if len(c.trimmed) > 0 && !c.capped && (!meta.Trimmed || !strings.Contains(out, "satır kırpıldı)")) {
			t.Fatalf("%s: kırpma işareti", c.name)
		}
		if !c.capped && !strings.HasSuffix(out, mk("findings", 1, 3).Text) {
			t.Fatalf("%s: bulgular sonda ve tam kalmalı", c.name)
		}
	}
}

// TestEvalPackCompareBlock — compare paket kipini ve özetini gösterir;
// kip farklıysa dürüstlük notu.
func TestEvalPackCompareBlock(t *testing.T) {
	mkRun := func(id string, pack bool, stat *EvalPackStat) EvalRun {
		return EvalRun{RunID: id, State: EvalDone, ContextPack: pack, SuggestionKnown: true,
			Cases: []EvalCase{{CaseID: "c1", ProblemID: "p1", Category: "deploy", State: StateDone, Pack: stat}}}
	}
	base := mkRun("eb", false, nil)
	cand := mkRun("ec", true, &EvalPackStat{EstTokens: 900, Trimmed: true, ReaderData: 2, ReaderNoData: 1})
	cand.RerunOf = "eb"
	cmp, err := CompareEvalRuns(base, cand)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Base.ContextPack || !cmp.Cand.ContextPack || cmp.Pack.Base.On || !cmp.Pack.Cand.On || cmp.Pack.Cand.Cases != 1 ||
		cmp.Pack.Cand.AvgTokens == nil || *cmp.Pack.Cand.AvgTokens != 900 || cmp.Pack.Cand.TrimmedCases != 1 || cmp.Pack.Cand.ReaderNoData != 1 || cmp.Pack.Base.AvgTokens != nil {
		t.Fatalf("paket bloğu: %+v", cmp.Pack)
	}
	if !strings.Contains(cmp.Note, "Paket kipi farklı") {
		t.Fatalf("not: %s", cmp.Note)
	}
	if same, _ := CompareEvalRuns(base, base); strings.Contains(same.Note, "Paket kipi farklı") {
		t.Fatal("aynı kip → not yok")
	}
}

// TestStartEvalRunContextPack — paket isteği koşu satırına ve her tekrar işine.
func TestStartEvalRunContextPack(t *testing.T) {
	SetSettings(DefaultSettings())
	defer SetSettings(DefaultSettings())
	st := newEvalFakeStore()
	at := evalT0.Add(-10 * 24 * time.Hour)
	st.cases = []chstore.IncidentCase{confirmedCase("p1", "orders-api", "deploy", "orders-api deploy v2", at)}
	st.problems["p1"] = chstore.Problem{ID: "p1", Service: "orders-api", StartedAt: at.Add(time.Minute).UnixNano(), RuleID: "r"}
	run, err := StartEvalRun(context.Background(), st, EvalRunRequest{From: at.Add(-time.Hour), To: evalT0, Now: evalT0, ContextPack: true}, CurrentSettings())
	if err != nil {
		t.Fatal(err)
	}
	if !run.ContextPack || !st.get(t, chstore.SREReplayKey(run.RunID, "p1")).Pack {
		t.Fatalf("paket bayrağı: run=%v", run.ContextPack)
	}
}
