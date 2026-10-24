package sre

// contextpack_sections.go — v0.10.1494 (CoSRE 2d, plan §1.3 A2): Context
// Pack'in okuyucu bölümleri + öncelikli kırpma + bayrak kararı.
//
// Bölümler (sıra / öncelik — 0 asla düşmez … 9 ilk düşen):
//
//	baseline  2  BASELINE (veri, kaynak …): şimdi vs önceki pencere / 7 gün önce
//	changes   2  DEĞİŞİKLİKLER (veri): deploy satırları + git kaynağı + etki
//	anomalies 4  ANOMALİLER (veri, skor sırası)
//	neighbors 4  KOMŞULAR (veri): kenar hata/gecikme + eşzamanlı anomali
//	errors    5  HATA ÖZETİ (veri): operasyon / istisna grubu / örnek trace
//
// Hepsi "(veri)" etiketli: sistem promptunun DataNotInstruction çerçevesi
// sonda kalır. SAF: kayıt alanlarından; okuma yok, insan işareti / oy /
// model güveni okunmaz (contextpack_test.go korkuluğu bu dosyayı da tarar). Bayrak kapalıyken hiçbiri üretilmez (BuildContextPack).

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

const (
	packChangesMax   = 5
	packNeighborsMax = 5
)

// packFor — bu koşunun paket ayarı (nil = kapalı). Tekrar (Kind=replay)
// yalnız inv.Pack'e bakar (koşu isteği); canlı koşu bayrağa.
func packFor(rd readers, inv *Investigation) *ContextPackSettings {
	get := CurrentContextPackSettings
	if rd.pack != nil {
		get = rd.pack
	}
	c := NormalizeContextPackSettings(get())
	if inv.Kind == KindReplay {
		if !inv.Pack {
			return nil
		}
		c.Enabled = true
		return &c
	}
	if !c.Enabled {
		return nil
	}
	return &c
}

// packTokens — anlatım bütçesinin token tavanı (ayardan).
func packTokens(rd readers) int {
	get := CurrentContextPackSettings
	if rd.pack != nil {
		get = rd.pack
	}
	return NormalizeContextPackSettings(get()).MaxTokens
}

// narrationInput — SAF: anlatım girdisi. inv.Pack=false → renderInvestigationTR
// (legacy 4 KB, bayt bayt); true → okuyucu bölümleri + öncelikli kırpma
// (maxTokens × 4 bayt) ve künye.
func narrationInput(inv Investigation, p chstore.Problem, loc *time.Location, maxTokens int) (string, *PackMeta) {
	if !inv.Pack {
		return renderInvestigationTR(inv, p, loc), nil
	}
	if maxTokens <= 0 {
		maxTokens = DefaultContextPackSettings().MaxTokens
	}
	txt, meta := RenderTR(BuildContextPackWith(inv, p, loc, true), PackBudget{Mode: PackModePriority, MaxBytes: maxTokens * 4})
	return txt, &meta
}

// packMetaFor — kayda yazılacak künye (kapalı → nil). Tekrarda da üretilir
// (LLM yok — paket deterministik, değerlendirme boyut/kırpmayı ölçer).
func packMetaFor(inv Investigation, p chstore.Problem, pk *ContextPackSettings) *PackMeta {
	if pk == nil || !inv.Pack {
		return nil
	}
	_, meta := narrationInput(inv, p, tzdefault.Location(), pk.MaxTokens) // anlatımla aynı dilim
	return meta
}

// readerSections — SAF: okuyucu gerçekleri → bölümler (gerçek yoksa bölüm yok).
func readerSections(inv Investigation, hm func(int64) string) []PackSection {
	if inv.Facts == nil {
		return nil
	}
	f := inv.Facts
	var out []PackSection
	add := func(key string, prio int, text string) {
		if text != "" {
			out = append(out, section(key, prio, text))
		}
	}
	add("baseline", 2, sectionBaselineTR(f.Baseline))
	add("changes", 2, sectionChangesTR(f.Deploys, f.Baseline, hm))
	add("anomalies", 4, sectionAnomaliesTR(f.Anomalies, hm))
	add("neighbors", 4, sectionNeighborsTR(f.Deps, f.Anomalies))
	add("errors", 5, sectionErrorsTR(f.Errors, f.Pivot))
	return out
}

