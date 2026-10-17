package mcptools

// screen_scope.go — v0.10.1274 (CoSRE adım 5: sayfa bağlamı + env araçlara).
//
// İki parça, ikisi de sohbetin araç katmanına EKRAN KAPSAMINI taşır:
//
//  1. ScopeApplied — sunucunun bir araç çağrısına ekrandan (Topbar env,
//     servis sayfası, zaman aralığı, seçili trace) KENDİSİNİN koyduğu
//     değerlerin künyesi. api/chat_tool_scope.go applyScreenContext üretir;
//     step-result.ctxApplied ve kanıt çipi (ChatEvidence.CtxApplied) taşır —
//     operatör hangi kapsamın kullanıldığını görür.
//
//  2. EnvBlindReason — env argümanı OLMAYAN araçların kayıt defteri ve
//     sebebi. Ekranda somut bir env seçiliyken bu araçların sonucuna
//     "env uygulanmadı: <sebep>" notu eklenir; model sonucu env'e
//     daraltılmış sanıp öyle anlatmasın. tools_test.go'daki env-kör
//     ratchet'i BU haritadan okur: liste ile gerekçe tek yerde.
//
// Ek olarak servis-üyeliği env kapsamı (resolveEnvMembers): env boyutu
// taşımayan durum tabloları (anomaly_events, problems, deploy işaretçileri)
// /problems'ın v0.8.387 semantiğiyle daraltılır — "bu ortamda KOŞAN
// servislerin satırları". Sonuç bunu `env_scope: "service_membership"` ile
// açıkça söyler; sayılar ortam-kırılımlı DEĞİLDİR.

import (
	"context"
	"errors"
	"strings"
)

// ScopeApplied — ekrandan uygulanan / korunan / uygulanamayan kapsam.
// Tüm alanlar omitempty: boş künye hiç yazılmaz (eski istemci aynen).
type ScopeApplied struct {
	// Sunucunun argümana EKLEDİĞİ değerler (model vermemişti).
	Env     string `json:"env,omitempty"`
	Service string `json:"service,omitempty"`
	RangeS  int    `json:"rangeS,omitempty"`
	TraceID string `json:"traceId,omitempty"`
	// Model ekrandakinden FARKLI bir değeri açıkça verdi: modelinki kaldı
	// (operatör başka ortamı sormuş olabilir), ekrandaki burada kayıtlı.
	EnvKept     string `json:"envKept,omitempty"`
	ServiceKept string `json:"serviceKept,omitempty"`
	ScreenEnv   string `json:"screenEnv,omitempty"`
	// Aracın env argümanı yok: ekran env'i UYGULANMADI, sebebiyle.
	EnvNotApplied string `json:"envNotApplied,omitempty"`
}

// Empty — yazılacak bir şey yok mu.
func (s ScopeApplied) Empty() bool { return s == ScopeApplied{} }

// envBlindReasons — env argümanı BİLİNÇLİ olarak olmayan araçlar ve sebebi
// (Türkçe, sonuca "env uygulanmadı: <sebep>" olarak girer). Karar tablosu
// v0.10.1274 raporunda; her satır okumanın KENDİSİNE bakılarak yazıldı.
var envBlindReasons = map[string]string{
	// Kimlik-çapalı nokta aramaları: trace/span kimliği zaten kesin.
	"get_trace":                "trace kimliği zaten kesin; trace tek ortamdadır",
	"get_logs_for_trace":       "trace kimliğiyle kesin eşleşme; ortam süzgeci gerekmez",
	"get_exemplar_traces":      "örnek trace'ler metrik serisinin kendi etiketlerinden gelir",
	"get_linked_traces":        "trace kimliği zaten kesin; bağlı trace'ler kimlikle bulunur",
	"get_metrics_for_span":     "span kimliğiyle kesin eşleşme",
	"find_trace_by_span":       "span kimliğiyle kesin eşleşme",
	"find_trace_by_request_id": "istek kimliğiyle kesin eşleşme",
	// Okumanın env boyutu yok / uygulanamıyor.
	"render_chart":         "grafik okuması (spanMetricBatch) ortam kırılımı taşımıyor",
	"list_operations":      "operasyon kataloğu (operation_summary_5m) ortam boyutu taşımıyor",
	"get_topology":         "topology_edges_5m aynı servisi ortamlar arasında birleştiriyor; env yalnız etiket",
	"get_blast_radius":     "service_callers_5m'de env kolonu yok; çağıran sayıları tüm ortamların toplamı",
	"get_db_health":        "db_summary_5m ortam boyutu taşımıyor (env süzgeci ham span taramasına düşerdi)",
	"get_messaging_health": "messaging_summary_5m'de env kolonu yok",
	"get_pod_health":       "pod/runtime okumaları ortam boyutu taşımıyor (pod kimliğiyle gruplu)",
}

