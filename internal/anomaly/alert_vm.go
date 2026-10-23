package anomaly

// alert_vm.go — v0.10.1489: alarm kurallarının VictoriaMetrics okuyucusu (G/Ç).
// Saf parçalar alert_vm_query.go'da.
//
// TEK OKUYUCU, ÜÇ TÜKETİCİ: evaluator (tik başına Prepare → Measure), Alerts
// kural önizlemesi (Series) ve taban önerisi (Baseline) AYNI süreç-içi örneği
// ve AYNI kaynak kararını (alertVMState.decide) kullanır — önizleme ≠ canlı
// olamaz. Keşif (aile kapsamı, durum-etiketli servisler) 1478'in vmREDSource
// yöntemleri; bu okuyucu kendi örneğini tutar (anomali dedektörünün tik
// goroutine'ine kilitsiz bağlı belleğine dokunmaz).
//
// Tik başına iş (Prepare, evaluator lideri, tek bütçe — alertVMTickBudget):
//  1. keşif: aile kapsamı (vmMetaTTL'de bir; 2 etiket okuması/aile + ortam),
//     durum kümesi (vmStatusTTL'de bir, aile başına 1 anlık), aile üyeliği
//     (vmMetaTTL'de bir, aile başına 1 anlık: son 1 sa istek serisi).
//  2. değer: kuralların DISTINCT (pencere) × kullanılabilir aile × gereken tür
//     → filo geneli `sum by (servis)` ANLIK sorgu (t = şimdi − vmIngestLag).
//     Kural sayısıyla değil, distinct pencere sayısıyla büyür. Tüm kurallar
//     servise sabitse servis süzgeci eklenir. Tavan alertVMMaxQueriesPerTick,
//     sorgu başına vmQueryTimeout, vmConcurrency paralel. Hata veren (aile, tür)
//     vmKindBackoff boyunca denenmez → o türü isteyen kural×servis span'e düşer.

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// Bütçe sabitleri (DECISIONS v0.10.1489).
const (
	// alertVMTickBudget — evaluator tiki (1 dk) içinde VM'e ayrılan süre.
	alertVMTickBudget = 15 * time.Second
	// alertVMMaxQueriesPerTick — değer sorgusu tavanı (keşif hariç).
	alertVMMaxQueriesPerTick = 24
	// alertVMAPITimeout — önizleme / taban isteğinin VM süresi.
	alertVMAPITimeout = 8 * time.Second
	// alertVMDiscoveryBudget — keşif turunun süresi (çağıranın iptalinden bağımsız).
	alertVMDiscoveryBudget = 10 * time.Second
	// alertVMBaselineLookback — taban önerisinin geriye bakışı (span ile aynı).
	alertVMBaselineLookback = 7 * 24 * time.Hour
	// alertVMBaselineWindowSec — taban önerisinin kovası + pencere verilmediğinde
	// kaynak kararının penceresi.
	alertVMBaselineWindowSec = 300
	alertVMBaselineMaxPoints = 2100
)

// AlertVMNeed — bir kuralın tik ihtiyacı (Services nil = tüm servisler).
type AlertVMNeed struct {
	Metric    string
	WindowSec int
	Services  []string
}

// AlertVMMeasure — kural×servis ölçüsü. Source.VM=false → çağıran span yolunu
// koşar (değer + sayım İKİSİ de span'den; kaynak karışmaz).
type AlertVMMeasure struct {
	Source  AlertSource
	Value   float64
	Present bool   // false = pencerede ölçü yok (absentMeasure anlamı)
	Count   uint64 // pencerede GERÇEK istek sayısı (minSamples)
}

// AlertVMReader — süreç başına tek örnek (main.go): evaluator + API.
type AlertVMReader struct {
	wire atomic.Pointer[vmREDBox]
	sens func() chstore.AnomalySensitivityConfig
	// lk — bağlam-duyarlı kilit (kapasite 1): API isteği tikin keşfini
	// bekleyemezse son anlık görüntüyle karar verir.
	lk       chan struct{}
	src      *vmREDSource
	presence [vmFamN]alertPresence
	backoff  map[[2]int]time.Time
	last     atomic.Pointer[alertVMState]
	clock    func() time.Time
}

type alertPresence struct {
	at       time.Time
	scopeKey string
	svc      map[string]bool // nil = bilinmiyor
	reason   string
}

