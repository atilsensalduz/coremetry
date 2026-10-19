package api

// copilot_starters.go — v0.10.702 (operatör isteği 2026-09-12: "ilk sohbetteki
// öneriler zenginleşsin — takımımın servislerine ek olarak belirli bir
// endpoint'te hata alan trace'ler").
//
//	GET /api/copilot/starters?range_s=3600&env=prod
//
// Boş sohbetin VERİ çipleri. Statik çipler ("Takımımın servisleri nasıl?")
// FE'de kalır. Üretilen her çip guided router'da (ya da operasyon kataloğu
// ön-getirmesinde) LLM'siz yönlenen TAM cümledir (copilot_followup.go
// sözleşmesi; copilot_starters_test her cümleyi routeGuidedIntent'ten /
// opTokens+matchOperations'tan geçirir). Uydurma çip yok: veri yoksa grup yok.
//
// v0.10.1382 (operatör: "CoSRE'deki chat hazır soruları daha anlamlı cevapları
// seçilebilir halde getirsek") — yanıt GRUPLU: boş sohbetteki yetenek
// satırlarının (CosreCapabilities) altında seçilebilir somut seçenekler.
//
//	{groups:[{kind, fleet, items:[{chip, question, kind, scope?}]}], team, rangeS, env, envScope}
//
//   - services   — takımın servisleri hata oranına göre (ReadTeamServicesRED;
//     my_services ile aynı okuma) ya da takım yoksa filo sıcak noktaları
//     (TopErrorServices, fleet=true → FE "filo geneli" der). Hatalıysa
//     "<svc> son 1 saatte neden hata veriyor?" (root_cause), hatasızsa
//     "<svc> sağlığı nasıl?"; en kötü servisin en çok hata alan yolu
//     (endpoint_errors) korunur.
//   - operations — açık exception grupları (occurrences sırası): Oracle `ora:`
//     grubu → "<OP_KODU> operasyonu neden hata veriyor?" (katalog ön-getirmesi),
//     span grubu → "<svc> teknik hataları?" (service_exceptions).
//   - p1         — açık P1 problemleri → "<svc> kök nedeni ne?" + scope.problem
//     (kapsam kademesi problemin penceresinde root_cause koşar).
//   - deploys    — son 24 sa deploy'ları (takım ∩ ya da filo) → "<svc> son
//     deploy etkisi?" (deploy_impact).
//
// Bütçe: her grup KENDİ 3 s zaman aşımıyla paralel okunur ve tek başına
// düşer (soft-fail: o grup yok, diğerleri gelir). Ortam (env) YALNIZ P1
// problemlerine uygulanır (ProblemFilter.Env servis-üyeliği semantiği);
// servis/operasyon/deploy okumaları ortam boyutu taşımaz — yanıt bunu
// `envScope: "p1"` ile söyler.
//
// requireCopilot kapısı YOK — LLM kullanmıyor (config ucu gibi). Rol kapısı
// yok — viewer de görür. api.go/ai_routes.go BÜYÜMEZ: route defteri (init).
// Cache 60 s (guidedTeamCatalogue ile aynı tazelik); anahtar v2: kullanıcı
// kimliği (takım ondan türer) + pencere + env FNV özeti. MV okumaları, ES yok.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func init() { registerRoutesExtra("copilot_starters", (*Server).registerCopilotStarterRoutes) }

func (s *Server) registerCopilotStarterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/copilot/starters", s.copilotStarters)
}

// copilotStarter — bir veri çipi. Chip kısa etiket, Question gönderilen
// tam cümle (router'a giden metin), Kind FE'nin ikon/ton seçimi için.
// v0.10.1382 — Scope: çipin yapısal kapsamı (P1 → problem kimliği); FE
// followup çipleriyle aynı yoldan (parsed.scope) gönderir.
type copilotStarter struct {
	Chip     string     `json:"chip"`
	Question string     `json:"question"`
	Kind     string     `json:"kind"` // service_health | service_errors | endpoint_errors | operation | service_exceptions | p1 | deploy
	Scope    *chatScope `json:"scope,omitempty"`
}

