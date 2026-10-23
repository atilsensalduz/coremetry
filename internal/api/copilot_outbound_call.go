package api

// copilot_outbound_call.go — v0.10.1454: CoSRE guided "giden çağrı /
// outbound_call" niyeti, R1 (yol tetikli). SAF yarı: yuva, rota, pencere,
// hüküm, metin, grafik/tablo yükleri, çip ve linkler. Sunucu yarısı
// copilot_outbound_call_bundle.go; okuyucu chstore/outbound_calls.go.
//
// Operator-reported: servis sayfasında "**/esb/external/…/customer-kvkk/**
// kısmı için grafikleri ortalamayı gösterebilirsin" → 32,8 sn, 75K token,
// "veri bulunamadı". Yol bir CLIENT span'inin url.path'i (ESB'ye giden çağrı),
// katalogda yok; v0.10.1373'ün yol-yalnız geri dönüşü RED ölçmüyor ve paketi
// serbest döngüye tohum veriyordu, döngüde client-span grafiği üretecek araç
// yok. Kademe boşluğu, model hatası değil (plan-outbound-call.md §0).
//
// Akış: ön-getirmenin pathOnly kapanışı (chat_operation_prefetch.go) yol
// katalogda yokken dış-yol sondası koşar; HIT → ctx yuvasına (outboundSlot)
// yazar ve ön-getirme nil döner → guided kademesi yuvayı görür, rota
// outbound_call → LLM'siz deterministik cevap (operatör kararı S1). Sonda
// MISS / hata → bugünkü yol-yalnız hedef BAYT BAYT.
//
// Yuva neden ctx: chatOperationPrefetch imzası ve TestOpPrefetch* dokunulmaz;
// copilot_chat.go'ya tek satır (armDisambigRescue emsali), kademe işaretleri
// (TestChatTierOrderIsPinned) aynen.

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// guidedOutboundCall — router-içi niyet (intentAllowed DIŞI; metric_chart /
// topology emsali — sınıflandırıcı prompt'u değişmez).
const guidedOutboundCall guidedIntent = "outbound_call"

const (
	outboundDefaultRangeS = 3600          // açık pencere yoksa son 1 sa
	outboundMaxWindow     = 6 * time.Hour // ham okuma tavanı (clampExternalPathsWindow)
	outboundScanWindow    = time.Hour     // >outboundManyCallers çağıranda pencere
	outboundManyCallers   = 5             // bundan çok çağıran → 1 sa
	outboundWeek          = 7 * 24 * time.Hour
	outboundMinCalls      = 20   // hüküm için asgari çağrı
	outboundRiseRatio     = 1.2  // ARTIŞ: ≥ ×1,2 …
	outboundRiseMinMs     = 50.0 // … ve ≥ 50 ms fark
	outboundErrPts        = 2.0  // HATA ARTIŞI: ≥ +2 puan
	outboundTableMax      = 10
	outboundChartMaxPts   = 120
	outboundPerCallerMax  = 3 // ≤3 çağıran → çağıran başına seri
	outboundCacheTTL      = 60 * time.Second
	// outboundSourceTR — operatör kararı: kaynak başlıkta açıkça (VM'de dış
	// çağrı metriği yok; span sayımı prod örneklemesiyle gerçek trafiğin altında).
	outboundSourceTR = "trace (örneklenmiş)"
)

// ─── ctx yuvası ────────────────────────────────────────────────────────

// outboundHit — sonda HIT'i: guided rotasının girdisi.
type outboundHit struct {
	Path    string   // anılan (temizlenmiş) yol
	Callers []string // ölçülecek çağıranlar (≤100)
	Peer    string   // tek host ise; "" = tüm hostlar
	Service string   // odak servis (tek çağıran / ekran)
	Env     string   // tek ortam süzgeci
	Scan    bool     // çağıran belirsizdi: ≤100 dış çağıran tarandı (beyan)
	Src     string   // "mv" | "raw"
	Envs    []string // v0.10.1455 — ≥2 ortam anıldı: ortam başına kırılım
	ProbeS  int64    // v0.10.1455 — MV sondasının penceresi (sn); ham sonda 0
}

// outboundSlot — copilotChat'in ön-getirmeden ÖNCE kurduğu tutucu.
type outboundSlot struct {
	mu      sync.Mutex
	service string // ekran servisi
	env     string // ekran ortamı
	rangeS  int64
	anchor  time.Time
	hit     *outboundHit
}