// NewAlertVMReader — sens: yayınlanmış anomaly_sensitivity (atomik okuma).
func NewAlertVMReader(sens func() chstore.AnomalySensitivityConfig) *AlertVMReader {
	return &AlertVMReader{sens: sens, lk: make(chan struct{}, 1), src: newVMREDSource(), backoff: map[[2]int]time.Time{}, clock: time.Now}
}

// SetVM — nil = VM yok (her kural span).
func (r *AlertVMReader) SetVM(vm REDVM) {
	if r == nil {
		return
	}
	if vm == nil {
		r.wire.Store(nil)
		return
	}
	r.wire.Store(&vmREDBox{vm: vm})
}

func (r *AlertVMReader) lock(ctx context.Context) bool {
	select {
	case r.lk <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}
func (r *AlertVMReader) unlock() { <-r.lk }

// alertVMState — kararın anlık görüntüsü (salt okunur paylaşılır).
type alertVMState struct {
	mode       string
	configured bool
	reason     string
	vnow       time.Time
	meta       []vmFamMeta
	presence   [vmFamN]map[string]bool
	backoff    map[[2]int]time.Time
	vm         REDVM
}

// state — keşfi tazeler ve anlık görüntüyü döner (asla nil).
func (r *AlertVMReader) state(ctx context.Context, vnow time.Time) (*alertVMState, int) {
	cfg := chstore.AnomalySensitivityConfig{}
	if r.sens != nil {
		cfg = r.sens()
	}
	st := &alertVMState{mode: cfg.AlertSourceMode(), vnow: vnow}
	if st.mode == chstore.AnomalyREDSourceSpan {
		st.reason = alertReasonSpanSetting
		return st, 0
	}
	box := r.wire.Load()
	if box == nil || box.vm == nil || !box.vm.Configured() {
		st.reason = redReasonNotConfigured
		return st, 0
	}
	st.configured, st.vm = true, box.vm
	if !r.lock(ctx) {
		// Tik keşfi sürüyor: son görüntü (yoksa VM kararı verilemez → span).
		if prev := r.last.Load(); prev != nil && prev.configured {
			cp := *prev
			cp.mode, cp.vnow, cp.vm = st.mode, vnow, box.vm
			return &cp, 0
		}
		st.configured = false
		st.reason = redReasonVMError + ": keşif meşgul"
		return st, 0
	}
	defer r.unlock()
	// Keşif çağıranın iptalinden AYRI bağlamda: önizlemeden ayrılan bir istemci
	// aile kapsamını "okunamadı" diye 10 dk zehirlemesin (evaluator da okuyor).
	dctx, dcancel := context.WithTimeout(context.WithoutCancel(ctx), alertVMDiscoveryBudget)
	defer dcancel()
	q := r.discover(dctx, box.vm, cfg.REDSourceEnvScope(), vnow)
	st.meta = append([]vmFamMeta(nil), r.src.meta...)
	for fi := range r.presence {
		st.presence[fi] = r.presence[fi].svc
	}
	st.backoff = make(map[[2]int]time.Time, len(r.backoff))
	for k, v := range r.backoff {
		st.backoff[k] = v
	}
	r.last.Store(st)
	return st, q
}

// discover — 1478 keşfi (refreshMeta / refreshStatus) + aile üyeliği. Kilit altında.
func (r *AlertVMReader) discover(ctx context.Context, vm REDVM, env string, vnow time.Time) int {
	lm := vm.MetricLabelMap().Normalized()
	if key := env + "|" + lm.Service + "|" + lm.Env; key != r.src.envKey {
		for i := range r.src.meta {
			r.src.meta[i].at = time.Time{}
		}
		r.src.envKey = key
	}
	queries := 0
	for fi := range vmREDFamilies {
		r.src.refreshMeta(ctx, vm, fi, lm, env, vnow)
		queries += r.src.refreshStatus(ctx, vm, fi, vnow)
		queries += r.refreshPresence(ctx, vm, fi, vnow)
	}
	return queries
}

// refreshPresence — ailede son 1 sa istek serisi taşıyan servisler (vmMetaTTL).
func (r *AlertVMReader) refreshPresence(ctx context.Context, vm REDVM, fi int, vnow time.Time) int {
	m := r.src.meta[fi]
	p := &r.presence[fi]
	if !m.ok {
		*p = alertPresence{}
		return 0
	}
	if p.scopeKey == m.scopeKey && p.svc != nil && vnow.Sub(p.at) < vmMetaTTL {
		return 0
	}
	if p.scopeKey == m.scopeKey && p.svc == nil && !p.at.IsZero() && vnow.Sub(p.at) < vmKindBackoff {
		return 0 // son okuma hata verdi: geri çekilme (tik bütçesini her dakika yakmasın)
	}
	q, ok := alertVMPresenceQuery(vmREDFamilies[fi], m.scope)
	if !ok {
		return 0
	}
	qctx, cancel := context.WithTimeout(ctx, vmQueryTimeout)
	defer cancel()
	ser, err := vm.QueryPromQLInstant(qctx, q, vnow)
	if err != nil {
		if p.scopeKey == m.scopeKey && p.svc != nil && vnow.Sub(p.at) < vmMetaStale {
			return 1 // eski küme geçerli (okunamadı ≠ yok)
		}
		*p = alertPresence{at: vnow, scopeKey: m.scopeKey, reason: redReasonVMError + ": servis kümesi"}
		return 1
	}
	set := make(map[string]bool, len(ser))
	for _, sr := range ser {
		if svc, ok := alertVMLabel(sr.GroupKey, m.scope.SvcLabel); ok {
			set[svc] = true
		}
	}
	*p = alertPresence{at: vnow, scopeKey: m.scopeKey, svc: set}
	return 1
}

// familyFor — servisin ailesi: sıradaki ilk kullanılabilir aile ki servis
// üyesi. Aileler ASLA toplanmaz. Okuma hatası "yok" sayılmaz: hata veren
// aileden sonrakine geçilmez (servis o ailede olabilir) → span, gerekçeli.
func (st *alertVMState) familyFor(service string) (int, string) {
	reason, anyOK := "", false
	for fi := range vmREDFamilies {
		if fi >= len(st.meta) {
			break
		}
		m := st.meta[fi]
		if !m.ok {
			if strings.HasPrefix(m.reason, redReasonVMError) {
				return -1, m.reason
			}
			if reason == "" && m.reason != "" && m.reason != redReasonNoFamily {
				reason = m.reason
			}
			continue
		}
		anyOK = true
		p := st.presence[fi]
		if p == nil {
			return -1, redReasonVMError + ": servis kümesi (" + vmREDFamilies[fi].ID + ")"
		}
		if p[service] {
			return fi, ""
		}
	}
	if anyOK {
		return -1, redReasonNoService
	}
	if reason == "" {
		reason = redReasonNoFamily
	}
	return -1, reason
}

// kindsOK — metriğin türleri bu ailede ve serviste ölçülebilir mi.
func (st *alertVMState) kindsOK(fi int, service string, kinds []alertVMKind) (bool, string) {
	m := st.meta[fi]
	for _, k := range kinds {
		if k == akErr {
			switch {
			case len(m.scope.StatusLabels) == 0:
				return false, redReasonNoStatus
			case m.statusSvc == nil:
				return false, redReasonVMError + ": durum etiketi kümesi"
			case !m.statusSvc[service]:
				return false, redReasonNoStatus
			}
		}
		if until, ok := st.backoff[[2]int{fi, int(k)}]; ok && st.vnow.Before(until) {
			return false, redReasonVMError + " (" + k.String() + ")"
		}
	}
	return true, ""
}

// decide — kural×servis kaynak kararı (evaluator, önizleme ve taban AYNI işlev).
func (st *alertVMState) decide(service, metric string, windowSec int) (int, AlertSource) {
	in := alertPickInput{SpanSetting: st.mode == chstore.AnomalyREDSourceSpan, Configured: st.configured}
	kinds, ok := alertVMMetricKinds(metric)
	in.MetricSupported = ok
	in.WindowOK = windowSec >= alertVMMinWindowSec
	fi := -1
	if !in.SpanSetting && in.Configured && in.MetricSupported && in.WindowOK {
		fi, in.FamilyReason = st.familyFor(service)
		in.FamilyFound = fi >= 0
		if in.FamilyFound {
			in.KindsOK, in.KindReason = st.kindsOK(fi, service, kinds)
		}
	}
	use, reason := pickAlertSource(in)
	if st.reason != "" && (!st.configured || in.SpanSetting) {
		reason = st.reason
	}
	if !use {
		return -1, SpanAlertSource(reason)
	}
	return fi, AlertSource{VM: true, Family: vmREDFamilies[fi].Label}
}

// ── Tik (evaluator) ─────────────────────────────────────────────────────────

type alertResKey struct {
	fam    int
	kind   alertVMKind
	window int
}

type alertPlanned struct {
	key   alertResKey
	query string
}

// alertVMPlan — SAF (görüntü verilince): tikin sorgu planı + bütçe dışı kalanlar.
// Sıra: pencere artan, aile sırası, tür sırası (count önce).
func alertVMPlan(st *alertVMState, needs []AlertVMNeed) (plan []alertPlanned, skipped []alertResKey) {
	kindsByWin := map[int]map[alertVMKind]bool{}
	var services []string
	wildcard := false
	seenSvc := map[string]bool{}
	for _, n := range needs {
		kinds, ok := alertVMMetricKinds(n.Metric)
		if !ok || n.WindowSec < alertVMMinWindowSec {
			continue
		}
		if kindsByWin[n.WindowSec] == nil {
			kindsByWin[n.WindowSec] = map[alertVMKind]bool{}
		}
		for _, k := range kinds {
			kindsByWin[n.WindowSec][k] = true
		}
		if len(n.Services) == 0 {
			wildcard = true
		}
		for _, s := range n.Services {
			if s != "" && !seenSvc[s] {
				seenSvc[s] = true
				services = append(services, s)
			}
		}
	}
	wins := make([]int, 0, len(kindsByWin))
	for w := range kindsByWin {
		wins = append(wins, w)
	}
	sort.Ints(wins)
	for _, w := range wins {
		for fi, f := range vmREDFamilies {
			if fi >= len(st.meta) || !st.meta[fi].ok || len(st.presence[fi]) == 0 {
				continue
			}
			sc := st.meta[fi].scope
			svcM := ""
			if !wildcard && len(services) > 0 {
				if m := vmetrics.EnvMatcher(sc.SvcLabel, services); len(m) <= vmMaxEnvMatcherLen {
					svcM = m
				}
			}
			for k := alertVMKind(0); k < akN; k++ {
				if !kindsByWin[w][k] {
					continue
				}
				if until, ok := st.backoff[[2]int{fi, int(k)}]; ok && st.vnow.Before(until) {
					continue
				}
				q, ok := alertVMExpr(f, k, sc, w, svcM, nil)
				if !ok {
					continue
				}
				key := alertResKey{fam: fi, kind: k, window: w}
				if len(plan) >= alertVMMaxQueriesPerTick {
					skipped = append(skipped, key)
					continue
				}
				plan = append(plan, alertPlanned{key: key, query: q})
			}
		}
	}
	return plan, skipped
}

// AlertVMTick — bir evaluator tikinin VM ölçüleri. nil-güvenli (nil = span).
type AlertVMTick struct {
	st     *alertVMState
	vals   map[alertResKey]map[string]float64
	quant  map[alertResKey]map[string]map[string]float64
	failed map[alertResKey]string
	counts map[string]map[string]int // metrik → kaynak → kural×servis
	stats  AlertVMStats
}

// Prepare — tikin VM ölçüleri (asla nil). Bütün hatalar gerekçeye yazılır.
func (r *AlertVMReader) Prepare(ctx context.Context, needs []AlertVMNeed, now time.Time) *AlertVMTick {
	start := r.clock()
	vnow := now.Add(-vmIngestLag)
	bctx, cancel := context.WithTimeout(ctx, alertVMTickBudget)
	defer cancel()
	st, dq := r.state(bctx, vnow)
	t := &AlertVMTick{st: st, vals: map[alertResKey]map[string]float64{}, quant: map[alertResKey]map[string]map[string]float64{},
		failed: map[alertResKey]string{}, counts: map[string]map[string]int{}}
	t.stats = AlertVMStats{Mode: st.mode, Configured: st.configured, Reason: st.reason, DiscoveryQueries: dq}
	if st.mode == chstore.AnomalyREDSourceSpan || !st.configured {
		t.stats.LastMs = r.clock().Sub(start).Milliseconds()
		return t
	}
	plan, skipped := alertVMPlan(st, needs)
	for _, k := range skipped {
		t.failed[k] = alertReasonBudget
	}
	t.stats.Skipped = len(skipped)
	type res struct {
		p   alertPlanned
		ser []chstore.SpanMetricSeries
		err error
	}
	out := make([]res, len(plan))
	sem := make(chan struct{}, vmConcurrency)
	var wg sync.WaitGroup
	for i, p := range plan {
		wg.Add(1)
		go func(i int, p alertPlanned) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			qctx, qc := context.WithTimeout(bctx, vmQueryTimeout)
			defer qc()
			ser, err := st.vm.QueryPromQLInstant(qctx, p.query, vnow)
			out[i] = res{p: p, ser: ser, err: err}
		}(i, p)
	}
	wg.Wait()
	var failedKinds [][2]int
	for _, o := range out {
		t.stats.Queries++
		if o.err != nil {
			t.stats.QueryErrs++
			t.stats.LastError = fmt.Sprintf("%s/%s/%ds: %v", vmREDFamilies[o.p.key.fam].ID, o.p.key.kind, o.p.key.window, o.err)
			t.failed[o.p.key] = redReasonVMError + " (" + o.p.key.kind.String() + ")"
			failedKinds = append(failedKinds, [2]int{o.p.key.fam, int(o.p.key.kind)})
			continue
		}
		svcLabel := st.meta[o.p.key.fam].scope.SvcLabel
		if o.p.key.kind == akQuant {
			m := map[string]map[string]float64{}
			for _, sr := range o.ser {
				svc, ok := alertVMLabel(sr.GroupKey, svcLabel)
				phi, pok := alertVMLabel(sr.GroupKey, "phi")
				if !ok || !pok || len(sr.Points) == 0 {
					continue
				}
				if m[svc] == nil {
					m[svc] = map[string]float64{}
				}
				m[svc][phi] = sr.Points[len(sr.Points)-1].Value
			}
			t.quant[o.p.key] = m
			continue
		}
		m := map[string]float64{}
		for _, sr := range o.ser {
			svc, ok := alertVMLabel(sr.GroupKey, svcLabel)
			if !ok || len(sr.Points) == 0 {
				continue
			}
			v := sr.Points[len(sr.Points)-1].Value
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			m[svc] = v
		}
		t.vals[o.p.key] = m
	}
	if len(failedKinds) > 0 && r.lock(ctx) {
		for _, k := range failedKinds {
			r.backoff[k] = vnow.Add(vmKindBackoff)
		}
		r.unlock()
	}
	t.stats.LastMs = r.clock().Sub(start).Milliseconds()
	return t
}

