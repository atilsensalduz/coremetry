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
//	{groups:[{kind, fleet, items:[{chip, title?, question, kind, explainAsk?}]}], team, env, envScope}
//
//   - exceptions — v0.10.1404: en son AÇIK exception grupları (aşağıda).
//
// v0.10.1404 (operatör, v0.10.1402 sonrası prod ekranı: "P1 yerine en son
// exception'lara yönlendirsin.") — `p1` grubu (açık P1 problemleri → "<svc>
// kök nedeni ne?" + scope.problem) KALDIRILDI; yerinde `exceptions` grubu:
// Exceptions kutusunun (exception_groups — küçük ReplacingMergeTree durum
// tablosu, FINAL + LIMIT + max_execution_time; ham span YOK) AÇIK grupları
// (new/acknowledged/regressed; ignored/resolved DEĞİL), son görülme yeni
// önce, en çok 3. Kapsam: kullanıcının takımı çözüldüyse takımın servisleri
// (`service IN (…)`, Exceptions sayfasının owner/sre süzgeciyle aynı yol),
// değilse filo ("filo geneli"). Süzgeç takım exception cevabının
// (readTeamExceptions — "Tüm son exception'lar" çipinin cevabı) AYNISI: son
// 24 sa, sayfanın varsayılan tabanı (çoklu-servis + regressed muaf), env ∩
// servisler — çipler o cevabın listesinin başıdır. Örnek okuması YOK (çip
// yalnız etiket + parmak izi taşır). Öğe = açıklama niyeti: `explainAsk`
// {kind: exception, id: <parmak izi>, src: starter} — FE tıkta sohbet içi
// açıklama turu başlatır (v0.10.1400 yapısal yolu; router'a gitmez), etiket
// kısa ("ORDERS_GET_X · orders-api" / "SocketTimeoutException · orders-api"),
// `title` tam.
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
// Bütçe: exceptions grubu KENDİ 3 s zaman aşımıyla okunur ve düşerse grup yok
// (soft-fail). Ortam (env) exception grubunun servis kümesine uygulanır
// (Exceptions sayfasıyla aynı kural: kapsam ∩ ortam üyeleri; üyelik okunamazsa
// daraltma yok) — yanıt bunu `envScope: "exceptions"` ile söyler (v0.10.1404).
//
// requireCopilot kapısı YOK — LLM kullanmıyor (config ucu gibi). Rol kapısı
// yok — viewer de görür. api.go/ai_routes.go BÜYÜMEZ: route defteri (init).
// Cache 60 s (guidedTeamCatalogue ile aynı tazelik); anahtar v5: kullanıcı
// kimliği (takım ondan türer) + env FNV özeti. MV / durum tabloları, ES yok.

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("copilot_starters", (*Server).registerCopilotStarterRoutes) }

func (s *Server) registerCopilotStarterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/copilot/starters", s.copilotStarters)
}

// copilotStarter — bir veri çipi. Chip kısa etiket, Question kullanıcı turunun
// metni, Kind FE'nin ikon/ton seçimi için.
// v0.10.1404 — p1 öğesinin Scope'u (problem kimliği) grupla birlikte kalktı;
// exception öğesi Title (tam etiket) + ExplainAsk (yapısal açıklama niyeti,
// chat_followup_chips.go açıklama çipiyle aynı şekil) taşır.
type copilotStarter struct {
	Chip       string          `json:"chip"`
	Title      string          `json:"title,omitempty"`
	Question   string          `json:"question"`
	Kind       string          `json:"kind"` // exception (v0.10.1404: p1 kalktı)
	ExplainAsk *chatExplainAsk `json:"explainAsk,omitempty"`
}

// starterGroup — v0.10.1382: bir yetenek satırının seçenekleri. Fleet=true →
// takım çözülemedi, öğeler filo genelinden (FE etiketi "filo geneli").
type starterGroup struct {
	Kind  string           `json:"kind"` // exceptions (v0.10.1404: p1 kalktı)
	Fleet bool             `json:"fleet,omitempty"`
	Items []copilotStarter `json:"items"`
}

const (
	// v0.10.1382 — grup bütçesi ve çip etiketi tavanı (operatör onaylı varsayılanlar).
	startersGroupTimeout = 3 * time.Second
	startersChipRunes    = 48
	// v0.10.1404 — exceptions grubu: en çok 3 çip; CH okuması 10 satır (durum /
	// parmak izi elemesi ve kopyalar için pay — exception_groups küçük durum
	// tablosu, FINAL + LIMIT + max_execution_time); pencere takım exception
	// cevabının varsayılanı (teamExcDefaultRangeS, 24 sa).
	startersExcMax       = 3
	startersExcReadLimit = 10
	startersExcLookback  = teamExcDefaultRangeS * time.Second
	starterKindException = "exception"
	starterGroupExc      = "exceptions"
	starterExplainSrc    = "starter"
)

