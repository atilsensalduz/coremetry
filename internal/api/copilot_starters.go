package api

// copilot_starters.go — v0.10.702 (operatör isteği 2026-09-12: "ilk sohbetteki
// öneriler zenginleşsin — takımımın servislerine ek olarak belirli bir
// endpoint'te hata alan trace'ler").
//
//	GET /api/copilot/starters?env=prod   (v0.10.1390: range_s kalktı)
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
//	{groups:[{kind, fleet, items:[{chip, question, kind, scope?}]}], team, env, envScope}
//
//   - p1         — açık P1 problemleri → "<svc> kök nedeni ne?" + scope.problem
//     (kapsam kademesi problemin penceresinde root_cause koşar).
//
// v0.10.1402 (operatör: "Son deploy etkisini kaldırabiliriz") — `deploys`
// grubu (son 24 sa deploy'ları → "<svc> son deploy etkisi?") KALDIRILDI:
// başlangıç ekranındaki deploy kartının tek kaynağıydı. deploy_impact
// rotası sohbette aynen duruyor; yalnız başlangıç seçeneği gitti. Yanıtın
// `team` alanı artık FE'deki Takımım kartının varsayılan takımı.
//
// v0.10.1390 (operatör: "Burada filo geneli ve böyle birçok servis yazmasın,
// search kısmı çıksa yeter. Aynısı hata veren teknik operasyonlar için de
// geçerli, çok karışık gösteriyor.") — `services` ve `operations` grupları
// KALDIRILDI: servis satırında her zaman görünen ServicePicker, operasyon
// satırında katalog araması (copilot_operation_search.go) var; o grupların
// okumaları (takım RED'i / filo sıcak noktaları + en kötü yol, açık exception
// grupları) artık yapılmaz. Pencere (range_s) yalnız o grupların girdisiydi —
// p1 ona bakmaz; parametre ve anahtar boyutu da kalktı (v3).
//
// Bütçe: P1 grubu KENDİ 3 s zaman aşımıyla okunur ve düşerse grup yok
// (soft-fail). Ortam (env) YALNIZ P1 problemlerine uygulanır
// (ProblemFilter.Env servis-üyeliği semantiği) — yanıt bunu `envScope: "p1"`
// ile söyler.
//
// requireCopilot kapısı YOK — LLM kullanmıyor (config ucu gibi). Rol kapısı
// yok — viewer de görür. api.go/ai_routes.go BÜYÜMEZ: route defteri (init).
// Cache 60 s (guidedTeamCatalogue ile aynı tazelik); anahtar v3: kullanıcı
// kimliği (takım ondan türer) + env FNV özeti. MV / durum tabloları, ES yok.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
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
	Kind     string     `json:"kind"` // p1 (v0.10.1390: servis/operasyon, v0.10.1402: deploy türü kalktı)
	Scope    *chatScope `json:"scope,omitempty"`
}

// starterGroup — v0.10.1382: bir yetenek satırının seçenekleri. Fleet=true →
// takım çözülemedi, öğeler filo genelinden (FE etiketi "filo geneli").
type starterGroup struct {
	Kind  string           `json:"kind"` // p1 (v0.10.1402: deploys kalktı)
	Fleet bool             `json:"fleet,omitempty"`
	Items []copilotStarter `json:"items"`
}

const (
	// v0.10.1382 — grup tavanları (operatör onaylı varsayılanlar).
	startersP1Max        = 3
	startersGroupTimeout = 3 * time.Second
	startersProblemLimit = 50
	startersChipRunes    = 48
)

// startersKey — v0.10.1382 SAF (hash-all-inputs): kullanıcı (takım ondan
// türer) + env FNV özeti. v2: yanıt şekli gruplu. v0.10.1390 — v3: servis/
// operasyon grupları kalktı (v2 kayıtları okunmasın) ve pencere girdisi yok.
// v0.10.1402 — v4: deploys grubu kalktı (v3 kayıtları deploy çipi taşır).
func startersKey(userID, env string) string {
	return fmt.Sprintf("copilot:starters:v4:u=%s:e=%s", fnvStr(userID), fnvStr(env))
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

// starterOpCodeEligible — v0.10.1382 SAF: Oracle operasyon kodu çip olabilir
// mi — katalog eşleyicisinin belirteç kuralı (opTokens: ≥4 karakter,
// boşluksuz) ile aynı; aksi hâlde cümle ön-getirmeye düşmez.
func starterOpCodeEligible(op string) bool {
	o := strings.TrimSpace(op)
	return len([]rune(o)) >= opTokenMin && !strings.ContainsAny(o, " \t\n")
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

// starterGroups — v0.10.1382 SAF: boş grup yanıta GİRMEZ (FE boş satır
// çizmez). v0.10.1402 — yalnız p1 grubu (deploys kalktı).
func starterGroups(fleet bool, p1 []copilotStarter) []starterGroup {
	if len(p1) == 0 {
		return []starterGroup{}
	}
	return []starterGroup{{Kind: "p1", Fleet: fleet, Items: p1}}
}

func (s *Server) copilotStarters(w http.ResponseWriter, r *http.Request) {
	claims := auth.FromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		writeJSON(w, map[string]any{"groups": []starterGroup{}})
		return
	}
	env := normalizeGreetingEnv(r.URL.Query().Get("env"))
	userID := claims.UserID
	s.serveCached(w, r, startersKey(userID, env), 60*time.Second, func(ctx context.Context) (any, error) {
		team, svcs := s.starterTeamServices(ctx, userID)
		fleet := len(svcs) == 0
		var scope []string // nil = filo
		if !fleet {
			scope = svcs
		}

		// v0.10.1402 — tek grup (p1): paralel koşturucu ve deploy okuması kalktı.
		p1 := func() []copilotStarter {
			c, cancel := context.WithTimeout(ctx, startersGroupTimeout)
			defer cancel()
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
		}()
		return map[string]any{
			"groups":   starterGroups(fleet, p1),
			"team":     team,
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