// Measure — kural×servis ölçüsü. nil tik = span (okuyucu bağlı değil).
func (t *AlertVMTick) Measure(service, metric string, windowSec int) AlertVMMeasure {
	if t == nil || t.st == nil {
		return AlertVMMeasure{Source: SpanAlertSource(alertReasonNoTick)}
	}
	m := t.measure(service, metric, windowSec)
	if t.counts[metric] == nil {
		t.counts[metric] = map[string]int{}
	}
	t.counts[metric][m.Source.Label()]++
	return m
}

func (t *AlertVMTick) measure(service, metric string, windowSec int) AlertVMMeasure {
	fi, src := t.st.decide(service, metric, windowSec)
	if !src.VM {
		return AlertVMMeasure{Source: src}
	}
	f := vmREDFamilies[fi]
	kinds, _ := alertVMMetricKinds(metric)
	s := alertVMSample{Scale: alertVMScale(f), WindowSec: windowSec}
	for _, k := range kinds {
		key := alertResKey{fam: fi, kind: k, window: windowSec}
		if reason, bad := t.failed[key]; bad {
			return AlertVMMeasure{Source: SpanAlertSource(reason)}
		}
		if k == akQuant {
			qm, ok := t.quant[key]
			if !ok {
				return AlertVMMeasure{Source: SpanAlertSource(alertReasonBudget)}
			}
			s.Quant = qm[service]
			continue
		}
		vm, ok := t.vals[key]
		if !ok {
			return AlertVMMeasure{Source: SpanAlertSource(alertReasonBudget)}
		}
		v, have := vm[service]
		switch k {
		case akCount:
			s.Count, s.HaveCount = v, have
		case akErr:
			s.Err, s.HaveErr = v, have
		case akSum:
			s.Sum, s.HaveSum = v, have
		}
	}
	val, present := alertVMValue(metric, s)
	cnt := 0.0
	if s.HaveCount && s.Count > 0 {
		cnt = s.Count
	}
	return AlertVMMeasure{Source: src, Value: val, Present: present, Count: uint64(math.Round(cnt))}
}

