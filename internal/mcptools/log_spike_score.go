package mcptools

// log_spike_score.go — v0.10.1284 (AI SRE Faz 1a): explain_log_spike'ın SAF
// çekirdeği — tepe bulma, taban penceresi, katkı sıralaması, Türkçe gerekçe,
// önbellek anahtarı. Ağ yok, saat yok; hepsi tablo testli
// (log_spike_score_test.go).
//
// Tepe (detectSpike): m = medyan, σ = max(1.4826·MAD, √max(m,1), 1) (Poisson
// tabanı: seyrek seride tek satır "tepe" olmasın). Sıcak kova: z ≥ 3.5 VE
// x ≥ 1.5·m VE x − m ≥ max(10, 5·kova_s/60). ≤1 kovalık boşlukla ayrılan
// sıcak koşular birleşir, kenarlar z ≥ 1.5 sürdükçe genişler; puan Σ(x − m),
// en büyük kazanır (eşitlikte en yeni). Koşu kovaların yarısından uzunsa
// "level_shift" (tepe değil seviye değişimi).
//
// Katkı (rankSpikeContributors): b̂ = b·Ls/Lb, fazla = s − b̂, E = S − B̂.
// Tut: E > 0 VE s ≥ max(20, S·%1) VE fazla ≥ E·%10 VE s ≥ 1.5·b̂ + 5 VE
// artıştaki payı tabandakinden büyük (lift > 0 — orantılı büyüyen değer
// artışı açıklamaz; tasarıma ek, v0.10.1284).
// pay = fazla/E. Tabanın top-N'i TAMSA (N'den az değer döndü) ve değer
// tabanda yoksa "yeni"; top-N doluysa taban bilinmiyor → üst sınır olarak
// N'inci değerin sayısı kullanılır ve "normalde ≤ %x" denir (baseUnknown).
// Sıra fazlaya göre, boyut başına ≤2, toplam ≤5.

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

// Eşikler (varsayım 1 — design-followups-AD).
const (
	spikeZHot        = 3.5
	spikeZExtend     = 1.5
	spikeMinFactor   = 1.5
	spikeMinAbs      = 10.0
	spikeLevelShift  = 0.5
	spikeMinSupport  = 20
	spikeSupportPct  = 0.01
	spikeMinShare    = 0.10
	spikeLiftFactor  = 1.5
	spikeLiftPad     = 5.0
	spikePerDimMax   = 2
	spikeContribMax  = 5
	spikeBaseMin     = 15 * time.Minute
	spikeBaseMax     = 3 * time.Hour
	spikeBaseFactor  = 3
	spikeWeakFactor  = 1.2
	spikeKeyBucket   = 30 // s — önbellek anahtarının zaman ızgarası
	spikeKeyVersion  = "logspike:v1:"
	spikePatternMin  = 5 // desen boyutu: örneklemde en az bu kadar satır
	spikeValueRunes  = 80
	spikeReasonRunes = 200
)

// Kaynak (spike.source) değerleri.
const (
	spikeSourceGiven      = "given"
	spikeSourceDetected   = "detected"
	spikeSourceLevelShift = "level_shift"
)

// Boyut adları (contributors[].dim / dims[].dim).
const (
	spikeDimSeverity  = "severity"
	spikeDimPod       = "pod"
	spikeDimCluster   = "cluster"
	spikeDimService   = "service"
	spikeDimNamespace = "namespace"
	spikeDimLogger    = "logger"
	spikeDimErrorType = "error_type"
	spikeDimPattern   = "pattern"
)

// spikeDetection — detectSpike çıktısı. Start/End KAPSAYAN kova indeksleri.
type spikeDetection struct {
	Found      bool
	Start, End int
	LevelShift bool
	Median     float64
	Peak       int64
	PeakRatio  float64 // tepe / max(medyan, 1)
	Score      float64
}