type outboundSlotKey struct{}

// armOutboundSlot — v0.10.1454: copilot_chat.go'daki TEK satır.
func armOutboundSlot(ctx context.Context, service, env string, rangeS int64, anchor time.Time) context.Context {
	return context.WithValue(ctx, outboundSlotKey{}, &outboundSlot{service: strings.TrimSpace(service), env: strings.TrimSpace(env), rangeS: rangeS, anchor: anchor})
}

func outboundSlotFrom(ctx context.Context) *outboundSlot {
	if ctx == nil {
		return nil
	}
	o, _ := ctx.Value(outboundSlotKey{}).(*outboundSlot)
	return o
}

func (o *outboundSlot) set(h outboundHit) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.hit = &h
	o.mu.Unlock()
}

// get — nil-güvenli; HIT yoksa nil.
func (o *outboundSlot) get() *outboundHit {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.hit
}

// route — SAF: HIT → guided rotası.
func (h outboundHit) route() guidedRoute {
	return guidedRoute{Intent: guidedOutboundCall, Service: h.Service, Env: h.Env,
		OutboundPath: h.Path, OutboundPeer: h.Peer, OutboundCallers: append([]string(nil), h.Callers...),
		OutboundScan: h.Scan, OutboundSrc: h.Src, EnvSet: append([]string(nil), h.Envs...), OutboundProbeS: h.ProbeS}
}

// ─── sonda kararı (SAF) ───────────────────────────────────────────────

// outboundCallersFrom — SAF: sonda satırlarından ölçülecek çağıranlar + tek
// host. svcHint satırlarda varsa yalnız o (operatörün baktığı servis); host
// yalnız TEK ise süzgeç olur (birden çok → "" tüm hostlar, tablo kırar).
func outboundCallersFrom(rows []chstore.OutboundCaller, svcHint string) (callers []string, peer string) {
	if svcHint != "" {
		var only []chstore.OutboundCaller
		for _, r := range rows {
			if r.Service == svcHint {
				only = append(only, r)
			}
		}
		if len(only) > 0 {
			rows = only
		}
	}
	seen, hosts := map[string]bool{}, map[string]bool{}
	for _, r := range rows {
		if r.Service != "" && !seen[r.Service] && len(callers) < chstore.OutboundCallerScanMax {
			seen[r.Service] = true
			callers = append(callers, r.Service)
		}
		if r.Host != "" {
			hosts[r.Host] = true
		}
	}
	if len(hosts) == 1 {
		for h := range hosts {
			peer = h
		}
	}
	return callers, peer
}

// outboundWindowFor — SAF: ölçüm penceresi (sona yaslı). ≤6 sa; çok çağıranda
// (>5) ≤1 sa. note kırpmayı söyler ("" = kırpılmadı).
func outboundWindowFor(from, to time.Time, nCallers int) (time.Time, time.Time, string) {
	max, why := outboundMaxWindow, "ham span okuması ≤6 sa"
	if nCallers > outboundManyCallers {
		max, why = outboundScanWindow, fmt.Sprintf("%d çağıran tarandığı için ≤1 sa", nCallers)
	}
	if to.Sub(from) > max {
		return to.Add(-max), to, fmt.Sprintf("pencere %s'e kırpıldı (%s)", strings.TrimPrefix(fmtRangeTR(int64(max/time.Second)), "son "), why)
	}
	return from, to, ""
}

// outboundStepS — SAF: grafik adımı (≥60 sn, ≤120 nokta; metric_chart D3).
func outboundStepS(from, to time.Time) int64 {
	rs := int64(to.Sub(from) / time.Second)
	step := rs / outboundChartMaxPts
	if rs%outboundChartMaxPts != 0 {
		step++
	}
	if step < 60 {
		step = 60
	}
	return step
}

// ─── ölçüm + hüküm (SAF) ──────────────────────────────────────────────

// outboundStat — bir pencerenin toplamı.
type outboundStat struct {
	Calls, Errors uint64
	AvgMs, P95Ms  float64
	P95Exact      bool // tek grup → p95 kesin; çok grupta p95 satır başına
	Groups        int
}

func (s outboundStat) errPct() float64 {
	if s.Calls == 0 {
		return 0
	}
	return float64(s.Errors) * 100 / float64(s.Calls)
}

func (s outboundStat) measured() bool { return s.Calls >= outboundMinCalls }