// Publish — tikin ölçümünü yayınlar (evaluator turu bitince) + değişince log.
func (t *AlertVMTick) Publish() {
	if t == nil {
		return
	}
	st := t.stats
	st.LastUnix = time.Now().Unix()
	st.Sources = t.counts
	if t.st != nil {
		for fi, f := range vmREDFamilies {
			fs := AlertVMFamilyStats{ID: f.ID}
			if fi < len(t.st.meta) {
				fs.Usable, fs.Reason = t.st.meta[fi].ok, t.st.meta[fi].reason
			}
			fs.Services = len(t.st.presence[fi])
			st.Families = append(st.Families, fs)
		}
	}
	alertObs.Store(&st)
	if st.Queries > 0 || st.QueryErrs > 0 {
		log.Printf("[evaluator] alarm VM kaynağı: %d sorgu (%d hata, %d bütçe dışı) + %d keşif, %d ms · %v",
			st.Queries, st.QueryErrs, st.Skipped, st.DiscoveryQueries, st.LastMs, st.Sources)
	}
}

// ── Ölçüm ──────────────────────────────────────────────────────────────────

// AlertVMFamilyStats — ailenin alarm okuyucusundaki durumu.
type AlertVMFamilyStats struct {
	ID       string `json:"id"`
	Usable   bool   `json:"usable"`
	Reason   string `json:"reason,omitempty"`
	Services int    `json:"services"` // aile üyesi servis sayısı (son 1 sa)
}