func medianF(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// detectSpike — SAF: tamamlanmış kovaların sayıları → en belirgin artış.
func detectSpike(x []int64, bucketS int) spikeDetection {
	out := spikeDetection{}
	n := len(x)
	if n == 0 {
		return out
	}
	fx := make([]float64, n)
	var peak int64
	for i, v := range x {
		fx[i] = float64(v)
		if v > peak {
			peak = v
		}
	}
	m := medianF(fx)
	dev := make([]float64, n)
	for i, v := range fx {
		dev[i] = math.Abs(v - m)
	}
	sigma := math.Max(1.4826*medianF(dev), math.Max(math.Sqrt(math.Max(m, 1)), 1))
	out.Median, out.Peak = m, peak
	out.PeakRatio = round4(float64(peak) / math.Max(m, 1))
	minAbs := math.Max(spikeMinAbs, 5*float64(bucketS)/60)
	z := func(i int) float64 { return (fx[i] - m) / sigma }
	hot := make([]bool, n)
	for i := range fx {
		hot[i] = z(i) >= spikeZHot && fx[i] >= spikeMinFactor*m && fx[i]-m >= minAbs
	}
	// Sıcak koşular (≤1 kovalık boşlukla birleşir).
	type run struct{ s, e int }
	var runs []run
	for i := 0; i < n; i++ {
		if !hot[i] {
			continue
		}
		if k := len(runs); k > 0 && i-runs[k-1].e <= 2 {
			runs[k-1].e = i
			continue
		}
		runs = append(runs, run{i, i})
	}
	if len(runs) == 0 {
		return out
	}
	best, bestScore := -1, math.Inf(-1)
	for k := range runs {
		r := &runs[k]
		for r.s > 0 && z(r.s-1) >= spikeZExtend {
			r.s--
		}
		for r.e < n-1 && z(r.e+1) >= spikeZExtend {
			r.e++
		}
		sc := 0.0
		for i := r.s; i <= r.e; i++ {
			sc += fx[i] - m
		}
		if sc >= bestScore { // eşitlikte en yeni (sonraki koşu)
			best, bestScore = k, sc
		}
	}
	r := runs[best]
	out.Found, out.Start, out.End, out.Score = true, r.s, r.e, bestScore
	out.LevelShift = float64(r.e-r.s+1) > spikeLevelShift*float64(n)
	return out
}

// spikeBaseWindow — SAF: taban süresi = clamp(3·L, 15 dk, 3 sa).
func spikeBaseWindow(spike time.Duration) time.Duration {
	b := time.Duration(spikeBaseFactor) * spike
	if b < spikeBaseMin {
		return spikeBaseMin
	}
	if b > spikeBaseMax {
		return spikeBaseMax
	}
	return b
}

// spikeDimInput — bir boyutun iki penceredeki üst değerleri.
type spikeDimInput struct {
	Dim   string
	Field string
	Spike *logstore.FieldStatsResult
	Base  *logstore.FieldStatsResult
	// BaseLimit — tabandan istenen değer sayısı; dönen < BaseLimit ise
	// top-N TAMDIR (listede olmayan değer tabanda yoktur).
	BaseLimit int
	// Raw — desen boyutu: örneklemdeki HAM satır sayısı (ölçeklenmemiş);
	// RawMin altındaki değer desteksiz sayılır. nil = yok.
	Raw    map[string]int64
	RawMin int64
}

// spikeTotals — iki pencerenin toplam log sayısı + süreleri.
type spikeTotals struct {
	S, B   int64
	Ls, Lb time.Duration
}

// SpikeContributor — sıralı katkı.
type SpikeContributor struct {
	Rank        int     `json:"rank"`
	Dim         string  `json:"dim"`
	Value       string  `json:"value"`
	SpikeCount  int64   `json:"spikeCount"`
	BaseCount   int64   `json:"baseCount"`
	SharePct    float64 `json:"sharePct"`
	SpikePct    float64 `json:"spikePct"`
	BasePct     float64 `json:"basePct"`
	LiftPts     float64 `json:"liftPts"`
	New         bool    `json:"new,omitempty"`
	BaseUnknown bool    `json:"baseUnknown,omitempty"`
	Reason      string  `json:"reason"`
	excess      float64
}

// rankSpikeContributors — SAF. İkinci dönüş: elenmeden geçen ama tavana
// sığmayan katkı var mı (contributorsHasMore).
func rankSpikeContributors(dims []spikeDimInput, t spikeTotals, max int) ([]SpikeContributor, bool) {
	if max <= 0 {
		max = spikeContribMax
	}
	if t.Ls <= 0 || t.Lb <= 0 {
		return []SpikeContributor{}, false
	}
	scale := float64(t.Ls) / float64(t.Lb)
	E := float64(t.S) - float64(t.B)*scale
	if E <= 0 {
		return []SpikeContributor{}, false
	}
	var cands []SpikeContributor
	for _, d := range dims {
		if d.Spike == nil || len(d.Spike.Values) == 0 {
			continue
		}
		lift := logstore.LiftFieldStats(d.Spike, d.Base)
		baseCount := map[string]int64{}
		complete := true
		var floor int64
		var baseTotal int64
		if d.Base != nil {
			baseTotal = d.Base.Total
			for _, v := range d.Base.Values {
				baseCount[v.Value] = v.Count
			}
			if d.BaseLimit > 0 && len(d.Base.Values) >= d.BaseLimit {
				complete = false
				floor = d.Base.Values[len(d.Base.Values)-1].Count
				for _, v := range d.Base.Values {
					if v.Count < floor {
						floor = v.Count
					}
				}
			}
		}
		for _, v := range lift.Values {
			s := v.Count
			if d.Raw != nil && d.Raw[v.Value] < d.RawMin {
				continue
			}
			b, inBase := baseCount[v.Value]
			isNew, unknown := false, false
			basePct := v.BasePct
			if !inBase {
				if complete {
					isNew = true
				} else {
					unknown = true
					b = floor // üst sınır → fazla için muhafazakâr
					if baseTotal > 0 {
						basePct = 100 * float64(floor) / float64(baseTotal)
					}
				}
			}
			bhat := float64(b) * scale
			excess := float64(s) - bhat
			support := math.Max(spikeMinSupport, spikeSupportPct*float64(t.S))
			if float64(s) < support || excess < spikeMinShare*E || float64(s) < spikeLiftFactor*bhat+spikeLiftPad {
				continue
			}
			// Payı ARTMAYAN değer artışı AÇIKLAMAZ, yalnız onunla orantılı
			// büyür (tek desen %100 → %100, baskın INFO bandı): lift > 0 şart.
			if v.SelPct-basePct <= 0 {
				continue
			}
			c := SpikeContributor{Dim: d.Dim, Value: v.Value, SpikeCount: s, BaseCount: b,
				SharePct: spikeRound1(100 * excess / E), SpikePct: spikeRound1(v.SelPct), BasePct: spikeRound1(basePct),
				LiftPts: spikeRound1(v.SelPct - basePct), New: isNew, BaseUnknown: unknown, excess: excess}
			if isNew {
				c.BaseCount = 0
			}
			c.Reason = spikeReasonTR(c)
			cands = append(cands, c)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].excess != cands[j].excess {
			return cands[i].excess > cands[j].excess
		}
		if cands[i].Dim != cands[j].Dim {
			return cands[i].Dim < cands[j].Dim
		}
		return cands[i].Value < cands[j].Value
	})
	perDim := map[string]int{}
	out := []SpikeContributor{}
	more := false
	for _, c := range cands {
		if perDim[c.Dim] >= spikePerDimMax {
			continue
		}
		if len(out) >= max {
			more = true
			break
		}
		perDim[c.Dim]++
		c.Rank = len(out) + 1
		out = append(out, c)
	}
	return out, more
}

