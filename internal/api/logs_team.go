package api

// logs_team.go — v0.10.1477 (operatör 2026-10-23: "Uygulama takımımı da
// karşılamak isterim; hatta mümkünse giren kişinin takımına göre logs sayfası
// filtrelensin otomatik.").
//
//	GET /api/logs/my-team   → {team, available, fields}
//
// İki parça:
//
//  1. `team=<takım>` — /logs okumalarının (`/api/logs`, `/api/logs/search`,
//     `/api/logs/timeseries`, `/api/logs/fieldstats`, `/api/logs/patterns`,
//     `/api/logs/stream`) BİRİNCİ SINIF süzgeci (`pattern=` emsali, yeni rota
//     yok). Ham q metni DEĞİL: sunucu takımı kanonik takıma düşen TÜM ham
//     yazımlara açar (teamLogLabelValues — v0.10.1473 CoSRE son-log okumasıyla
//     AYNI genişletme, ≤10 değer) ve logstore.Filter.TeamValues'a koyar; ES
//     yapılandırılmış takım alan(lar)ı × değerler üzerinde exact-term OR uygular
//     (teamValueClause — wildcard / query_string YOK). Alan boşsa ya da CH
//     backend'inde süzgeç uygulanmaz ve yanıt unappliedFilters:["team"] der
//     (env/cluster çiplerinin dürüstlük sözleşmesi). Önbellek anahtarları takımı
//     VE çözülmüş değerlerin özetini taşır (v0.5.187 — alias değişince eski
//     değer kümesinin yanıtı yeni kümeye servis edilmesin).
//
//  2. /api/logs/my-team — giriş yapan kullanıcının takımı (users.team; CoSRE
//     starters'ın `team`'iyle aynı kaynak) + özelliğin açık olup olmadığı
//     (takım alanı yapılandırılmış VE log backend'i Elasticsearch). FE bunu
//     /logs'un otomatik varsayılanı için okur: URL'de süzgeç yoksa bir kez
//     `team=` yazar (replace). Özellik kapalıysa kullanıcı satırı HİÇ okunmaz.
//     Rol kapısı YOK — viewer da kendi takımını görür; kimliksiz → boş takım.
//     60 s önbellek, anahtar: kullanıcı kimliği + alanlar + backend.
//
// api.go BÜYÜMEZ (TestApiGoDoesNotGrow): rota bu dosyada, kayıt init()'te.

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	// logsTeamMaxLen — `team=` değeri tavanı (takım adları kısa; sınırsız
	// değer sınırsız önbellek girdisi basmasın).
	logsTeamMaxLen = 128
	// logsTeamResolveTimeout — takım → ham yazımlar çözümü (katalog süreç-içi
	// önbellekli, alias tek satır okuma); düşerse yalnız istenen ad.
	logsTeamResolveTimeout = 2 * time.Second
	logsMyTeamTTL          = 60 * time.Second
)

func init() { registerRoutesExtra("logs_team", (*Server).registerLogsTeamRoutes) }

func (s *Server) registerLogsTeamRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/logs/my-team", s.getLogsMyTeam)
}

// logsTeamFromQuery — SAF: `team` değeri doğrulaması. Boş → ("", true):
// takım süzgeci yok. Kontrol karakteri / tavan üstü → ("", false): çağıran
// 400 döner (sessizce takımsız liste "takımın logları bunlar" yalanı olurdu).
func logsTeamFromQuery(raw string) (string, bool) {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "", true
	}
	if len(t) > logsTeamMaxLen {
		return "", false
	}
	for _, r := range t {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return t, true
}

// applyLogsTeamParam — `team` parametresini Filter'a işler: doğrular, ham
// yazımlara açar. Geçersiz değer → 400 JSON ve false. Önbellek anahtarından
// ÖNCE çağrılır (anahtar Team + TeamValues özetini taşır).
func (s *Server) applyLogsTeamParam(w http.ResponseWriter, r *http.Request, q url.Values, f *logstore.Filter) bool {
	team, ok := logsTeamFromQuery(q.Get("team"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "geçersiz takım değeri")
		return false
	}
	if team == "" {
		return true
	}
	f.Team = team
	f.TeamValues = s.resolveLogsTeamValues(r.Context(), team)
	return true
}

// resolveLogsTeamValues — takım alanı yapılandırılmamışsa çözüm YAPILMAZ
// (süzgeç zaten uygulanmayacak; yalnız istenen ad — unapplied bayrağı için
// yeter). Aksi hâlde CoSRE son-log okumasının çözümü (katalog + alias).
func (s *Server) resolveLogsTeamValues(ctx context.Context, team string) []string {
	if len(s.teamLogFields()) == 0 {
		return []string{team}
	}
	c, cancel := context.WithTimeout(ctx, logsTeamResolveTimeout)
	defer cancel()
	vals := s.resolveTeamLogValues(c, team)
	if len(vals) == 0 {
		return []string{team}
	}
	return vals
}

// logsTeamKey — SAF: önbellek anahtarının takım parçası. Takımsız istek ""
// (bugünkü anahtarlar BAYT-BAYT aynı kalır); takımlı istek takım adı + sıralı
// değerlerin FNV özeti — alias/katalog değişince değer kümesi değişir, eski
// kümenin yanıtı servis edilmez.
func logsTeamKey(f logstore.Filter) string {
	vals := logstore.TeamFilterValues(f)
	if len(vals) == 0 {
		return ""
	}
	return ":team=" + fnvStr(append([]string{f.Team, "\x02"}, logsTeamSorted(vals)...)...)
}

func logsTeamSorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// logsMyTeamAvailable — özellik açık mı: takım alanı yapılandırılmış VE okuma
// backend'i Elasticsearch (alan ayarı ES belge yoludur; CH'de süzgeç
// uygulanamaz → otomatik varsayılan hiç devreye girmez, sayfa bugünkü gibi).
func (s *Server) logsMyTeamAvailable() ([]string, bool) {
	fields := s.teamLogFields()
	if len(fields) == 0 || s.logs == nil || s.logs.Backend() != "elasticsearch" {
		return fields, false
	}
	return fields, true
}

// logsMyTeamKey — SAF: kullanıcı kimliği + alanlar + backend (hash-all-inputs).
func logsMyTeamKey(userID string, fields []string, backend string) string {
	return "logs-myteam:v1:" + fnvStr(append([]string{userID, backend, "\x02"}, fields...)...)
}

type logsMyTeamPayload struct {
	Team      string   `json:"team"`
	Available bool     `json:"available"`
	Fields    []string `json:"fields"`
}

func (s *Server) getLogsMyTeam(w http.ResponseWriter, r *http.Request) {
	fields, available := s.logsMyTeamAvailable()
	if fields == nil {
		fields = []string{}
	}
	claims := auth.FromContext(r.Context())
	if !available || claims == nil || claims.UserID == "" || s.store == nil {
		// Özellik kapalı / kimliksiz: kullanıcı satırı OKUNMAZ, sayfa bugünkü gibi.
		writeJSON(w, logsMyTeamPayload{Team: "", Available: available, Fields: fields})
		return
	}
	userID := claims.UserID
	backend := s.logs.Backend()
	s.serveCached(w, r, logsMyTeamKey(userID, fields, backend), logsMyTeamTTL, func(ctx context.Context) (any, error) {
		u, err := s.store.GetUserByID(ctx, userID)
		if err != nil {
			return nil, err // önbelleğe girmez; FE takımsız davranır
		}
		team := ""
		if u != nil {
			team = strings.TrimSpace(u.Team)
		}
		return logsMyTeamPayload{Team: team, Available: true, Fields: fields}, nil
	})
}