// startersKey — v0.10.1382 SAF (hash-all-inputs): kullanıcı (takım ondan
// türer) + env FNV özeti. v2: yanıt şekli gruplu. v0.10.1390 — v3: servis/
// operasyon grupları kalktı (v2 kayıtları okunmasın) ve pencere girdisi yok.
// v0.10.1402 — v4: deploys grubu kalktı (v3 kayıtları deploy çipi taşır).
// v0.10.1404 — v5: p1 grubu yerine exceptions (v4 kayıtları P1 çipi taşır).
func startersKey(userID, env string) string {
	return fmt.Sprintf("copilot:starters:v5:u=%s:e=%s", fnvStr(userID), fnvStr(env))
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

// starterExcOpen — v0.10.1404 SAF: Exceptions kutusunun "açık" kovası
// (buildExceptionGroupWhere State:"open" ile aynı küme). SQL zaten süzer; Go
// tarafı yanıtın sözleşmesini okuyucudan bağımsız tutar.
func starterExcOpen(state string) bool {
	switch state {
	case chstore.ExStateNew, chstore.ExStateAcknowledged, chstore.ExStateRegressed:
		return true
	}
	return false
}

// starterExcShortType — v0.10.1404 SAF: exception tipinin kısa adı (son nokta
// sonrası; Go'nun `*pkg.Err` yıldızı düşer). "java.net.SocketTimeoutException"
// → "SocketTimeoutException"; nokta yoksa tip aynen (HTTP "503" gibi).
func starterExcShortType(typ string) string {
	t := strings.TrimSpace(typ)
	if i := strings.LastIndex(t, "."); i >= 0 && i < len(t)-1 {
		t = t[i+1:]
	}
	return strings.TrimLeft(t, "*")
}

// starterExcLabels — v0.10.1404 SAF: grup satırının (exGroupLineFrom) kısa çip
// etiketi ve tam başlığı. Oracle: "OP_CODE · servis" / "KOD · OP_CODE ·
// servisler"; span: "KısaTip · servis" / "tam.Tip · servis". Servis yoksa
// yalnız ad; ad da yoksa "exception".
func starterExcLabels(l exGroupLine) (chip, title string) {
	join := func(parts ...string) string {
		var keep []string
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				keep = append(keep, p)
			}
		}
		return strings.Join(keep, " · ")
	}
	first := ""
	if len(l.Services) > 0 {
		first = l.Services[0]
	}
	all := strings.Join(l.Services, ", ")
	if l.ServiceCount > len(l.Services) {
		all += fmt.Sprintf(" (+%d)", l.ServiceCount-len(l.Services))
	}
	if l.Oracle {
		name := strings.TrimSpace(l.Operation)
		if name == "" {
			name = l.Code
		}
		chip, title = join(name, first), join(l.Code, l.Operation, all)
	} else {
		chip, title = join(starterExcShortType(l.Type), first), join(l.Type, all)
	}
	if chip == "" {
		chip = "exception"
	}
	if title == "" {
		title = chip
	}
	return chip, title
}

// starterExceptionChips — v0.10.1404 SAF: okunan exception grupları → en çok
// max açıklama çipi. Yalnız AÇIK durumlar, geçerli parmak izi (açıklama
// kademesinin kabul ettiği biçim — normalizeExplainAsk), parmak izi başına
// bir çip; son görülme YENİ önce (eşitlikte parmak izi — SQL sırasıyla aynı).
func starterExceptionChips(rows []chstore.ExceptionGroup, h exGroupHooks, max int) []copilotStarter {
	open := make([]chstore.ExceptionGroup, 0, len(rows))
	seen := map[string]bool{}
	for _, g := range rows {
		if !starterExcOpen(g.State) || !explainFingerprintOK(g.Fingerprint) || seen[g.Fingerprint] {
			continue
		}
		seen[g.Fingerprint] = true
		open = append(open, g)
	}
	sort.SliceStable(open, func(i, j int) bool {
		if open[i].LastSeen != open[j].LastSeen {
			return open[i].LastSeen > open[j].LastSeen
		}
		return open[i].Fingerprint < open[j].Fingerprint
	})
	out := []copilotStarter{}
	for _, g := range open {
		if len(out) >= max {
			break
		}
		chip, title := starterExcLabels(exGroupLineFrom(g, h))
		out = append(out, copilotStarter{
			Chip:       starterTrunc(chip, startersChipRunes),
			Title:      title,
			Question:   chip + " grubunu açıkla",
			Kind:       starterKindException,
			ExplainAsk: &chatExplainAsk{Kind: explainAskException, ID: g.Fingerprint, Src: starterExplainSrc},
		})
	}
	return out
}

