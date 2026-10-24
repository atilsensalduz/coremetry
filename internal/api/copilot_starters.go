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
//
// v0.10.1515 (operatör 2026-10-24: "Takımının son exception'larına
// bakabilirim" kartında "filo geneli" ve son exception çipleri kalksın,
// yerine takım seçici; seçim Exceptions sayfasına takım süzgeçli gider) —
// `exceptions` grubu KALDIRILDI: grubun tek tüketicisi o kartın çipleriydi.
// Uç artık CH'ye GİTMEZ (exception_groups / env üyeliği / yayılım okumaları
// yok); yalnız kullanıcının takımını döner (Takımım ve exceptions kartlarının
// varsayılan takımı). `groups` alanı geriye dönük uyum için hep boş dizi.
// Anahtar v6 (v5 kayıtları exception çipi taşır).

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("copilot_starters", (*Server).registerCopilotStarterRoutes) }

func (s *Server) registerCopilotStarterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/copilot/starters", s.copilotStarters)
}

// starterGroup — v0.10.1382: bir yetenek satırının seçenekleri. v0.10.1515 —
// sunucu artık grup üretmez; şekil yanıtın `groups: []` alanı için kalır.
type starterGroup struct {
	Kind  string `json:"kind"`
	Fleet bool   `json:"fleet,omitempty"`
	Items []any  `json:"items"`
}

// startersKey — v0.10.1382 SAF (hash-all-inputs): kullanıcı (takım ondan
// türer) + env FNV özeti. v2: yanıt şekli gruplu. v0.10.1390 — v3: servis/
// operasyon grupları kalktı (v2 kayıtları okunmasın) ve pencere girdisi yok.
// v0.10.1402 — v4: deploys grubu kalktı (v3 kayıtları deploy çipi taşır).
// v0.10.1404 — v5: p1 grubu yerine exceptions (v4 kayıtları P1 çipi taşır).
// v0.10.1515 — v6: exceptions grubu kalktı (v5 kayıtları exception çipi taşır).
func startersKey(userID, env string) string {
	return fmt.Sprintf("copilot:starters:v6:u=%s:e=%s", fnvStr(userID), fnvStr(env))
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

func (s *Server) copilotStarters(w http.ResponseWriter, r *http.Request) {
	claims := auth.FromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		writeJSON(w, map[string]any{"groups": []starterGroup{}})
		return
	}
	env := normalizeGreetingEnv(r.URL.Query().Get("env"))
	userID := claims.UserID
	s.serveCached(w, r, startersKey(userID, env), 60*time.Second, func(ctx context.Context) (any, error) {
		return map[string]any{
			"groups": []starterGroup{},
			"team":   s.starterUserTeam(ctx, userID),
			"env":    env,
		}, nil
	})
}

// starterUserTeam — v0.10.1402: kullanıcının takımı (users tablosu, tek satır).
// Okuma düşerse ya da takım yoksa boş (soft-fail: kartlar takımsız açılır).
// v0.10.1515 — servis kümesi artık gerekmiyor (exceptions grubu kalktı).
func (s *Server) starterUserTeam(ctx context.Context, userID string) string {
	u, err := s.store.GetUserByID(ctx, userID)
	if err != nil || u == nil {
		return ""
	}
	return strings.TrimSpace(u.Team)
}