// starterGroup — v0.10.1382: bir yetenek satırının seçenekleri. Fleet=true →
// takım çözülemedi, öğeler filo genelinden (FE etiketi "filo geneli").
type starterGroup struct {
	Kind  string           `json:"kind"` // services | operations | p1 | deploys
	Fleet bool             `json:"fleet,omitempty"`
	Items []copilotStarter `json:"items"`
}

const (
	startersRangeDefault = 3600
	startersRangeMin     = 300
	startersRangeMax     = 86400
	startersEndpointTopN = 5
	// v0.10.1382 — grup tavanları (operatör onaylı varsayılanlar).
	startersServicesMax   = 3
	startersOperationsMax = 3
	startersP1Max         = 3
	startersDeploysMax    = 2
	startersGroupTimeout  = 3 * time.Second
	startersExGroupLimit  = 5
	startersProblemLimit  = 50
	startersDeployLookbk  = 24 * time.Hour
	startersDeployLimit   = 50
	startersChipRunes     = 48
)

// startersKey — v0.10.1382 SAF (hash-all-inputs): kullanıcı (takım ondan
// türer) + pencere + env FNV özeti. v2: yanıt şekli gruplu, v1 kayıtları
// okunmasın.
func startersKey(userID string, rangeS int, env string) string {
	return fmt.Sprintf("copilot:starters:v2:u=%s:r=%d:e=%s", fnvStr(userID), rangeS, fnvStr(env))
}

// starterWindowTR — v0.10.1382 SAF: pencerenin router'ın tanıdığı Türkçe
// ifadesi ("son 1 saatte", "son 30 dakikada").
func starterWindowTR(rangeS int) string {
	if rangeS >= 3600 && rangeS%3600 == 0 {
		return fmt.Sprintf("son %d saatte", rangeS/3600)
	}
	return fmt.Sprintf("son %d dakikada", (rangeS+59)/60)
}

// starterServiceEligible — v0.10.1382 SAF: çip cümlesine girebilecek servis
// adı (boş değil, boşluksuz, sentetik önekli değil: `oracle:<kaynak>`,
// `db:…` özneleri router'da servis olarak çözülmez).
func starterServiceEligible(name string) bool {
	n := strings.TrimSpace(name)
	return n != "" && !strings.ContainsAny(n, " \t\n:")
}