// outboundTotal — SAF: win penceresinin satırlarının toplamı (ort çağrı
// ağırlıklı; p95 yalnız tek grupta kesin — yüzdelik toplanmaz).
func outboundTotal(rows []chstore.OutboundAgg, win string) outboundStat {
	var st outboundStat
	var sum float64
	for _, r := range rows {
		if r.Win != win {
			continue
		}
		st.Groups++
		st.Calls += r.Calls
		st.Errors += r.Errors
		sum += r.AvgMs * float64(r.Calls)
		st.P95Ms = r.P95Ms
	}
	if st.Calls > 0 {
		st.AvgMs = sum / float64(st.Calls)
	}
	st.P95Exact = st.Groups == 1
	if !st.P95Exact {
		st.P95Ms = 0
	}
	return st
}

// outboundVerdict kinds.
const (
	obVerdictInsufficient = "insufficient"
	obVerdictNoRef        = "no_ref"
	obVerdictRise         = "rise"
	obVerdictErrRise      = "err_rise"
	obVerdictBoth         = "rise_err"
	obVerdictFlat         = "flat"
)

// outboundVerdict — SAF: deterministik hüküm. Referans = ölçülen (≥20 çağrı)
// önceki pencere ve geçen hafta aynı pencerenin EN YÜKSEĞİ (temkinli: iki
// referanstan da kötüyse artış). ARTIŞ: ort (ya da iki tarafta da kesin p95)
// ≥ ×1,2 VE ≥ 50 ms fark; HATA ARTIŞI: hata% ≥ referans + 2 puan.
func outboundVerdict(cur, prev, week outboundStat) (kind string, refAvg float64) {
	if !cur.measured() {
		return obVerdictInsufficient, 0
	}
	var refs []outboundStat
	for _, r := range []outboundStat{prev, week} {
		if r.measured() {
			refs = append(refs, r)
		}
	}
	if len(refs) == 0 {
		return obVerdictNoRef, 0
	}
	var refP95, refErr float64
	p95ok := cur.P95Exact
	for _, r := range refs {
		refAvg = maxF(refAvg, r.AvgMs)
		refErr = maxF(refErr, r.errPct())
		if r.P95Exact {
			refP95 = maxF(refP95, r.P95Ms)
		} else {
			p95ok = false
		}
	}
	rise := cur.AvgMs >= outboundRiseRatio*refAvg && cur.AvgMs-refAvg >= outboundRiseMinMs
	if p95ok && refP95 > 0 && cur.P95Ms >= outboundRiseRatio*refP95 && cur.P95Ms-refP95 >= outboundRiseMinMs {
		rise = true
	}
	errRise := cur.errPct() >= refErr+outboundErrPts
	switch {
	case rise && errRise:
		return obVerdictBoth, refAvg
	case rise:
		return obVerdictRise, refAvg
	case errRise:
		return obVerdictErrRise, refAvg
	}
	return obVerdictFlat, refAvg
}

func maxF(a, b float64) float64 {
	if b > a {
		return b
	}
	return a
}

// fmtMsTR — SAF: "812 ms" / "1.240 ms" / "3,4 ms".
func fmtMsTR(v float64) string {
	if v >= 100 {
		return fmtCountTR(uint64(v+0.5)) + " ms"
	}
	return trNum(v) + " ms"
}

// outboundRender — metin girdisi.
type outboundRender struct {
	Path, Peer string
	Callers    []string
	RangeS     int64
	Env        string
	Cur        outboundStat
	Prev, Week outboundStat
	PathGroups int
	Notes      []string
	Scan       bool
	ReadErr    error // ana ölçüm okunamadı
	WeekErr    error
	Clamped    string
	Envs       []string // v0.10.1455 — ≥2 ortam: ortam başına kırılım
	EnvLines   string   // v0.10.1455 — "KÜME KIYASI" satırları (outboundEnvLinesTR)
	Source     string   // "" = outboundSourceTR
	AllEnvs    bool     // v0.10.1468 — ekran ortamı uygulanmadı (MV, tüm ortamlar)
}

// outboundSubjectTR — SAF: "orders-api → esbprod.example.internal · /yol".
func outboundSubjectTR(callers []string, peer, path string) string {
	who := strings.Join(callers, ", ")
	switch {
	case len(callers) == 0:
		who = "dış çağıranlar"
	case len(callers) > 3:
		who = fmt.Sprintf("%s +%d servis", strings.Join(callers[:3], ", "), len(callers)-3)
	}
	host := peer
	if host == "" {
		host = "dış hostlar"
	}
	return who + " → " + host + " · " + path
}