// starterExceptionScope — v0.10.1404 SAF: grubun servis kümesi. scope nil =
// filo. env boşsa kapsam aynen; üyelik okunamadıysa (membersOK=false)
// daraltma YOK (Exceptions sayfasının soft-fail duruşu); okunduysa kapsam ∩
// üyeler. skip=true → küme BOŞ (boş `Services` SQL'de "kısıt yok" demektir —
// filoyu döndürmek yerine grup yok).
func starterExceptionScope(scope []string, env string, members []string, membersOK bool) (svcs []string, skip bool) {
	if strings.TrimSpace(env) == "" || !membersOK {
		return scope, false
	}
	if members == nil {
		members = []string{}
	}
	svcs = intersectServices(scope, members)
	return svcs, len(svcs) == 0
}

// starterExceptionFilter — v0.10.1404 SAF: takım exception cevabının
// (readTeamExceptions) süzgeci — açık durumlar, son görülme yeni önce,
// [now-24 sa, now] pencere örtüşmesi, sayfanın varsayılan tabanı ve muafları.
func starterExceptionFilter(svcs []string, floor uint64, exempt []string, now time.Time) chstore.ExceptionGroupFilter {
	f := chstore.ExceptionGroupFilter{
		State: "open", Services: svcs, Sort: "lastSeen", Dir: "desc", Limit: startersExcReadLimit,
		ActiveFromNs: now.Add(-startersExcLookback).UnixNano(), ActiveToNs: now.UnixNano(),
		MinOccurrences: floor,
	}
	if floor > 0 {
		f.FloorExempt, f.FloorExemptRegressed = exempt, true
	}
	return f
}

// starterExcReader — v0.10.1404: okuma yüzü (*chstore.Store; testte sahte).
type starterExcReader interface {
	ListExceptionGroups(ctx context.Context, f chstore.ExceptionGroupFilter) ([]chstore.ExceptionGroup, error)
}

// readStarterExceptions — v0.10.1404: TEK okuma, kendi zaman aşımıyla;
// hata / zaman aşımı → nil (soft-fail: grup yok, yanıt düşmez).
func readStarterExceptions(ctx context.Context, rd starterExcReader, f chstore.ExceptionGroupFilter, h exGroupHooks, timeout time.Duration) []copilotStarter {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rows, err := rd.ListExceptionGroups(c, f)
	if err != nil || len(rows) == 0 {
		return nil
	}
	return starterExceptionChips(rows, h, startersExcMax)
}

// starterGroups — v0.10.1382 SAF: boş grup yanıta GİRMEZ (FE boş satır
// çizmez). v0.10.1404 — yalnız exceptions grubu (p1 kalktı).
func starterGroups(fleet bool, exc []copilotStarter) []starterGroup {
	if len(exc) == 0 {
		return []starterGroup{}
	}
	return []starterGroup{{Kind: starterGroupExc, Fleet: fleet, Items: exc}}
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

		// v0.10.1404 — tek grup (exceptions): en son açık exception grupları.
		// Ortam üyeliği, yayılım (taban muafları) ve liste AYNI 3 s bütçede.
		exc := func() []copilotStarter {
			c, cancel := context.WithTimeout(ctx, startersGroupTimeout)
			defer cancel()
			var members []string
			membersOK := false
			if env != "" {
				m, err := s.store.EnvMemberServices(c, env)
				members, membersOK = m, err == nil
			}
			svcs, skip := starterExceptionScope(scope, env, members, membersOK)
			if skip {
				return nil
			}
			floor := effectiveDefaultFloor(currentExceptionTriage())
			f := starterExceptionFilter(svcs, floor, s.exceptionSpread(c).ExemptBelow(floor), time.Now())
			return readStarterExceptions(c, s.store, f, exGroupHooks{
				oracleInfo: func(g chstore.ExceptionGroup) *chstore.OracleGroupInfo {
					st, ok := oracleGroupStats(g)
					return oracleGroupInfoFrom(g, st, ok, exGroupServicesMax)
				},
			}, startersGroupTimeout)
		}()
		return map[string]any{
			"groups":   starterGroups(fleet, exc),
			"team":     team,
			"env":      env,
			"envScope": starterGroupExc, // v0.10.1404 — ortam exception grubunun servis kümesine uygulanır
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