// starterTrunc — SAF: çip etiketi tavanı (tam metin Question/title'da).
func starterTrunc(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// routeChipEligible — router'ın yol deseniyle uyumlu: "/" öneki, ≥3
// karakter, boşluksuz.
func routeChipEligible(path string) bool {
	p := strings.TrimSpace(path)
	return strings.HasPrefix(p, "/") && len(p) >= 3 && !strings.ContainsAny(p, " \t\n")
}

// worstTeamService — SAF: hata oranına göre sıralı listede hata TAŞIYAN
// ilk servis; hiçbiri hata taşımıyorsa nil.
func worstTeamService(rows []chstore.ServiceSummary) *chstore.ServiceSummary {
	for i := range rows {
		if rows[i].ErrorCount > 0 && starterServiceEligible(rows[i].Name) {
			return &rows[i]
		}
	}
	return nil
}

// worstEndpoint — SAF: hata-sıralı endpoint listesinde çip'e uygun ilk yol.
func worstEndpoint(rows []chstore.EndpointRow) *chstore.EndpointRow {
	for i := range rows {
		if rows[i].Errors > 0 && routeChipEligible(rows[i].Path) {
			return &rows[i]
		}
	}
	return nil
}

// starterServiceChips — v0.10.1382 SAF. rows hata oranına göre sıralı
// (takım RED'i ya da filo sıcak noktaları). İlk `max` uygun servis: hatalıysa
// root_cause cümlesi, hatasızsa service_health. route = en kötü servisin en
// çok hata alan yolu → ek endpoint_errors çipi (v0.10.702 çipi, tavanın
// DIŞINDA — tür farklı: servis değil yol). Tekrar eden ad tek çip.
func starterServiceChips(rows []chstore.ServiceSummary, route *chstore.EndpointRow, rangeS, max int) []copilotStarter {
	out := []copilotStarter{}
	seen := map[string]bool{}
	for _, r := range rows {
		if len(out) >= max {
			break
		}
		name := strings.TrimSpace(r.Name)
		if !starterServiceEligible(name) || seen[name] {
			continue
		}
		seen[name] = true
		if r.ErrorCount > 0 {
			out = append(out, copilotStarter{
				Chip:     starterTrunc(name, startersChipRunes),
				Question: name + " " + starterWindowTR(rangeS) + " neden hata veriyor?",
				Kind:     "service_errors",
			})
			continue
		}
		out = append(out, copilotStarter{
			Chip:     starterTrunc(name, startersChipRunes),
			Question: name + " sağlığı nasıl?",
			Kind:     "service_health",
		})
	}
	if len(out) > 0 && route != nil && route.Errors > 0 && routeChipEligible(route.Path) {
		out = append(out, copilotStarter{
			Chip:     starterTrunc(route.Path, startersChipRunes),
			Question: route.Path + " hatalı trace'lerini getir",
			Kind:     "endpoint_errors",
		})
	}
	return out
}

// starterOpCodeEligible — v0.10.1382 SAF: Oracle operasyon kodu çip olabilir
// mi — katalog eşleyicisinin belirteç kuralı (opTokens: ≥4 karakter,
// boşluksuz) ile aynı; aksi hâlde cümle ön-getirmeye düşmez.
func starterOpCodeEligible(op string) bool {
	o := strings.TrimSpace(op)
	return len([]rune(o)) >= opTokenMin && !strings.ContainsAny(o, " \t\n")
}

// starterOperationChips — v0.10.1382 SAF. groups occurrences sırasıyla açık
// exception grupları. Oracle grubu (`ora:`; ex_message = operasyon kodu) →
// operasyon cümlesi; span grubu → servisin teknik hataları (servis başına
// bir çip). Uygunsuz kod/servis atlanır.
func starterOperationChips(groups []chstore.ExceptionGroup, max int) []copilotStarter {
	out := []copilotStarter{}
	seen := map[string]bool{}
	for _, g := range groups {
		if len(out) >= max {
			break
		}
		var st copilotStarter
		if chstore.IsOracleGroup(g.Fingerprint) {
			op := strings.TrimSpace(g.Message)
			if !starterOpCodeEligible(op) {
				continue
			}
			st = copilotStarter{
				Chip:     starterTrunc(op, startersChipRunes),
				Question: op + " operasyonu neden hata veriyor?",
				Kind:     "operation",
			}
		} else {
			svc := strings.TrimSpace(g.Service)
			if !starterServiceEligible(svc) {
				continue
			}
			st = copilotStarter{
				Chip:     starterTrunc(svc, startersChipRunes) + " teknik hataları",
				Question: svc + " teknik hataları?",
				Kind:     "service_exceptions",
			}
		}
		if seen[st.Question] {
			continue
		}
		seen[st.Question] = true
		out = append(out, st)
	}
	return out
}

// starterP1Chips — v0.10.1382 SAF. probs zenginleştirilmiş (öncelik hesaplı)
// açık problemler; YALNIZ P1, servis başına bir çip (ilk = en yeni). Kapsam
// yalnız problem kimliği: kapsam kademesi problemin servisini ve penceresini
// kendisi çözer — servis adını kapsama koymak, son pencerede span'i olmayan
// servisi "servis yok" cevabına düşürürdü.
func starterP1Chips(probs []chstore.Problem, max int) []copilotStarter {
	out := []copilotStarter{}
	seen := map[string]bool{}
	for _, p := range chstore.FilterProblemsByPriority(probs, []string{"P1"}) {
		if len(out) >= max {
			break
		}
		svc := strings.TrimSpace(p.Service)
		if p.ID == "" || !starterServiceEligible(svc) || seen[svc] {
			continue
		}
		seen[svc] = true
		chip := svc
		if rn := strings.TrimSpace(p.RuleName); rn != "" {
			chip = svc + " · " + rn
		}
		out = append(out, copilotStarter{
			Chip:     starterTrunc(chip, startersChipRunes),
			Question: svc + " kök nedeni ne?",
			Kind:     "p1",
			Scope:    &chatScope{Problem: p.ID},
		})
	}
	return out
}

// starterDeployChips — v0.10.1382 SAF. deps en yeni önce (GetRecentDeploys);
// svcs nil = filo, aksi hâlde takım kümesiyle kesişim. Servis başına bir çip.
func starterDeployChips(deps []chstore.RecentDeployEntry, svcs []string, max int) []copilotStarter {
	var allow map[string]bool
	if svcs != nil {
		allow = make(map[string]bool, len(svcs))
		for _, s := range svcs {
			allow[s] = true
		}
	}
	out := []copilotStarter{}
	seen := map[string]bool{}
	for _, d := range deps {
		if len(out) >= max {
			break
		}
		svc := strings.TrimSpace(d.Service)
		if !starterServiceEligible(svc) || seen[svc] || (allow != nil && !allow[svc]) {
			continue
		}
		seen[svc] = true
		chip := svc
		if v := strings.TrimSpace(d.Version); v != "" {
			chip = svc + " → " + v
		}
		out = append(out, copilotStarter{
			Chip:     starterTrunc(chip, startersChipRunes),
			Question: svc + " son deploy etkisi?",
			Kind:     "deploy",
		})
	}
	return out
}

// starterGroups — v0.10.1382 SAF: sabit sırada grupları toplar; boş grup
// yanıta GİRMEZ (FE boş satır çizmez).
func starterGroups(fleet bool, services, operations, p1, deploys []copilotStarter) []starterGroup {
	out := []starterGroup{}
	for _, g := range []starterGroup{
		{Kind: "services", Fleet: fleet, Items: services},
		{Kind: "operations", Fleet: fleet, Items: operations},
		{Kind: "p1", Fleet: fleet, Items: p1},
		{Kind: "deploys", Fleet: fleet, Items: deploys},
	} {
		if len(g.Items) > 0 {
			out = append(out, g)
		}
	}
	return out
}

func (s *Server) copilotStarters(w http.ResponseWriter, r *http.Request) {
	claims := auth.FromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		writeJSON(w, map[string]any{"groups": []starterGroup{}})
		return
	}
	rangeS := parseInt(r.URL.Query().Get("range_s"), startersRangeDefault)
	if rangeS < startersRangeMin || rangeS > startersRangeMax {
		rangeS = startersRangeDefault
	}
	env := normalizeGreetingEnv(r.URL.Query().Get("env"))
	userID := claims.UserID
	s.serveCached(w, r, startersKey(userID, rangeS, env), 60*time.Second, func(ctx context.Context) (any, error) {
		team, svcs := s.starterTeamServices(ctx, userID)
		fleet := len(svcs) == 0
		var scope []string // nil = filo
		if !fleet {
			scope = svcs
		}
		to := time.Now()
		from := to.Add(-time.Duration(rangeS) * time.Second)

		var services, operations, p1, deploys []copilotStarter
		var wg sync.WaitGroup
		run := func(dst *[]copilotStarter, read func(context.Context) []copilotStarter) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				gctx, cancel := context.WithTimeout(ctx, startersGroupTimeout)
				defer cancel()
				*dst = read(gctx)
			}()
		}
		run(&services, func(c context.Context) []copilotStarter { return s.starterServicesRead(c, scope, from, to, rangeS) })
		run(&operations, func(c context.Context) []copilotStarter {
			gs, err := s.store.ListExceptionGroups(c, chstore.ExceptionGroupFilter{
				State: "open", Services: scope, ActiveFromNs: from.UnixNano(),
				Sort: "occurrences", Dir: "desc", Limit: startersExGroupLimit,
			})
			if err != nil {
				return nil
			}
			return starterOperationChips(gs, startersOperationsMax)
		})
		run(&p1, func(c context.Context) []copilotStarter {
			// Öncelik okuma anında hesaplanır (SQL'e inmez): P1 daraltması
			// LIMIT'ten SONRA → tarama ProblemScanLimit ile açılır (CHECK 8 sınıfı).
			probs, err := s.store.ListProblems(c, chstore.ProblemFilter{
				Status: "open", Services: scope, Env: env,
				Limit: chstore.ProblemScanLimit(startersProblemLimit, true),
			})
			if err != nil || len(probs) == 0 {
				return nil
			}
			return starterP1Chips(s.enrichProblemsForRead(c, probs), startersP1Max)
		})
		run(&deploys, func(c context.Context) []copilotStarter {
			deps, err := s.store.GetRecentDeploys(c, startersDeployLookbk, startersDeployLimit)
			if err != nil {
				return nil
			}
			return starterDeployChips(deps, scope, startersDeploysMax)
		})
		wg.Wait()
		return map[string]any{
			"groups":   starterGroups(fleet, services, operations, p1, deploys),
			"team":     team,
			"rangeS":   rangeS,
			"env":      env,
			"envScope": "p1", // ortam yalnız P1 problemlerine uygulanır
		}, nil
	})
}