// renderOutboundTR — SAF: deterministik Türkçe cevap; ilk cümle düz.
func renderOutboundTR(in outboundRender) string {
	var b strings.Builder
	subj := outboundSubjectTR(in.Callers, in.Peer, in.Path)
	win := fmtRangeTR(in.RangeS)
	if in.ReadErr != nil {
		fmt.Fprintf(&b, "%s (%s): giden çağrılar OKUNAMADI (okunamadı ≠ çağrı yok): %v. Traces sayfasından aynı yolu arayabilirsin.", subj, win, in.ReadErr)
		return b.String()
	}
	envNote := ""
	switch {
	case len(in.Envs) >= 2:
		envNote = ", ortamlar " + strings.Join(in.Envs, ", ")
	case in.Env != "":
		envNote = ", ortam " + in.Env
	case in.AllEnvs:
		envNote = ", tüm ortamlar (MV)"
	}
	src := in.Source
	if src == "" {
		src = outboundSourceTR
	}
	if in.Cur.Calls == 0 {
		where := "bu yola"
		if in.Path == "(tüm yollar)" { // v0.10.1455 — yolsuz host özeti
			where = "bu hosta"
		}
		fmt.Fprintf(&b, "%s (%s%s, kaynak: %s): bu pencerede %s giden client span YOK (boş küme = sıfır çağrı değil, örneklenmiş trace'te görünmedi).", subj, win, envNote, src, where)
	} else {
		fmt.Fprintf(&b, "%s (%s%s, kaynak: %s): %s çağrı, ort %s", subj, win, envNote, src, fmtCountTR(in.Cur.Calls), fmtMsTR(in.Cur.AvgMs))
		var refs []string
		if in.Prev.measured() {
			refs = append(refs, "önceki pencere "+fmtMsTR(in.Prev.AvgMs))
		}
		if in.Week.measured() {
			refs = append(refs, "geçen hafta "+fmtMsTR(in.Week.AvgMs))
		}
		kind, refAvg := outboundVerdict(in.Cur, in.Prev, in.Week)
		if len(refs) > 0 {
			ratio := ""
			if refAvg > 0 {
				ratio = " → ×" + trNum(in.Cur.AvgMs/refAvg)
			}
			b.WriteString(" (" + strings.Join(refs, ", ") + ratio + ")")
		}
		if in.Cur.P95Exact && in.Cur.P95Ms > 0 {
			b.WriteString(", p95 " + fmtMsTR(in.Cur.P95Ms))
		}
		fmt.Fprintf(&b, ", hata %%%s", trNum(in.Cur.errPct()))
		if in.Prev.measured() {
			fmt.Fprintf(&b, " (önceki %%%s)", trNum(in.Prev.errPct()))
		}
		b.WriteString(". ")
		switch kind {
		case obVerdictInsufficient:
			fmt.Fprintf(&b, "Yetersiz veri (%d çağrı < %d) — hüküm yok.", in.Cur.Calls, outboundMinCalls)
		case obVerdictNoRef:
			b.WriteString("Kıyas yok: önceki pencere ve geçen hafta yeterli çağrı taşımıyor — hüküm yok.")
		case obVerdictBoth:
			b.WriteString("**ARTIŞ VAR** (gecikme ve hata oranı).")
		case obVerdictRise:
			b.WriteString("**ARTIŞ VAR** (gecikme).")
		case obVerdictErrRise:
			b.WriteString("**HATA ARTIŞI VAR**.")
		default:
			b.WriteString("Belirgin fark yok (±%20 / +2 puan eşiği altında).")
		}
	}
	if in.EnvLines != "" {
		b.WriteString("\n" + in.EnvLines)
	}
	// v0.10.1482 — sarmalayıcısız "/x/**": LIKE '/x/%' zaten önekin altındaki her
	// yolu topluyor; cevap bunun bir ÖNEK TOPLAMI olduğunu söyler.
	if p, ok := pathPrefixOf(in.Path); ok && in.Cur.Calls > 0 {
		fmt.Fprintf(&b, "\nÖnek toplamı: %s altındaki tüm yollar (%d yol); çağrı, hata %% ve ortalama toplamdır, p95 yalnız yol başına (yüzdelikler toplanmaz).", p, in.PathGroups)
	}
	if in.Cur.Groups > 1 || in.PathGroups > 1 {
		fmt.Fprintf(&b, "\n%d çağıran·host·yol grubu eşleşti; ayrıntı tabloda (p95 grup başına).", in.Cur.Groups)
	}
	if in.Scan {
		fmt.Fprintf(&b, "\nÇağıran servis belirsizdi: dış host çağıran ≤%d servis son 1 saatte tarandı.", chstore.OutboundCallerScanMax)
	}
	if in.Clamped != "" {
		b.WriteString("\nNot: " + in.Clamped + ".")
	}
	if in.WeekErr != nil {
		b.WriteString("\nNot: geçen hafta kıyası okunamadı.")
	}
	for _, n := range in.Notes {
		b.WriteString("\nNot: " + n + ".")
	}
	b.WriteString("\nDashboard'a ekle: bu kanıt ClickHouse SQL (client span); panel çevirisi yok.")
	return b.String()
}