// EnvBlindReason — aracın env-kör olup olmadığı ve sebebi.
func EnvBlindReason(tool string) (string, bool) {
	r, ok := envBlindReasons[tool]
	return r, ok
}

// EnvBlindTools — kayıt defterindeki araç adları (ratchet testi için).
func EnvBlindTools() []string {
	out := make([]string, 0, len(envBlindReasons))
	for n := range envBlindReasons {
		out = append(out, n)
	}
	return out
}

// EnvScopeServiceMembership — servis-üyeliği env kapsamının sonuç etiketi.
const EnvScopeServiceMembership = "service_membership"

// envMembershipNote — servis-üyeliği kapsamının modele giden açıklaması.
const envMembershipNote = "env servis üyeliğiyle uygulandı: bu ortamda koşan servislerin satırları; satırların kendisi ortam boyutu taşımaz, sayılar ortam-kırılımlı DEĞİL"

// errEnvMembersUnavailable — env → servis haritası kurulamadı (Store yok).
var errEnvMembersUnavailable = errors.New("env → servis haritası yok")

// envScope — servis-üyeliği env kapsamının çözümü.
//
// Applied=false + Note: env istendi ama harita çözülemedi → SÜZGEÇSİZ devam
// (/problems'ın soft-fail duruşu: geçici bir CH tökezlemesi satır SAKLAMAZ),
// ve bu sonuçta açıkça yazılır.
type envScope struct {
	Env     string
	Members []string // Applied iken nil değil (boş = ortamda servis yok)
	Applied bool
	Note    string
}

// resolveEnvMembers — env → üye servisler (60 sn önbellekli harita).
// Deps.EnvMembers test dikişi; nil → Store.EnvMemberServices.
func resolveEnvMembers(ctx context.Context, d Deps, env string) envScope {
	env = strings.TrimSpace(env)
	if env == "" {
		return envScope{}
	}
	var (
		members []string
		err     error
	)
	switch {
	case d.EnvMembers != nil:
		members, err = d.EnvMembers(ctx, env)
	case d.Store != nil:
		members, err = d.Store.EnvMemberServices(ctx, env)
	default:
		err = errEnvMembersUnavailable
	}
	if err != nil {
		return envScope{Env: env, Note: "env uygulanamadı: ortam → servis haritası çözülemedi — sonuç ortama göre SÜZÜLMEDİ"}
	}
	if members == nil {
		members = []string{}
	}
	return envScope{Env: env, Members: members, Applied: true, Note: envMembershipNote}
}

// memberSet — üyelik araması için küme.
func (e envScope) memberSet() map[string]bool {
	m := make(map[string]bool, len(e.Members))
	for _, s := range e.Members {
		m[s] = true
	}
	return m
}

// stamp — sonuca env künyesini yazar (env, env_scope, env_note). Saf.
func (e envScope) stamp(body map[string]any) {
	if e.Env == "" {
		return
	}
	body["env"] = e.Env
	if e.Applied {
		body["env_scope"] = EnvScopeServiceMembership
	} else {
		body["env_applied"] = false
	}
	body["env_note"] = e.Note
}

// scopeServices — servis-üyeliği kapsamının SQL `Services` kümesi:
// istenen servis (varsa) ∩ üyeler. nil = kısıt yok; boş = hiçbir şey.
// Saf, tablo testli.
func scopeServices(service string, e envScope) []string {
	service = strings.TrimSpace(service)
	if !e.Applied {
		if service != "" {
			return []string{service}
		}
		return nil
	}
	if service == "" {
		return e.Members
	}
	for _, m := range e.Members {
		if m == service {
			return []string{service}
		}
	}
	return []string{}
}