func spikeRound1(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*10) / 10
}

// spikeDimTR — boyutun Türkçe adı (gerekçe + tablo).
func spikeDimTR(dim string) string {
	switch dim {
	case spikeDimSeverity:
		return "seviye"
	case spikeDimService:
		return "servis"
	case spikeDimErrorType:
		return "hata türü"
	case spikeDimPattern:
		return "desen"
	}
	return dim // pod, cluster, namespace, logger — operatörün kelimeleri
}

// pctTR — "%62'si" / "%35'i" / "%40'ı": sayının Türkçe okunuşuna göre
// iyelik eki (son okunan sözcük: birler, yoksa onlar, 100 "yüz").
func pctTR(v float64) string {
	n := int(math.Round(v))
	return fmt.Sprintf("%%%d'%s", n, numPossessive(n))
}

func numPossessive(n int) string {
	if n < 0 {
		n = -n
	}
	ones := []string{"ı", "i", "si", "ü", "ü", "i", "sı", "si", "i", "u"} // sıfır bir iki üç dört beş altı yedi sekiz dokuz
	tens := []string{"", "u", "si", "u", "ı", "si", "ı", "i", "i", "ı"}   // on yirmi otuz kırk elli altmış yetmiş seksen doksan
	switch {
	case n == 0:
		return "ı"
	case n%10 != 0:
		return ones[n%10]
	case n%100 != 0:
		return tens[(n/10)%10]
	case n%1000 != 0:
		return "ü" // yüz
	}
	return "i" // bin
}