// AlertVMStats — alarm okuyucusunun son evaluator tiki.
type AlertVMStats struct {
	Mode             string                    `json:"mode"`
	Configured       bool                      `json:"configured"`
	Reason           string                    `json:"reason,omitempty"`
	LastUnix         int64                     `json:"lastUnix"`
	LastMs           int64                     `json:"lastMs"`
	Queries          int                       `json:"lastQueries"`
	QueryErrs        int                       `json:"lastQueryErrors"`
	Skipped          int                       `json:"lastSkipped"`
	DiscoveryQueries int                       `json:"lastDiscoveryQueries"`
	LastError        string                    `json:"lastError,omitempty"`
	Families         []AlertVMFamilyStats      `json:"families"`
	Sources          map[string]map[string]int `json:"sources,omitempty"` // metrik → vm|span → kural×servis
}

var alertObs atomic.Pointer[AlertVMStats]

// AlertVMObservability — son evaluator tikinin ölçümü (evaluator lideri
// olmayan pod'da boş = doğru cevap).
func AlertVMObservability() AlertVMStats {
	if p := alertObs.Load(); p != nil {
		return *p
	}
	return AlertVMStats{}
}

// ── Önizleme + taban (API) ──────────────────────────────────────────────────

// SourceFor — kural×servis kaynağı (önizleme rozeti). nil okuyucu = span.
func (r *AlertVMReader) SourceFor(ctx context.Context, service, metric string, windowSec int) AlertSource {
	if r == nil {
		return SpanAlertSource(alertReasonNoTick)
	}
	ctx, cancel := context.WithTimeout(ctx, alertVMAPITimeout)
	defer cancel()
	st, _ := r.state(ctx, r.clock().Add(-vmIngestLag))
	_, src := st.decide(service, metric, windowSec)
	return src
}