// ─── grafik + tablo yükleri (SAF) ─────────────────────────────────────

// outboundChartTitle — SAF: kaynak başlıkta açık (operatör kararı).
func outboundChartTitle(callers []string, peer, path string) string {
	return outboundSubjectTR(callers, peer, path) + " · gecikme / hata · " + outboundSourceTR
}

// outboundChartPayload — SAF: ort ms + p95 ms (secondary) + hata %; Group =
// çağıran (perCaller). Nokta [unixMs, değer]; ≤120 kova sunucu adımından.
func outboundChartPayload(points []chstore.OutboundPoint, title, href string, from, to time.Time) *mcptools.EvChartPayload {
	return outboundChartPayloadP(points, title, href, "p95 ms", from, to)
}

// outboundChartPayloadP — v0.10.1455: yüzdelik serinin adı parametre (MV
// trendi p99 kova maksimumu taşır: "p99 ms").
func outboundChartPayloadP(points []chstore.OutboundPoint, title, href, pctName string, from, to time.Time) *mcptools.EvChartPayload {
	type key struct{ grp, name string }
	idx := map[key]int{}
	var series []mcptools.EvChartSeries
	add := func(grp, name, unit, style string, t int64, v float64) {
		k := key{grp, name}
		i, ok := idx[k]
		if !ok {
			i = len(series)
			idx[k] = i
			series = append(series, mcptools.EvChartSeries{Name: name, Group: grp, Unit: unit, Style: style})
		}
		series[i].Points = append(series[i].Points, [2]float64{float64(t), v})
	}
	sorted := append([]chstore.OutboundPoint(nil), points...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Bucket < sorted[j].Bucket })
	for _, p := range sorted {
		if p.Calls == 0 {
			continue
		}
		ms := p.Bucket * 1000
		add(p.Service, "ort ms", "ms", "", ms, p.AvgMs)
		add(p.Service, pctName, "ms", "secondary", ms, p.P95Ms)
		add(p.Service, "hata %", "%", "", ms, float64(p.Errors)*100/float64(p.Calls))
	}
	if len(series) == 0 {
		return nil
	}
	return &mcptools.EvChartPayload{Title: title, DeepLink: href,
		Window: mcptools.EvBlockWindow{FromNs: from.UnixNano(), ToNs: to.UnixNano()}, Series: series}
}

// outboundTableColumns — tablo sütunları (trace hücreleri FE'de /trace linki).
var outboundTableColumns = []mcptools.EvTableColumn{
	{Key: "caller", Label: "Çağıran", Kind: "text"},
	{Key: "host", Label: "Host", Kind: "text"},
	{Key: "path", Label: "Yol", Kind: "text"},
	{Key: "calls", Label: "Çağrı", Kind: "num"},
	{Key: "err", Label: "Hata %", Kind: "num"},
	{Key: "avg", Label: "Ort ms", Kind: "num"},
	{Key: "p95", Label: "p95 ms", Kind: "num"},
	{Key: "dprev", Label: "Δ önceki %", Kind: "num"},
	{Key: "dweek", Label: "Δ geçen hafta %", Kind: "num"},
	{Key: "slow", Label: "En yavaş", Kind: "trace"},
	{Key: "errt", Label: "Hatalı", Kind: "trace"},
}

// outboundTableRows — SAF: cur satırları (çağrı sırası, ≤10) + önceki /
// geçen hafta ort farkı (%; referansta <20 çağrı → boş).
func outboundTableRows(rows []chstore.OutboundAgg, week []chstore.OutboundAgg) (out [][]any, truncated bool) {
	return outboundTableRowsX(rows, week, false, true)
}

