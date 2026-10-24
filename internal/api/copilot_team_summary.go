package api

// copilot_team_summary.go — v0.10.1383 (operatör 2026-10-19: "takım ismi
// verince onunla ilgili daha fazla detay verebilir").
//
// Takım cevabı (guidedTeamServices "platform takımının servisleri nasıl?" ve
// guidedMyServices "takımımın servisleri nasıl?") eskiden yalnız RED satırı
// taşıyordu. Artık RED bloğunun ARKASINA tek bir TAKIM ÖZETİ bloğu girer:
//
//	açık problemler (P1/P2/P3 sayısı + P1 servisleri)
//	son 24 saatin deploy'ları ∩ takım servisleri
//	en çok görülen açık exception grupları (pencerede aktif)
//
// ve cevabın altına servis başına yapısal takip adımları (en çok 4):
// "<svc> detayı" → "<svc> servisini analiz et" (servis raporu),
// "<svc> exception'ları" → "<svc> teknik hataları?", deploy bulunduysa
// "<svc> deploy etkisi" → "<svc> son deploy etkisi?". Her cümle guided
// router'da LLM'siz yönlenir (TestFollowupTemplatesRouteGuided).
//
// v0.10.1515 (operatör: "Alttaki chipler çok çıkıyor, gerek yok servis servis
// yazmasına") — servis başına takip adımları (teamSummaryChips + sıralayıcı
// teamSummaryRank) KALDIRILDI; takım cevabının çipleri artık iki genel seçici
// çipi (copilot_team_overview.go teamServicePickChips). Özet bloğu aynen.
//
// Bütçe: üç okuma PARALEL, her biri kendi 3 s zaman aşımıyla; biri düşerse
// yalnız o bölüm "OKUNAMADI" der (soft-fail) — okunmuş ama boş bölüm "yok"
// der. İkisi ASLA karışmaz: okunamayan veri "sorun yok" diye anlatılırsa
// operatör yanlış güvene kapılır. Okumalar sınırlı (problem taraması
// ProblemScanLimit, deploy 200, exception 5) ve MV/state tablolarından; ES
// yok. Sonuç 60 s Redis'te (copilot:teamsum:v1:<fnv>, girdilerin TAMAMI
// özetlenir); bir bölüm okunamadıysa önbelleğe YAZILMAZ (geçici hata 60 s
// boyunca "OKUNAMADI" olarak donmasın).

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	teamSumTimeout       = 3 * time.Second
	teamSumTTL           = 60 * time.Second
	teamSumProblemLimit  = 50 // ProblemScanLimit(…, true) ile açılır
	teamSumDeployLookbk  = 24 * time.Hour
	teamSumDeployLimit   = 200
	teamSumExGroupLimit  = 5
	teamSumP1Rows        = 3
	teamSumDeployRows    = 5
	teamSumChipLabelRune = 40
)

// teamSumProblem — özetteki P1 satırı.
type teamSumProblem struct {
	Service  string `json:"service"`
	RuleName string `json:"ruleName,omitempty"`
}

// teamSumException — özetteki exception grubu satırı.
type teamSumException struct {
	Service     string `json:"service"`
	Type        string `json:"type,omitempty"`
	Message     string `json:"message,omitempty"`
	Occurrences uint64 `json:"occurrences"`
}

// teamSummary — üç okumanın sonucu. *OK=false → o bölüm OKUNAMADI.
type teamSummary struct {
	ProblemsOK     bool                        `json:"problemsOk"`
	P1             int                         `json:"p1"`
	P2             int                         `json:"p2"`
	P3             int                         `json:"p3"`
	ProblemsCapped bool                        `json:"problemsCapped,omitempty"` // tarama tavanına dayandı → "en az"
	P1Top          []teamSumProblem            `json:"p1Top,omitempty"`
	OpenBySvc      map[string]int              `json:"openBySvc,omitempty"`
	P1BySvc        map[string]int              `json:"p1BySvc,omitempty"`
	DeploysOK      bool                        `json:"deploysOk"`
	DeploysCapped  bool                        `json:"deploysCapped,omitempty"` // filo listesi tavanda kesildi
	Deploys        []chstore.RecentDeployEntry `json:"deploys,omitempty"`
	ExceptionsOK   bool                        `json:"exceptionsOk"`
	Exceptions     []teamSumException          `json:"exceptions,omitempty"`
}

func (t teamSummary) allOK() bool { return t.ProblemsOK && t.DeploysOK && t.ExceptionsOK }

// teamSummaryKey — SAF (hash-all-inputs, CLAUDE.md): takım + SIRALI servis
// kümesi + env + pencere (uzunluk + dakika kovası). Servisler tek tek ayrı
// FNV parçası (fnvStr ayırıcısı) — "a,b"+"c" ≠ "a"+"b,c".
func teamSummaryKey(team string, svcs []string, env string, from, to time.Time) string {
	sorted := append([]string(nil), svcs...)
	sort.Strings(sorted)
	parts := append([]string{
		team, env,
		strconv.FormatInt(int64(to.Sub(from)/time.Second), 10),
		strconv.FormatInt(to.Unix()/60, 10),
	}, sorted...)
	return "copilot:teamsum:v1:" + fnvStr(parts...)
}