// applyAlertSeries — SAF: tek servisin bir tür okumasını kova örneklerine
// yazar (nokta zamanı → kova başı, unix sn).
func applyAlertSeries(get func(int64) *alertVMSample, k alertVMKind, ser []chstore.SpanMetricSeries, svcLabel, service string) {
	for _, sr := range ser {
		if svc, ok := alertVMLabel(sr.GroupKey, svcLabel); !ok || svc != service {
			continue
		}
		phi, _ := alertVMLabel(sr.GroupKey, "phi")
		for _, pt := range sr.Points {
			if math.IsNaN(pt.Value) || math.IsInf(pt.Value, 0) {
				continue
			}
			s := get(pt.Time / int64(time.Second))
			switch k {
			case akCount:
				s.Count, s.HaveCount = pt.Value, true
			case akErr:
				s.Err, s.HaveErr = pt.Value, true
			case akSum:
				s.Sum, s.HaveSum = pt.Value, true
			case akQuant:
				if s.Quant == nil {
					s.Quant = map[string]float64{}
				}
				s.Quant[phi] = pt.Value
			}
		}
	}
}

// singleSamples — tek servisin türlerini okur: step > 0 → aralık (kova başı →
// örnek), step = 0 → `to` anında anlık (tek örnek, anahtar `to`). Hata veren
// tür geri çekilmeye girer (evaluator da o türü span'e düşürür — parite).
func (r *AlertVMReader) singleSamples(ctx context.Context, st *alertVMState, fi int, service string, kinds []alertVMKind,
	windowSec int, from, to time.Time, step int, phis []string) (map[int64]*alertVMSample, error) {
	f := vmREDFamilies[fi]
	sc := st.meta[fi].scope
	svcM := vmetrics.EnvMatcher(sc.SvcLabel, []string{service})
	out := map[int64]*alertVMSample{}
	get := func(b int64) *alertVMSample {
		s := out[b]
		if s == nil {
			s = &alertVMSample{Scale: alertVMScale(f), WindowSec: windowSec}
			out[b] = s
		}
		return s
	}
	for _, k := range kinds {
		q, ok := alertVMExpr(f, k, sc, windowSec, svcM, phis)
		if !ok {
			return nil, fmt.Errorf("%s ölçülemez", k)
		}
		var ser []chstore.SpanMetricSeries
		var err error
		if step > 0 {
			ser, err = st.vm.QueryPromQLRange(ctx, q, from, to, step, int(to.Sub(from).Seconds())/step+2)
		} else {
			ser, err = st.vm.QueryPromQLInstant(ctx, q, to)
		}
		if err != nil {
			// İstemci iptali / API zaman aşımı VM'in hatası değil: evaluator'ı
			// span'e düşüren geri çekilme yalnız VM'in kendi hatasında.
			if ctx.Err() == nil && r.lock(ctx) {
				r.backoff[[2]int{fi, int(k)}] = st.vnow.Add(vmKindBackoff)
				r.unlock()
			}
			return nil, err
		}
		applyAlertSeries(get, k, ser, sc.SvcLabel, service)
	}
	return out, nil
}