// outboundTableColumnsFor — v0.10.1455: byEnv → başta "Ortam"; pct yüzdelik
// sütun adı (MV: p99); traces=false → örnek trace sütunları yok (MV taşımaz).
func outboundTableColumnsFor(byEnv bool, pct string, traces bool) []mcptools.EvTableColumn {
	var cols []mcptools.EvTableColumn
	if byEnv {
		cols = append(cols, mcptools.EvTableColumn{Key: "env", Label: "Ortam", Kind: "text"})
	}
	for _, c := range outboundTableColumns {
		if !traces && c.Kind == "trace" {
			continue
		}
		if c.Key == "p95" {
			c.Label = pct + " ms"
		}
		cols = append(cols, c)
	}
	return cols
}

// outboundTableRowsX — SAF: outboundTableRows + ortam sütunu / trace'siz biçim.
// Δ anahtarı ortamı da taşır (ortam başına önceki / geçen hafta).
func outboundTableRowsX(rows []chstore.OutboundAgg, week []chstore.OutboundAgg, byEnv, traces bool) (out [][]any, truncated bool) {
	type k struct{ e, s, p, path string }
	prev, wk := map[k]chstore.OutboundAgg{}, map[k]chstore.OutboundAgg{}
	var cur []chstore.OutboundAgg
	for _, r := range rows {
		switch r.Win {
		case "prev":
			prev[k{r.Env, r.Service, r.Peer, r.Path}] = r
		case "cur":
			cur = append(cur, r)
		}
	}
	for _, r := range week {
		wk[k{r.Env, r.Service, r.Peer, r.Path}] = r
	}
	sort.SliceStable(cur, func(i, j int) bool { return cur[i].Calls > cur[j].Calls })
	if len(cur) > outboundTableMax {
		cur, truncated = cur[:outboundTableMax], true
	}
	delta := func(m map[k]chstore.OutboundAgg, r chstore.OutboundAgg) any {
		ref, ok := m[k{r.Env, r.Service, r.Peer, r.Path}]
		if !ok || ref.Calls < outboundMinCalls || ref.AvgMs <= 0 {
			return nil
		}
		return round1((r.AvgMs - ref.AvgMs) * 100 / ref.AvgMs)
	}
	for _, r := range cur {
		errPct := 0.0
		if r.Calls > 0 {
			errPct = float64(r.Errors) * 100 / float64(r.Calls)
		}
		row := []any{r.Service, r.Peer, r.Path, r.Calls, round1(errPct), round1(r.AvgMs), round1(r.P95Ms),
			delta(prev, r), delta(wk, r)}
		if traces {
			row = append(row, r.SlowTrace, r.ErrTrace)
		}
		if byEnv {
			row = append([]any{r.Env}, row...)
		}
		out = append(out, row)
	}
	return out, truncated
}

// ─── v0.10.1455 (R2) — ortam kıyası, iş kodları, host sorusu (SAF) ────

// outboundEnvLinesTR — SAF: ≥2 ortamda ortam başına hüküm satırları
// ("KÜME KIYASI"); her ortam kendi önceki penceresi / geçen haftasıyla.
func outboundEnvLinesTR(rows, week []chstore.OutboundAgg) string {
	envs := map[string]bool{}
	for _, r := range rows {
		if r.Win == "cur" {
			envs[r.Env] = true
		}
	}
	if len(envs) == 0 {
		return ""
	}
	names := make([]string, 0, len(envs))
	for e := range envs {
		names = append(names, e)
	}
	sort.Strings(names)
	only := func(src []chstore.OutboundAgg, env string) []chstore.OutboundAgg {
		var out []chstore.OutboundAgg
		for _, r := range src {
			if r.Env == env {
				out = append(out, r)
			}
		}
		return out
	}
	var b strings.Builder
	b.WriteString("KÜME KIYASI (ortam başına):")
	for _, e := range names {
		er, ew := only(rows, e), only(week, e)
		cur, prev, wk := outboundTotal(er, "cur"), outboundTotal(er, "prev"), outboundTotal(ew, "cur")
		label := e
		if label == "" {
			label = "(ortamsız)"
		}
		fmt.Fprintf(&b, "\n- %s: %s çağrı, ort %s", label, fmtCountTR(cur.Calls), fmtMsTR(cur.AvgMs))
		if prev.measured() {
			fmt.Fprintf(&b, " (önceki %s)", fmtMsTR(prev.AvgMs))
		}
		kind, _ := outboundVerdict(cur, prev, wk)
		fmt.Fprintf(&b, ", hata %%%s — %s", trNum(cur.errPct()), outboundVerdictLabelTR(kind))
	}
	return b.String()
}