// teamSummaryFromProblems — SAF: zenginleştirilmiş (öncelik hesaplı) açık
// problemler → sayılar + P1 satırları (servis başına bir, en çok 3).
func teamSummaryFromProblems(t *teamSummary, probs []chstore.Problem, capped bool) {
	t.ProblemsOK, t.ProblemsCapped = true, capped
	t.OpenBySvc, t.P1BySvc = map[string]int{}, map[string]int{}
	seenP1 := map[string]bool{}
	for _, p := range probs {
		svc := strings.TrimSpace(p.Service)
		if svc != "" {
			t.OpenBySvc[svc]++
		}
		switch p.Priority {
		case "P1":
			t.P1++
			if svc != "" {
				t.P1BySvc[svc]++
			}
			if svc != "" && !seenP1[svc] && len(t.P1Top) < teamSumP1Rows {
				seenP1[svc] = true
				t.P1Top = append(t.P1Top, teamSumProblem{Service: svc, RuleName: strings.TrimSpace(p.RuleName)})
			}
		case "P2":
			t.P2++
		default: // boş kova P3 sayılır (chstore.FilterProblemsByPriority ile aynı)
			t.P3++
		}
	}
}

// teamSummaryFromDeploys — SAF: filo deploy listesi (en yeni önce) ∩ takım.
func teamSummaryFromDeploys(t *teamSummary, deps []chstore.RecentDeployEntry, svcs []string, capped bool) {
	t.DeploysOK, t.DeploysCapped = true, capped
	allow := make(map[string]bool, len(svcs))
	for _, s := range svcs {
		allow[s] = true
	}
	for _, d := range deps {
		if allow[strings.TrimSpace(d.Service)] {
			t.Deploys = append(t.Deploys, d)
		}
	}
}

// teamSummaryFromExceptions — SAF: açık exception grupları (occurrences sırası).
func teamSummaryFromExceptions(t *teamSummary, groups []chstore.ExceptionGroup) {
	t.ExceptionsOK = true
	for _, g := range groups {
		t.Exceptions = append(t.Exceptions, teamSumException{
			Service: strings.TrimSpace(g.Service), Type: strings.TrimSpace(g.Type),
			Message: strings.TrimSpace(g.Message), Occurrences: g.Occurrences,
		})
	}
}