// starterTeamServices — v0.10.1382: kullanıcının takımı ve servisleri.
// Okuma düşerse ya da takım/servis yoksa boş küme → filo yolu (soft-fail;
// FE "filo geneli" etiketiyle dürüst kalır).
func (s *Server) starterTeamServices(ctx context.Context, userID string) (string, []string) {
	u, err := s.store.GetUserByID(ctx, userID)
	if err != nil || u == nil || u.Team == "" {
		return "", nil
	}
	mds, err := s.store.ListServiceMetadata(ctx)
	if err != nil {
		return u.Team, nil
	}
	svcs := servicesForUserTeam(s.teamAliasesCtx(ctx), mds, u.Team)
	if len(svcs) > maxTeamServices {
		svcs = svcs[:maxTeamServices]
	}
	return u.Team, svcs
}

// starterServicesRead — v0.10.1382: takım RED'i (hata oranı sırası) ya da
// filo sıcak noktaları; en kötü hatalı servisin en çok hata alan yolu.
func (s *Server) starterServicesRead(ctx context.Context, scope []string, from, to time.Time, rangeS int) []copilotStarter {
	var rows []chstore.ServiceSummary
	if scope != nil {
		red, err := mcptools.ReadTeamServicesRED(ctx, s.mcpDeps(), scope, from, to)
		if err != nil {
			return nil
		}
		mcptools.SortServicesByErrorRate(red)
		rows = red
	} else {
		ranks, err := s.store.TopErrorServices(ctx, from, to, nil, startersServicesMax)
		if err != nil {
			return nil
		}
		for _, rk := range ranks {
			rows = append(rows, chstore.ServiceSummary{Name: rk.Name, SpanCount: rk.Spans, ErrorCount: rk.Errors})
		}
	}
	var route *chstore.EndpointRow
	if worst := worstTeamService(rows); worst != nil {
		eps, err := s.store.GetEndpointsMV(ctx, chstore.EndpointsQuery{
			From: from, To: to, Service: worst.Name,
			Limit: startersEndpointTopN, Sort: "errors", Dir: "desc",
		})
		if err == nil {
			route = worstEndpoint(eps)
		}
	}
	return starterServiceChips(rows, route, rangeS, startersServicesMax)
}