// outboundCodeColumns — iş kodu tablosu (function_code / channel_code top-5).
var outboundCodeColumns = []mcptools.EvTableColumn{
	{Key: "kind", Label: "Tür", Kind: "text"},
	{Key: "code", Label: "Kod", Kind: "text"},
	{Key: "calls", Label: "Çağrı", Kind: "num"},
	{Key: "err", Label: "Hata %", Kind: "num"},
	{Key: "avg", Label: "Ort ms", Kind: "num"},
}

// outboundCodeRows — SAF: boş kod elenir; tür sırası function_code önce.
func outboundCodeRows(codes []chstore.OutboundCode) [][]any {
	sorted := append([]chstore.OutboundCode(nil), codes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind > sorted[j].Kind // function_code > channel_code
		}
		return sorted[i].Calls > sorted[j].Calls
	})
	var out [][]any
	for _, c := range sorted {
		if strings.TrimSpace(c.Code) == "" {
			continue
		}
		errPct := 0.0
		if c.Calls > 0 {
			errPct = float64(c.Errors) * 100 / float64(c.Calls)
		}
		out = append(out, []any{c.Kind, c.Code, c.Calls, round1(errPct), round1(c.AvgMs)})
	}
	return out
}

// fuKindOutboundHost — "hangi dış host?" çipi (mesaj gönderir, router'a yönlenir).
const fuKindOutboundHost = "outbound_host"

// outboundHostChipText — SAF: çip metni yeniden AYNI rotaya çözülür: "dış
// host" ikilisi (ön-getirme vetosu + hedef) + tam host adı (canlı eşleşme) +
// "çağrı süreleri" (ölçü ipucu); servis / ortam taşınır.
func outboundHostChipText(route guidedRoute, host string) string {
	t := "dış host " + host + " çağrı süreleri"
	if route.Service != "" {
		t = route.Service + " " + t
	}
	if route.Env != "" {
		t += " " + route.Env
	}
	return t
}

// outboundAskHostTR — SAF: host tek adaya inmedi — tahmin yok, adaylar.
func outboundAskHostTR(route guidedRoute) string {
	term := route.OutboundTerm
	who := ""
	if route.Service != "" {
		who = " (" + route.Service + " çağırıyor)"
	}
	list := strings.Join(route.OutboundOptions, ", ")
	switch {
	case len(route.OutboundOptions) == 0:
		return "Dış host kataloğu boş ya da okunamadı (son 24 saat, topology_edges_5m) — giden çağrı için host çözülemedi. External sayfasından bakabilirsin."
	case route.OutboundMiss:
		return fmt.Sprintf("'%s' hiçbir canlı dış host adında geçmiyor (son 24 saat). En yoğun dış hostlar%s: %s. Hangisine bakayım?", term, who, list)
	case term != "":
		return fmt.Sprintf("'%s' birden çok dış hostla eşleşti%s: %s. Hangisine bakayım? (tahmin etmiyorum)", term, who, list)
	}
	return fmt.Sprintf("Hangi dış host? En yoğun dış hostlar%s: %s.", who, list)
}

// outboundAskHostFollowups — SAF: aday başına çip (≤guidedFollowupMax).
func outboundAskHostFollowups(route guidedRoute, asked []string) []chatFollowup {
	var cands []chatFollowup
	for _, h := range route.OutboundOptions {
		cands = append(cands, chatFollowup{Text: outboundHostChipText(route, h), Label: h, Kind: fuKindOutboundHost})
	}
	return dedupFollowupsAsked(cands, asked, guidedFollowupMax)
}

func round1(v float64) float64 {
	if v < 0 {
		return -round1(-v)
	}
	return float64(int64(v*10+0.5)) / 10
}