// renderTeamSummaryTR — SAF: takım özeti kanıt bloğu. Her bölüm üç
// hâlden birini SÖYLER: veri / "yok" (okundu, boş) / "OKUNAMADI" (okuma
// düştü — sıfır DEĞİL). now deploy yaşları için (test sabitler).
func renderTeamSummaryTR(team string, nSvcs int, t teamSummary, rangeS int64, env string, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Takım özeti (%s, %d servis):\n", team, nSvcs)
	if env != "" {
		fmt.Fprintf(&b, "Not: %q ortam süzgeci YALNIZ problemlere uygulandı; deploy ve exception bölümleri tüm ortamlar.\n", env)
	}
	// Problemler
	switch {
	case !t.ProblemsOK:
		b.WriteString("- Açık problemler: OKUNAMADI (okuma başarısız — sıfır SANMA, \"problem yok\" DEME).\n")
	case t.P1+t.P2+t.P3 == 0:
		b.WriteString("- Açık problemler: yok.\n")
	default:
		atLeast := ""
		if t.ProblemsCapped {
			atLeast = "en az "
		}
		fmt.Fprintf(&b, "- Açık problemler: %s%d (P1: %d, P2: %d, P3: %d)", atLeast, t.P1+t.P2+t.P3, t.P1, t.P2, t.P3)
		if env != "" {
			fmt.Fprintf(&b, " — %q ortamı", env)
		}
		b.WriteString(".\n")
		if len(t.P1Top) > 0 {
			parts := make([]string, 0, len(t.P1Top))
			for _, p := range t.P1Top {
				if p.RuleName != "" {
					parts = append(parts, p.Service+" ("+truncate(p.RuleName, 60)+")")
				} else {
					parts = append(parts, p.Service)
				}
			}
			fmt.Fprintf(&b, "  P1 servisleri: %s.\n", strings.Join(parts, ", "))
		}
	}
	// Deploy'lar
	switch {
	case !t.DeploysOK:
		b.WriteString("- Son 24 saatte deploy: OKUNAMADI (okuma başarısız — \"deploy yok\" DEME).\n")
	case len(t.Deploys) == 0:
		b.WriteString("- Son 24 saatte deploy: yok")
		if t.DeploysCapped {
			fmt.Fprintf(&b, " (filo listesinin en yeni %d kaydında; daha eskisi görünmeyebilir)", teamSumDeployLimit)
		}
		b.WriteString(".\n")
	default:
		fmt.Fprintf(&b, "- Son 24 saatte deploy: %d", len(t.Deploys))
		if t.DeploysCapped {
			b.WriteString(" (en az — filo listesi tavanda kesildi)")
		}
		b.WriteString(":\n")
		for i, d := range t.Deploys {
			if i >= teamSumDeployRows {
				fmt.Fprintf(&b, "  … ve %d deploy daha.\n", len(t.Deploys)-teamSumDeployRows)
				break
			}
			v := strings.TrimSpace(d.Version)
			if v == "" {
				v = "sürüm bilinmiyor"
			}
			ago := "zaman bilinmiyor"
			if d.FirstSeenNs > 0 {
				ago = fmtAgoTR(int64(now.Sub(time.Unix(0, d.FirstSeenNs))/time.Second)) + " önce"
			}
			fmt.Fprintf(&b, "  - %s → %s (%s)\n", d.Service, v, ago)
		}
	}
	// Exception'lar
	switch {
	case !t.ExceptionsOK:
		b.WriteString("- Açık exception grupları: OKUNAMADI (okuma başarısız — \"exception yok\" DEME).\n")
	case len(t.Exceptions) == 0:
		fmt.Fprintf(&b, "- Açık exception grupları (son %s aktif): yok.\n", fmtAgoTR(rangeS))
	default:
		fmt.Fprintf(&b, "- Açık exception grupları (son %s aktif, en çok görülen %d):\n", fmtAgoTR(rangeS), len(t.Exceptions))
		for _, e := range t.Exceptions {
			what := e.Type
			if what == "" {
				what = "exception"
			}
			fmt.Fprintf(&b, "  - %s · %s · %d oluşum", e.Service, what, e.Occurrences)
			if e.Message != "" {
				fmt.Fprintf(&b, " · %s", truncate(e.Message, 120))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("KURAL: takım özetini kısaca ver (P1 varsa önce onu); OKUNAMADI yazan bölümü \"yok\" diye ANLATMA, " +
		"okunamadığını söyle. Cevabın altındaki çipler bir servis seçtirip sağlığına ya da exception'larına iniyor.\n")
	return b.String()
}

// readTeamSummary — üç sınırlı okuma paralel; her biri kendi zaman aşımıyla
// soft-fail. Önbellek: tam başarılı sonuç 60 s.
func (s *Server) readTeamSummary(ctx context.Context, team string, svcs []string, env string, from, to time.Time) teamSummary {
	key := teamSummaryKey(team, svcs, env, from, to)
	if s.cache != nil {
		if b, ok, _ := s.cache.Get(ctx, key); ok && len(b) > 0 {
			var t teamSummary
			if json.Unmarshal(b, &t) == nil {
				return t
			}
		}
	}
	var t teamSummary
	var mu sync.Mutex
	var wg sync.WaitGroup
	run := func(read func(context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, teamSumTimeout)
			defer cancel()
			read(c)
		}()
	}
	run(func(c context.Context) {
		lim := chstore.ProblemScanLimit(teamSumProblemLimit, true)
		probs, err := s.store.ListProblems(c, chstore.ProblemFilter{Status: "open", Services: svcs, Env: env, Limit: lim})
		if err != nil {
			return
		}
		probs = s.enrichProblemsForRead(c, probs)
		mu.Lock()
		teamSummaryFromProblems(&t, probs, len(probs) >= lim)
		mu.Unlock()
	})
	run(func(c context.Context) {
		deps, err := s.store.GetRecentDeploys(c, teamSumDeployLookbk, teamSumDeployLimit)
		if err != nil {
			return
		}
		mu.Lock()
		teamSummaryFromDeploys(&t, deps, svcs, len(deps) >= teamSumDeployLimit)
		mu.Unlock()
	})
	run(func(c context.Context) {
		gs, err := s.store.ListExceptionGroups(c, chstore.ExceptionGroupFilter{
			State: "open", Services: svcs, ActiveFromNs: from.UnixNano(),
			Sort: "occurrences", Dir: "desc", Limit: teamSumExGroupLimit,
		})
		if err != nil {
			return
		}
		mu.Lock()
		teamSummaryFromExceptions(&t, gs)
		mu.Unlock()
	})
	wg.Wait()
	if t.allOK() && s.cache != nil {
		if b, err := json.Marshal(t); err == nil {
			_ = s.cache.Set(ctx, key, b, teamSumTTL)
		}
	}
	return t
}

// teamSummaryBlock — guided takım bundle'larının ortak kancası: okur, ⚙
// adımını emit eder, kanıt bloğunu döndürür. v0.10.1515 — servis çipi
// yazmaz (takım cevabının çipleri teamOverviewBlock'ta).
func (s *Server) teamSummaryBlock(ctx context.Context, emit func(string, any), route *guidedRoute, team string, svcs []string, from, to time.Time, rangeS int64) string {
	if len(svcs) == 0 {
		return ""
	}
	n := emitGuidedStep(emit, "team_summary", fmt.Sprintf(`{"team":%s,"services":%d}`, jsonStr(team), len(svcs)))
	t := s.readTeamSummary(ctx, team, svcs, route.Env, from, to)
	blk := renderTeamSummaryTR(team, len(svcs), t, rangeS, route.Env, time.Now())
	emitGuidedStepResult(emit, n, "team_summary", blk, nil)
	return blk
}