func redLineTR(s REDStat, errUnknown bool) string {
	e := fmt.Sprintf("hata %%%.2f", s.ErrPct)
	if errUnknown {
		e = "hata bilinmiyor"
	}
	return fmt.Sprintf("%.4g r/s · %s · p99 %.0f ms", s.Rate, e, s.P99Ms)
}

func sectionBaselineTR(bf *BaselineFacts) string {
	if bf == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "BASELINE (veri, %s):", sourceTR(bf))
	if !bf.CurOK {
		b.WriteString(" çıpa penceresinde RED yok\n")
		return b.String()
	}
	b.WriteString(" şimdi " + redLineTR(bf.Cur, bf.ErrorsUnknown) + "\n")
	for _, r := range bf.Rows {
		if r.NoData {
			fmt.Fprintf(&b, "- %s: %s\n", baselineRefLabelTR(r.Ref), firstNonEmpty(r.Note, "veri yok"))
			continue
		}
		fmt.Fprintf(&b, "- %s: %s (rate %s, hata %s, p99 %s)", baselineRefLabelTR(r.Ref), redLineTR(r.Base, bf.ErrorsUnknown),
			fmtDelta(r.RateDeltaPct), fmtDelta(r.ErrDeltaPct), fmtDelta(r.P99DeltaPct))
		if r.Note != "" {
			b.WriteString(" · " + r.Note)
		}
		b.WriteString("\n")
	}
	if bf.BatchService {
		b.WriteString("- batch servis: yük (rate) farkı anomali sayılmaz\n")
	}
	return b.String()
}

// originTR — git kaynağının Türkçe etiketi.
func originTR(origin string, conf uint8) string {
	var s string
	switch origin {
	case "rollback":
		s = "geri alma (git)"
	case "redeploy_older":
		s = "eski yapının yeniden deploy'u"
	case "forward":
		s = "ileri deploy"
	case "unknown":
		s = "bilinmiyor"
	default:
		return ""
	}
	if conf > 0 && origin != "unknown" {
		s += fmt.Sprintf(" %%%d", conf)
	}
	return s
}