// Series — kural önizlemesi / problem grafiği: değerlendiricinin AYNI kaynağı
// ve AYNI penceresiyle kova kova (adım stepSec). used=false → kaynak span
// (src gerekçeli), çağıran span dizisini çizer. Kova [b, b+step) noktası =
// b+step anındaki pencere değeri (span dizisinin T'si); VM gecikmesinin
// (vmIngestLag) ötesi çizilmez.
func (r *AlertVMReader) Series(ctx context.Context, service, metric string, windowSec int, from, to time.Time, stepSec int) (pts []chstore.AlertMetricPoint, src AlertSource, used bool) {
	if r == nil {
		return nil, SpanAlertSource(alertReasonNoTick), false
	}
	ctx, cancel := context.WithTimeout(ctx, alertVMAPITimeout)
	defer cancel()
	vnow := r.clock().Add(-vmIngestLag)
	st, _ := r.state(ctx, vnow)
	fi, src := st.decide(service, metric, windowSec)
	if !src.VM || stepSec <= 0 {
		return nil, src, false
	}
	end := to
	if end.After(vnow) {
		end = vnow
	}
	if !end.After(from) {
		return []chstore.AlertMetricPoint{}, src, true
	}
	kinds, _ := alertVMMetricKinds(metric)
	samples, err := r.singleSamples(ctx, st, fi, service, kinds, windowSec, from, end, stepSec, nil)
	if err != nil {
		return nil, SpanAlertSource(redReasonVMError + ": " + err.Error()), false
	}
	step := int64(stepSec)
	pts = make([]chstore.AlertMetricPoint, 0, (end.Unix()-from.Unix())/step+1)
	for b := from.Unix(); b+step <= end.Unix(); b += step {
		pt := chstore.AlertMetricPoint{T: (b + step) * int64(time.Second)}
		s := samples[b]
		if s == nil {
			s = &alertVMSample{Scale: alertVMScale(vmREDFamilies[fi]), WindowSec: windowSec}
		}
		if v, ok := alertVMValue(metric, *s); ok {
			pt.V = &v
		}
		pts = append(pts, pt)
	}
	return pts, src, true
}