// spikeValueTR — gerekçedeki değer (kısaltılmış; desen «…» içinde).
func spikeValueTR(dim, v string) string {
	v = truncateRunes(strings.Join(strings.Fields(v), " "), spikeValueRunes)
	if dim == spikeDimPattern {
		return "«" + v + "»"
	}
	return v
}

// spikeReasonTR — SAF: katkının tek satırlık Türkçe gerekçesi. Değer adına
// ek getirilmez (ünlü uyumu keyfi kimliklerde güvenilmez) — "kaynaklı".
func spikeReasonTR(c SpikeContributor) string {
	val := spikeDimTR(c.Dim) + " " + spikeValueTR(c.Dim, c.Value)
	var s string
	switch {
	case c.New:
		s = fmt.Sprintf("yeni %s — artışın %s (öncesinde görülmedi)", val, pctTR(c.SharePct))
	case c.BaseUnknown:
		s = fmt.Sprintf("artışın %s %s kaynaklı (artışta payı %%%s, normalde ≤ %%%s)", pctTR(c.SharePct), val, fmtPctTR(c.SpikePct), fmtPctTR(c.BasePct))
	default:
		s = fmt.Sprintf("artışın %s %s kaynaklı (artışta payı %%%s, normalde %%%s)", pctTR(c.SharePct), val, fmtPctTR(c.SpikePct), fmtPctTR(c.BasePct))
	}
	return truncateRunes(s, spikeReasonRunes)
}

// fmtPctTR — yüzde değeri: tam sayıysa ondalıksız, değilse tek ondalık
// (Türkçe ondalık virgül).
func fmtPctTR(v float64) string {
	v = spikeRound1(v)
	if v == math.Trunc(v) {
		return fmt.Sprintf("%d", int(v))
	}
	return strings.Replace(fmt.Sprintf("%.1f", v), ".", ",", 1)
}

// spikeKeyBucket30 — önbellek anahtarının 30 sn ızgarası (unix sn).
func spikeKeyBucket30(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix() / spikeKeyBucket * spikeKeyBucket
}

// logSpikeKey — SAF: önbellek anahtarı TÜM girdileri hash'ler (CLAUDE.md
// sert kısıtı): servis, env, sorgu, seviye, 30 sn'lik pencere uçları,
// kaynak (verilen/otomatik), arka uç ve SIRALI çözülmüş alan adları.
func logSpikeKey(req LogSpikeRequest, from, to time.Time, backend string, fields []string) string {
	fs := append([]string(nil), fields...)
	sort.Strings(fs)
	source := "auto"
	if !req.SpikeFrom.IsZero() {
		source = spikeSourceGiven
	}
	return spikeKeyVersion + fnvHex(req.Service, req.Env, req.Query, req.Level,
		fmt.Sprint(spikeKeyBucket30(from)), fmt.Sprint(spikeKeyBucket30(to)), source, backend, strings.Join(fs, "\x01"))
}