func sectionChangesTR(df *DeployFacts, bf *BaselineFacts, hm func(int64) string) string {
	if df == nil || len(df.Rows) == 0 {
		return ""
	}
	var im *DeployImpactFact
	if bf != nil {
		im = bf.Impact
	}
	var b strings.Builder
	b.WriteString("DEĞİŞİKLİKLER (veri):\n")
	for i, r := range df.Rows {
		if i >= packChangesMax {
			break
		}
		fmt.Fprintf(&b, "- %s %s", hm(r.At), truncRunes(r.Name, 80))
		if r.Version != "" {
			b.WriteString(" " + truncRunes(r.Version, 40))
			if r.Prev != "" {
				b.WriteString("←" + truncRunes(r.Prev, 40))
			}
		}
		if o := originTR(r.Origin, r.OriginConfidence); o != "" {
			b.WriteString(" · kaynak: " + o)
		}
		if im != nil && im.At == r.At && im.Name == r.Name {
			switch {
			case im.BeforeOK && im.AfterOK:
				fmt.Fprintf(&b, " · deploy etkisi (±%d dk): hata %%%.2f→%%%.2f, p99 %.0f→%.0f ms, rate %s", im.WindowSec/60,
					im.Before.ErrPct, im.After.ErrPct, im.Before.P99Ms, im.After.P99Ms, fmtDelta(im.RateDeltaPct))
			case im.Note != "":
				b.WriteString(" · deploy etkisi: " + im.Note)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func sectionAnomaliesTR(af *AnomalyFacts, hm func(int64) string) string {
	if af == nil || !af.SubjectOK || len(af.Subject) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ANOMALİLER (veri, skor sırası; %d olaydan ilk %d):\n", af.SubjectAll, len(af.Subject))
	for _, a := range af.Subject {
		fmt.Fprintf(&b, "- %s %s tepe×%.2g şimdi×%.2g başlangıç %s\n", a.Kind, truncRunes(oneLine(a.Pattern), 100), a.PeakRatio, a.CurrentRatio, hm(a.StartedAt))
	}
	return b.String()
}

func sectionNeighborsTR(df *DepsFacts, af *AnomalyFacts) string {
	if df == nil || af == nil || !af.NeighborsOK {
		return ""
	}
	by := map[string]NeighborAnomaly{}
	for _, n := range af.Neighbors {
		by[n.Target] = n
	}
	var b strings.Builder
	rows := 0
	for _, e := range df.Edges {
		if !isServiceNode(e.Target) || rows >= packNeighborsMax {
			continue
		}
		rows++
		fmt.Fprintf(&b, "- %s hata %%%.2f ort %.0f ms", e.Target, e.ErrorRate, e.AvgMs)
		if n, ok := by[e.Target]; ok {
			fmt.Fprintf(&b, " · eşzamanlı anomali %d [%s %s]", n.Count, n.TopKind, truncRunes(oneLine(n.TopPattern), 80))
		} else {
			b.WriteString(" · eşzamanlı anomali yok")
		}
		b.WriteString("\n")
	}
	if rows == 0 {
		return ""
	}
	return "KOMŞULAR (veri, aşağı akış servisler):\n" + b.String()
}

func sectionErrorsTR(ef *ErrorFacts, pf *PivotFacts) string {
	if ef == nil || (len(ef.Ops) == 0 && len(ef.Groups) == 0) {
		return ""
	}
	var b strings.Builder
	b.WriteString("HATA ÖZETİ (veri):\n")
	for _, o := range ef.Ops {
		fmt.Fprintf(&b, "- operasyon %s: %d hata (%%%.2f)\n", o.Name, o.Errors, o.ErrPct)
	}
	for _, g := range ef.Groups {
		fmt.Fprintf(&b, "- istisna %s ×%d", g.Type, g.Count)
		if g.Message != "" {
			b.WriteString(" · " + truncRunes(g.Message, 120))
		}
		if g.SampleTraceID != "" {
			b.WriteString(" · örnek trace " + g.SampleTraceID)
		}
		b.WriteString("\n")
	}
	if pf != nil && pf.TraceID != "" {
		b.WriteString("- temsilci trace (pivot): " + pf.TraceID + "\n")
	}
	return b.String()
}

// packSec — kırpma sırasında bölümün hâli.
type packSec struct {
	PackSection
	header  string
	rows    []string
	trimmed int
	dropped bool
}

func (s *packSec) text() string {
	if s.dropped {
		return ""
	}
	t := s.header + strings.Join(s.rows, "")
	if s.trimmed > 0 {
		t += fmt.Sprintf("… (+%d satır kırpıldı)\n", s.trimmed)
	}
	return t
}

// renderPriority — SAF: öncelikli kırpma. Tavan max bayt (≤0 → tavansız).
// Sıra: Priority büyükten küçüğe, eşitlikte sondaki bölüm önce; her bölümde
// satırlar sondan kırpılır, başlık + "… (+k satır kırpıldı)" kalır; hâlâ
// aşıyorsa bölüm düşer. Öncelik 0–1 dokunulmaz; son emniyet capBytes.
func renderPriority(pack ContextPack, max int) (string, PackMeta) {
	meta := PackMeta{Mode: PackModePriority, Sections: []PackSectionMeta{}, MaxBytes: max}
	var secs []*packSec
	for _, s := range pack.Sections {
		if s.Text == "" {
			continue
		}
		lines := strings.SplitAfter(s.Text, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		secs = append(secs, &packSec{PackSection: s, header: lines[0], rows: append([]string(nil), lines[1:]...)})
	}
	total := func() int {
		n := 0
		for _, s := range secs {
			n += len(s.text())
		}
		return n
	}
	if max > 0 && total() > max {
		order := make([]int, len(secs))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool {
			if secs[order[a]].Priority != secs[order[b]].Priority {
				return secs[order[a]].Priority > secs[order[b]].Priority
			}
			return order[a] > order[b]
		})
		for _, i := range order {
			s := secs[i]
			if total() <= max {
				break
			}
			if s.Priority <= 1 {
				continue
			}
			for len(s.rows) > 0 && total() > max {
				s.rows = s.rows[:len(s.rows)-1]
				s.trimmed++
			}
			if total() > max {
				s.dropped = true
			}
		}
	}
	var sb strings.Builder
	for _, s := range secs {
		sb.WriteString(s.text())
		meta.Sections = append(meta.Sections, PackSectionMeta{Key: s.Key, Bytes: len(s.Text), Rows: s.Rows, TrimmedRows: s.trimmed, Dropped: s.dropped})
		if s.trimmed > 0 || s.dropped {
			meta.Trimmed = true
		}
	}
	out := sb.String()
	if max > 0 && len(out) > max {
		out, meta.Capped = capBytes(out, max), true
	}
	meta.Bytes, meta.EstTokens = len(out), estimateTokens(out)
	return out, meta
}