// outboundSampleTraces — SAF: link için ≤3 örnek trace (en yavaş önce, sonra hatalı).
func outboundSampleTraces(rows []chstore.OutboundAgg) []string {
	var out []string
	seen := map[string]bool{}
	push := func(id string) {
		id = strings.ToLower(strings.TrimSpace(id))
		if fuTraceRe.MatchString(id) && !seen[id] && len(out) < 3 {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, r := range rows {
		if r.Win == "cur" {
			push(r.SlowTrace)
		}
	}
	for _, r := range rows {
		if r.Win == "cur" {
			push(r.ErrTrace)
		}
	}
	return out
}

// ─── link + çip (SAF) ─────────────────────────────────────────────────

// outboundTracesHref — SAF: /traces serbest arama (yol parçaları; opTracesHref deseni).
func outboundTracesHref(route guidedRoute, errorsOnly bool) string {
	q := url.Values{}
	if route.OutboundPath != "" {
		q.Set("search", pathSearchText(route.OutboundPath))
	} else {
		q.Set("search", route.OutboundPeer) // v0.10.1455 — yolsuz: host adı
	}
	if len(route.OutboundCallers) == 1 {
		q.Set("service", route.OutboundCallers[0])
	}
	if errorsOnly {
		q.Set("hasError", "true")
	}
	return "/traces?" + q.Encode()
}

// outboundLinks — SAF: Traces (pencereli, guidedAnswerLinks tek çıkışta) ·
// External host çekmecesi · ≤3 örnek trace.
func outboundLinks(route guidedRoute) []guidedAnswerLink {
	what := route.OutboundPath
	if what == "" {
		what = route.OutboundPeer
	}
	if what == "" { // v0.10.1455 — "hangi dış host?" turu: yalnız External
		return []guidedAnswerLink{{Label: "External", Href: "/external"}}
	}
	out := []guidedAnswerLink{{Label: "Traces · " + what, Href: outboundTracesHref(route, false)}}
	if route.OutboundPeer != "" {
		out = append(out, guidedAnswerLink{Label: route.OutboundPeer + " · External", Href: "/external?host=" + url.QueryEscape(route.OutboundPeer)})
	} else {
		out = append(out, guidedAnswerLink{Label: "External", Href: "/external"})
	}
	for _, id := range route.OutboundTraces {
		out = append(out, guidedAnswerLink{Label: "Trace " + id[:12], Href: "/trace?id=" + id})
	}
	return out
}

// outboundSuggestions — SAF: düz öneriler (çip metni router'a yönlenir).
func outboundSuggestions(route guidedRoute) []string {
	if route.OutboundPath == "" && route.OutboundPeer == "" && len(route.OutboundOptions) > 0 { // v0.10.1455
		out := make([]string, 0, len(route.OutboundOptions))
		for _, h := range route.OutboundOptions {
			out = append(out, outboundHostChipText(route, h))
		}
		return out
	}
	svc := route.Service
	if svc == "" && len(route.OutboundCallers) == 1 {
		svc = route.OutboundCallers[0]
	}
	if svc == "" {
		return []string{"Açık problemler?"}
	}
	return []string{svc + " topolojisi?", svc + " sağlığı nasıl?", svc + " en yavaş trace'ler?"}
}

// fuKindOutboundLink — giden çağrı cevabının link çipleri (mesaj göndermez).
const fuKindOutboundLink = "outbound_link"

// outboundFollowups — SAF: "Son 6 saate genişlet" (pencere mutasyonu, rota
// JSON'dan klonlanır) · hatalı çağrıların trace'leri (link, cevabın
// penceresi) · host'un tüm yolları (External çekmecesi).
func outboundFollowups(route guidedRoute, asked []string) []chatFollowup {
	if route.OutboundPath == "" && route.OutboundPeer == "" { // v0.10.1455 — "hangi dış host?"
		return outboundAskHostFollowups(route, asked)
	}
	var cands []chatFollowup
	if route.OutboundRangeS > 0 && route.OutboundRangeS < int64(outboundMaxWindow/time.Second) {
		cands = append(cands, chatFollowup{Text: "Son 6 saate genişlet", Label: "Son 6 saate genişlet", Kind: fuKindMetricWindow})
	}
	win := linkWindow{fromMs: route.OutboundFromMs, toMs: route.OutboundToMs, set: route.OutboundToMs > route.OutboundFromMs}
	cands = append(cands, chatFollowup{Text: "Hatalı çağrıların trace'leri", Label: "Hatalı çağrıların trace'leri", Kind: fuKindOutboundLink, Href: win.apply(outboundTracesHref(route, true))})
	if route.OutboundPeer != "" {
		cands = append(cands, chatFollowup{Text: route.OutboundPeer + " hostunun tüm yolları", Label: route.OutboundPeer + " · tüm yollar", Kind: fuKindOutboundLink, Href: "/external?host=" + url.QueryEscape(route.OutboundPeer)})
	}
	return dedupFollowupsAsked(cands, asked, guidedFollowupMax)
}
