package api

// notify_policy_replay.go — v0.10.1523 (Problem standardı R1): yeni bildirim
// barının geçmiş N günü (≤ 30) yeniden oynatması.
//
//	GET /api/admin/notify-policy/replay?days=14   admin
//
// Okumalar (hepsi zaman sınırlı + LIMIT + max_execution_time 20):
// notification_log (gerçek gönderimler + canlı gölge kodları), problems FINAL
// (pencerede açılmış), problem kararları, onaylı incident_cases, TEK toplu
// service_summary_5m okuması (servis × açılış kovası çiftleri, tavanlı) ve
// TEK toplu exception_groups okuması. Birleştirme SAF:
// chstore.FoldNotifyReplay. Rapor aile başına önce / sonra / kapı başına
// tutma / 30 dk içinde yeniden açılma / "problem değil" sayılarını ve RECALL
// KONTROLÜNÜ (barın tuttuğu her "Gerçek problem" işareti ve onaylı vaka,
// tek tek) taşır.
//
// Neden admin-only: rapor tüm servislerin bildirim geçmişini ve ekip
// kararlarını birlikte açar; R1'de UI yok, operatör uçu prod'da elle koşar.
//
// Neden serveCached 5 dk: 14 günlük okuma pahalı, cevap operatör sorusu
// başına bir kez gerekir. Anahtar TÜM girdileri taşır: gün sayısı (clamp'ten
// SONRA — ?days=999 ayrı girdi basamaz) + politika parmak izi (PUT sonrası
// eski politikanın cevabı servis edilmesin) + saat kovası.
//
// Kayıt init()'te registerRoutesExtra ile (api.go büyümez).

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("notify-policy-replay", (*Server).registerNotifyPolicyReplayRoutes) }

func (s *Server) registerNotifyPolicyReplayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/notify-policy/replay", auth.RequireRole(auth.RoleAdmin, s.getNotifyPolicyReplay))
}

// notifyReplayDays — SAF: ?days= → [1, 30]; boş/bozuk = 14.
func notifyReplayDays(q string) int {
	d, err := strconv.Atoi(strings.TrimSpace(q))
	if err != nil || d <= 0 {
		return chstore.NotifyReplayDefaultDays
	}
	if d > chstore.NotifyReplayMaxDays {
		return chstore.NotifyReplayMaxDays
	}
	return d
}

// notifyReplayKey — SAF: önbellek anahtarı (gün + politika parmak izi +
// saat kovası).
func notifyReplayKey(days int, pol chstore.NotifyPolicy, now time.Time) string {
	raw, _ := json.Marshal(chstore.NormalizeNotifyPolicy(pol))
	h := fnv.New64a()
	_, _ = h.Write(raw)
	return fmt.Sprintf("notify-policy:replay:d=%d:pol=%x:h=%d", days, h.Sum64(), now.Unix()/3600)
}

func (s *Server) getNotifyPolicyReplay(w http.ResponseWriter, r *http.Request) {
	days := notifyReplayDays(r.URL.Query().Get("days"))
	pol := chstore.CurrentNotifyPolicy()
	now := time.Now()
	s.serveCached(w, r, notifyReplayKey(days, pol, now), 5*time.Minute, func(ctx context.Context) (any, error) {
		return s.computeNotifyReplay(ctx, days, pol, now)
	})
}

// computeNotifyReplay — okumalar (her biri sınırlı) + SAF birleştirme.
func (s *Server) computeNotifyReplay(ctx context.Context, days int, pol chstore.NotifyPolicy, now time.Time) (any, error) {
	to := now
	from := now.Add(-time.Duration(days) * 24 * time.Hour)
	in := chstore.NotifyReplayInput{From: from, To: to, Policy: pol,
		Keep: chstore.CurrentProblemPriority().InboxKeepSourcePriorityList(), Priority: chstore.CurrentProblemPriority()}
	sens := s.store.AnomalySensitivityForDetectors()
	in.Calendar = sens.Calendar
	in.IsBatch = sens.IsBatchService

	probs, truncP, err := s.store.ReplayProblems(ctx, from, to)
	if err != nil {
		return nil, err
	}
	sends, truncS, err := s.store.ReplayNotificationSends(ctx, from, to)
	if err != nil {
		return nil, err
	}
	in.Sends, in.Truncated.Problems, in.Truncated.Sends = sends, truncP, truncS
	if sh, err := s.store.ReplayShadowCounts(ctx, from, to); err == nil {
		in.Shadow = sh
	} else {
		log.Printf("[notify-replay] gölge sayımı: %v", err)
	}
	if vs, err := s.store.ListProblemVerdicts(ctx); err == nil {
		in.Verdicts = vs
	} else {
		log.Printf("[notify-replay] kararlar: %v", err)
	}
	for _, src := range []string{"problem", "exception"} {
		cs, err := s.store.ListIncidentCases(ctx, chstore.IncidentCaseQuery{Status: chstore.CaseConfirmed, Source: src,
			From: from.Add(-30 * 24 * time.Hour), To: to})
		if err != nil {
			log.Printf("[notify-replay] onaylı vakalar (%s): %v", src, err)
			continue
		}
		in.Cases = append(in.Cases, cs...)
	}
	// Penceredeki problem listesinde olmayan onaylı problem vakalarının
	// kaynağını tek toplu okumayla getir (vaka açılışı pencere öncesi olabilir).
	have := make(map[string]bool, len(probs))
	for _, p := range probs {
		have[p.ID] = true
	}
	var missing []string
	for _, c := range in.Cases {
		if c.Source == "problem" && !have[c.SourceID] {
			missing = append(missing, c.SourceID)
		}
	}
	if len(missing) > 0 {
		extra, err := s.store.ListProblems(ctx, chstore.ProblemFilter{IDs: missing, Limit: len(missing)})
		if err == nil {
			probs = append(probs, extra...)
		} else {
			log.Printf("[notify-replay] vaka kaynakları: %v", err)
		}
	}
	in.Problems = probs
	traffic, truncT, err := s.store.ServiceBucketTraffic(ctx, chstore.NotifyReplayTrafficPairs(probs))
	if err != nil {
		log.Printf("[notify-replay] trafik: %v", err) // bilinmiyor — tutmaz
	} else {
		in.Traffic, in.Truncated.Traffic = traffic, truncT
	}
	// Exception grupları: gönderilmiş exception kimlikleri + "gerçek" e:
	// işaretleri + onaylı exception vakaları — tek toplu okuma.
	fpSet := map[string]bool{}
	for id, sd := range sends {
		if sd.RelatedKind == "exception" {
			if fp := chstore.ExceptionFingerprintOfNotifyID(id); fp != "" {
				fpSet[fp] = true
			}
		}
	}
	for _, v := range in.Verdicts {
		if v.Verdict == chstore.ProblemVerdictReal && strings.HasPrefix(v.Signature, "e:") {
			fpSet[strings.TrimPrefix(v.Signature, "e:")] = true
		}
	}
	for _, c := range in.Cases {
		if c.Source == "exception" {
			fpSet[c.SourceID] = true
		}
	}
	if len(fpSet) > 0 {
		fps := make([]string, 0, len(fpSet))
		for fp := range fpSet {
			fps = append(fps, fp)
		}
		groups, err := s.store.GetExceptionGroupsByFingerprints(ctx, fps)
		if err != nil {
			log.Printf("[notify-replay] exception grupları: %v", err)
		} else {
			for fp, g := range groups {
				g.Priority, g.PriorityReason = exceptionPriority(g)
				groups[fp] = g
			}
			in.Groups = groups
		}
	}
	return chstore.FoldNotifyReplay(in), nil
}