// Baseline — taban önerisi (son 7 gün) değerlendiricinin kaynağından. Oran /
// sayı metrikleri: 5 dk kovaların dağılımı (span tabanıyla aynı kova); gecikme:
// 7 günlük histogramın yüzdelikleri (span: span-düzeyi t-digest). used=false →
// span tabanı (src gerekçeli). Servis boşsa (genel taban) span.
func (r *AlertVMReader) Baseline(ctx context.Context, service, metric string, windowSec int) (b *chstore.MetricBaseline, src AlertSource, used bool) {
	if r == nil {
		return nil, SpanAlertSource(alertReasonNoTick), false
	}
	if service == "" {
		return nil, SpanAlertSource("servis seçilmedi — genel taban span'den (kural servis başına değerlendirilir)"), false
	}
	ctx, cancel := context.WithTimeout(ctx, alertVMAPITimeout)
	defer cancel()
	vnow := r.clock().Add(-vmIngestLag)
	st, _ := r.state(ctx, vnow)
	if windowSec <= 0 {
		windowSec = alertVMBaselineWindowSec
	}
	fi, src := st.decide(service, metric, windowSec) // kuralın penceresiyle — evaluator'ın kararı
	if !src.VM {
		return nil, src, false
	}
	out := &chstore.MetricBaseline{Metric: metric, Service: service, WindowSec: int64(alertVMBaselineLookback / time.Second)}
	if alertVMPhi(metric) != "" || metric == "avg_ms" {
		// Tek anlık okuma: 7 günlük pencerenin histogramı (count + sum + yüzdelikler).
		win := int(alertVMBaselineLookback / time.Second)
		samples, err := r.singleSamples(ctx, st, fi, service, []alertVMKind{akCount, akSum, akQuant}, win, time.Time{}, vnow, 0,
			[]string{"0.5", "0.95", "0.99", "1"})
		if err != nil {
			return nil, SpanAlertSource(redReasonVMError + ": " + err.Error()), false
		}
		if s := samples[vnow.Unix()]; s != nil && s.HaveCount && s.Count > 0 {
			scale := alertVMScale(vmREDFamilies[fi])
			q := func(phi string) float64 {
				v, ok := s.Quant[phi]
				if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
					return 0
				}
				return v * scale
			}
			out.P50, out.P95, out.P99, out.Max = q("0.5"), q("0.95"), q("0.99"), q("1")
			if out.Max < out.P99 {
				out.Max = out.P99
			}
			if s.HaveSum {
				out.Mean = s.Sum / s.Count * scale
			}
			out.SampleCount = int64(math.Round(s.Count))
		}
		return out, src, true
	}
	step := alertVMBaselineWindowSec
	from := vnow.Add(-alertVMBaselineLookback)
	kinds, _ := alertVMMetricKinds(metric)
	samples, err := r.singleSamples(ctx, st, fi, service, kinds, step, from, vnow, step, nil)
	if err != nil {
		return nil, SpanAlertSource(redReasonVMError + ": " + err.Error()), false
	}
	vals := make([]float64, 0, len(samples))
	for _, s := range samples {
		if !s.HaveCount || s.Count <= 0 {
			continue // span tabanı da GROUP BY'da istek olmayan kovayı görmez
		}
		if v, ok := alertVMValue(metric, *s); ok {
			vals = append(vals, v)
		}
	}
	sort.Float64s(vals)
	out.BucketSec = int64(step)
	out.SampleCount = int64(len(vals))
	if len(vals) > 0 {
		q := alertVMQuantiles(vals, 0.5, 0.95, 0.99)
		out.P50, out.P95, out.P99, out.Max = q[0], q[1], q[2], vals[len(vals)-1]
		total := 0.0
		for _, v := range vals {
			total += v
		}
		out.Mean = total / float64(len(vals))
	}
	return out, src, true
}
