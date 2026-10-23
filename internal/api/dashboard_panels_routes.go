package api

// dashboard_panels_routes.go — v0.10.1443 (Grafana-Assistant programı madde 3,
// "sohbetten dashboard"; R1 düğmeler). Sohbet cevabındaki grafikleri tek tıkla
// panoya çeviren iki uç:
//
//	POST /api/dashboards/{id}/panels   — var olan panoya panel EKLE
//	POST /api/dashboards/from-chat     — panellerden YENİ pano kur
//
// Gövde: {panels: Panel[], dashboard?: {name}, origin?: {conversation, exchangeId}}
// Yanıt: {id, name, added, skipped: [{title, reason, detail?}], href}
//
// Kararlar (docs/DECISIONS.md v0.10.1443):
//   - Panolar PAYLAŞILAN (dashboards tablosunda sahip kolonu yok) — bu yüzden
//     audit satırı sohbet kimliğini taşır: "bu paneli kim, hangi konuşmadan
//     ekledi" sorusunun tek cevabı.
//   - Sohbet kökenli (gövdede origin var) PromQL paneli sunucuda YENİDEN
//     doğrulanır (validateChartPromQL — sohbetin izin listesi). Explore pini
//     (origin yok) eskisi gibi her PromQL'i yazabilir; yalnız tür/kaynak. MetricsQL'e özgü sorgu yalnız source:'vm' ile
//     kabul: CH yolunda VM'e özgü sözdizimi panelde kırmızı hata olurdu.
//   - Tekrar eden panel (aynı tür + normalize sorgu/süzgeç + agg + birim +
//     kaynak; FNV) yazılmaz, skipped[]'e "duplicate" ile girer.
//   - Tavanlar: pano başına ≤50 panel (dashboards/data demet tavanıyla aynı),
//     panel JSON'u ≤512 KiB, başlık ≤120 karakter; ad çakışması → "ad (2)".
//   - Yazım bütün satır (ReplacingMergeTree): mevcut ad/açıklama/değişken/
//     etiket mergeDashboardUpdate ile taşınır.
//
// Rotalar route_registry defterinden kaydolur — api.go büyümez.

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1443 — tavanlar.
const (
	dashPanelsMax        = 50        // pano başına panel (dashboards_data.go demet tavanı)
	dashPanelsJSONMax    = 512 << 10 // panels JSON'u
	dashPanelTitleMax    = 120       // panel başlığı (rune)
	dashNameMax          = 120       // pano adı (rune)
	dashPanelsBodyMax    = 1 << 20   // istek gövdesi
	dashOriginFieldMax   = 128       // origin.conversation / exchangeId
	dashChatTag          = "cosre"   // sohbetten doğan panonun etiketi
	dashChatDefaultName  = "CoSRE"   // ad verilmezse
	dashSkipDuplicate    = "duplicate"
	dashSkipInvalid      = "invalid"
	dashSkipCap          = "cap"
	dashSkipSize         = "size"
	dashNameCollisionMax = 100
)

// dashPanelTypes — kabul edilen panel türleri (lib/types.ts PanelType).
var dashPanelTypes = map[string]bool{
	"metric": true, "spanmetric": true, "stat": true, "gauge": true, "markdown": true,
	"row": true, "heatmap": true, "promql": true, "topn": true,
}

// dashboardStore — uçların okuduğu/yazdığı yüzey (*chstore.Store karşılar;
// testler sahte depo verir).
type dashboardStore interface {
	ListDashboards(ctx context.Context) ([]chstore.Dashboard, error)
	GetDashboard(ctx context.Context, id string) (*chstore.Dashboard, error)
	UpsertDashboard(ctx context.Context, d chstore.Dashboard) error
}

// dashPanelsReq — iki ucun ortak gövdesi.
type dashPanelsReq struct {
	Panels    []json.RawMessage `json:"panels"`
	Dashboard *struct {
		Name string `json:"name"`
	} `json:"dashboard,omitempty"`
	// Origin — sohbet kökeni; nil = sohbet dışı (Explore pini).
	Origin *dashPanelsOrigin `json:"origin,omitempty"`
}

type dashPanelsOrigin struct {
	Conversation string `json:"conversation"`
	ExchangeID   string `json:"exchangeId"`
}

// fromChat — istek sohbetten mi (origin gövdede var; yeni, henüz kalıcı
// olmayan konuşmada alanlar boş olabilir). Sohbet panelleri sohbet izin
// listesinden yeniden doğrulanır; Explore pini (origin yok) bugüne kadar
// GET+PUT ile her PromQL'i yazabiliyordu — /panels ucuna taşınınca sohbet
// tavanlarıyla (alt sorgu, >1h aralık…) geriye dönük kırılmasın.
func (r dashPanelsReq) fromChat() bool {
	return r.Origin != nil
}

// dashPanelSkip — yazılmayan panel ve gerekçesi.
type dashPanelSkip struct {
	Title  string `json:"title"`
	Reason string `json:"reason"` // duplicate | invalid | cap | size
	Detail string `json:"detail,omitempty"`
}

// dashPanelsResult — iki ucun yanıtı.
type dashPanelsResult struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Added   int             `json:"added"`
	Skipped []dashPanelSkip `json:"skipped"`
	Href    string          `json:"href"`
}

// dashPanelErr — istemci hatası (durum kodu + mesaj).
type dashPanelErr struct {
	code int
	msg  string
}

func (e *dashPanelErr) Error() string { return e.msg }

func init() {
	registerRoutesExtra("dashboard_panels", func(s *Server, mux *http.ServeMux) { registerDashboardPanelRoutes(mux, s) })
}

func registerDashboardPanelRoutes(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("POST /api/dashboards/{id}/panels", auth.RequireAnyRole(editorRoles, s.appendDashboardPanels))
	mux.HandleFunc("POST /api/dashboards/from-chat", auth.RequireAnyRole(editorRoles, s.createDashboardFromChat))
}

// dashStore — nil *chstore.Store'u nil arayüze çevirir (yoksa st==nil
// denetimi kaçar, çağrı nil işaretçide patlar).
func (s *Server) dashStore() dashboardStore {
	if s.store == nil {
		return nil
	}
	return s.store
}

func (s *Server) appendDashboardPanels(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeDashPanelsReq(w, r)
	if !ok {
		return
	}
	res, err := dashAppendPanels(r.Context(), s.dashStore(), r.PathValue("id"), req, func() string { return newID(8) })
	if !s.writeDashPanelsErr(w, err) {
		return
	}
	if res.Added > 0 {
		s.audit(r, "dashboard.panels_append", "dashboard", res.ID, dashAuditDetails(res, req))
	}
	writeJSON(w, res)
}

func (s *Server) createDashboardFromChat(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeDashPanelsReq(w, r)
	if !ok {
		return
	}
	res, err := dashCreateFromPanels(r.Context(), s.dashStore(), req, func() string { return newID(8) })
	if !s.writeDashPanelsErr(w, err) {
		return
	}
	s.audit(r, "dashboard.create_from_chat", "dashboard", res.ID, dashAuditDetails(res, req))
	writeJSON(w, res)
}

func decodeDashPanelsReq(w http.ResponseWriter, r *http.Request) (dashPanelsReq, bool) {
	var req dashPanelsReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, dashPanelsBodyMax)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid body")
		return req, false
	}
	if len(req.Panels) == 0 {
		writeJSONError(w, http.StatusBadRequest, "panels required")
		return req, false
	}
	if len(req.Panels) > dashPanelsMax {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("too many panels (max %d)", dashPanelsMax))
		return req, false
	}
	return req, true
}

// writeDashPanelsErr — true: hata yok, devam.
func (s *Server) writeDashPanelsErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	if pe, ok := err.(*dashPanelErr); ok {
		writeJSONError(w, pe.code, pe.msg)
		return false
	}
	writeErr(w, err)
	return false
}

func dashAuditDetails(res dashPanelsResult, req dashPanelsReq) string {
	var o dashPanelsOrigin
	if req.Origin != nil {
		o = *req.Origin
	}
	b, _ := json.Marshal(map[string]any{
		"name": res.Name, "count": res.Added, "skipped": len(res.Skipped),
		"conversation": dashClip(o.Conversation, dashOriginFieldMax),
		"exchangeId":   dashClip(o.ExchangeID, dashOriginFieldMax),
	})
	return string(b)
}

// dashAppendPanels — var olan panoya ekler. SAF değil (depo), ama deposu
// arayüz: tablo testleri sahte depoyla koşar.
func dashAppendPanels(ctx context.Context, st dashboardStore, id string, req dashPanelsReq, mkID func() string) (dashPanelsResult, error) {
	if st == nil {
		return dashPanelsResult{}, &dashPanelErr{http.StatusServiceUnavailable, "dashboard store unavailable"}
	}
	existing, err := st.GetDashboard(ctx, id)
	if err != nil {
		return dashPanelsResult{}, err
	}
	if existing == nil {
		return dashPanelsResult{}, &dashPanelErr{http.StatusNotFound, "dashboard not found"}
	}
	var current []json.RawMessage
	if len(existing.Panels) > 0 {
		if err := json.Unmarshal(existing.Panels, &current); err != nil {
			return dashPanelsResult{}, fmt.Errorf("dashboard %s panels: %w", id, err)
		}
	}
	merged, added, skipped := dashMergePanels(current, req.Panels, req.fromChat(), mkID)
	res := dashPanelsResult{ID: existing.ID, Name: existing.Name, Added: added, Skipped: skipped, Href: dashHref(existing.ID)}
	if added == 0 {
		return res, nil
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return dashPanelsResult{}, err
	}
	// Bütün satır: ad/açıklama gövdede; değişken/etiket nil → mevcut taşınır.
	d := mergeDashboardUpdate(chstore.Dashboard{Name: existing.Name, Description: existing.Description, Panels: raw}, *existing)
	if err := st.UpsertDashboard(ctx, d); err != nil {
		return dashPanelsResult{}, err
	}
	return res, nil
}

// dashCreateFromPanels — panellerden yeni pano (etiket "cosre").
func dashCreateFromPanels(ctx context.Context, st dashboardStore, req dashPanelsReq, mkID func() string) (dashPanelsResult, error) {
	if st == nil {
		return dashPanelsResult{}, &dashPanelErr{http.StatusServiceUnavailable, "dashboard store unavailable"}
	}
	merged, added, skipped := dashMergePanels(nil, req.Panels, true, mkID)
	if added == 0 {
		msg := "no valid panels"
		if len(skipped) > 0 && skipped[0].Detail != "" {
			msg += ": " + skipped[0].Detail
		}
		return dashPanelsResult{}, &dashPanelErr{http.StatusBadRequest, msg}
	}
	want := ""
	if req.Dashboard != nil {
		want = req.Dashboard.Name
	}
	want = dashClip(strings.Join(strings.Fields(want), " "), dashNameMax)
	if want == "" {
		want = dashChatDefaultName
	}
	list, err := st.ListDashboards(ctx)
	if err != nil {
		return dashPanelsResult{}, err
	}
	names := make([]string, 0, len(list))
	for _, d := range list {
		names = append(names, d.Name)
	}
	name := dashUniqueName(want, names)
	raw, err := json.Marshal(merged)
	if err != nil {
		return dashPanelsResult{}, err
	}
	d := chstore.Dashboard{
		ID: mkID(), Name: name, Description: "CoSRE sohbetinden oluşturuldu",
		Panels: raw, Tags: json.RawMessage(`["` + dashChatTag + `"]`),
	}
	if err := st.UpsertDashboard(ctx, d); err != nil {
		return dashPanelsResult{}, err
	}
	return dashPanelsResult{ID: d.ID, Name: name, Added: added, Skipped: skipped, Href: dashHref(d.ID)}, nil
}

func dashHref(id string) string { return "/dashboard?id=" + url.QueryEscape(id) }

// dashUniqueName — SAF: ad çakışırsa "ad (2)", "ad (3)"… (büyük/küçük harf ve
// boşluk duyarsız karşılaştırma).
func dashUniqueName(want string, existing []string) string {
	taken := make(map[string]bool, len(existing))
	for _, n := range existing {
		taken[strings.ToLower(strings.TrimSpace(n))] = true
	}
	if !taken[strings.ToLower(want)] {
		return want
	}
	for i := 2; i <= dashNameCollisionMax; i++ {
		c := fmt.Sprintf("%s (%d)", want, i)
		if !taken[strings.ToLower(c)] {
			return c
		}
	}
	return fmt.Sprintf("%s (%s)", want, newID(3))
}

// dashMergePanels — SAF (mkID hariç): gelen panelleri doğrular, normalize
// eder, mevcutlarla ve kendi aralarında tekilleştirir, tavanları uygular.
// Dönen dizi = mevcut + eklenenler. strict: PromQL sohbet izin listesinden
// geçer (sohbet kökeni); mevcut paneller anahtar için hep gevşek okunur —
// izin listesi dışındaki eski panel de tekrar sayılabilsin.
func dashMergePanels(current, incoming []json.RawMessage, strict bool, mkID func() string) ([]json.RawMessage, int, []dashPanelSkip) {
	out := append([]json.RawMessage(nil), current...)
	seenKey := map[string]bool{}
	seenID := map[string]bool{}
	size := 2 // "[]"
	for _, p := range current {
		if k, _, _, err := dashPanelInspect(p, false); err == nil {
			seenKey[k] = true
		}
		if id := dashPanelID(p); id != "" {
			seenID[id] = true
		}
		size += len(p) + 1
	}
	skipped := []dashPanelSkip{}
	added := 0
	for _, p := range incoming {
		key, title, norm, err := dashPanelInspect(p, strict)
		if err != nil {
			skipped = append(skipped, dashPanelSkip{Title: title, Reason: dashSkipInvalid, Detail: err.Error()})
			continue
		}
		if seenKey[key] {
			skipped = append(skipped, dashPanelSkip{Title: title, Reason: dashSkipDuplicate})
			continue
		}
		if len(out) >= dashPanelsMax {
			skipped = append(skipped, dashPanelSkip{Title: title, Reason: dashSkipCap, Detail: fmt.Sprintf("pano en çok %d panel taşır", dashPanelsMax)})
			continue
		}
		id := dashString(norm["id"])
		if id == "" || seenID[id] {
			id = mkID()
		}
		norm["id"], _ = json.Marshal(id)
		raw, err := json.Marshal(norm)
		if err != nil {
			skipped = append(skipped, dashPanelSkip{Title: title, Reason: dashSkipInvalid, Detail: err.Error()})
			continue
		}
		if size+len(raw)+1 > dashPanelsJSONMax {
			skipped = append(skipped, dashPanelSkip{Title: title, Reason: dashSkipSize, Detail: "pano gövdesi 512 KiB tavanını aşardı"})
			continue
		}
		size += len(raw) + 1
		seenKey[key] = true
		seenID[id] = true
		out = append(out, raw)
		added++
	}
	return out, added, skipped
}

func dashPanelID(p json.RawMessage) string {
	var v struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(p, &v)
	return v.ID
}

func dashString(raw json.RawMessage) string {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// dashPanelCfg — tekilleştirme ve doğrulamanın okuduğu config alanları.
type dashPanelCfg struct {
	Query      string `json:"query"`
	Source     string `json:"source"`
	Agg        string `json:"agg"`
	Field      string `json:"field"`
	GroupBy    string `json:"groupBy"`
	DSL        string `json:"dsl"`
	Filters    string `json:"filters"`
	Unit       string `json:"unit"`
	MetricName string `json:"metricName"`
	Service    string `json:"service"`
}

var dashWS = regexp.MustCompile(`\s+`)

// dashPanelInspect — SAF: paneli doğrular + normalize eder (başlık tavanı,
// genişlik varsayılanı) ve tekilleştirme anahtarını döndürür. Hata = panel
// yazılmaz (skipped "invalid"); başlık hata durumunda da döner (rapor için).
func dashPanelInspect(p json.RawMessage, strict bool) (key, title string, norm map[string]json.RawMessage, err error) {
	if err := json.Unmarshal(p, &norm); err != nil || norm == nil {
		return "", "", nil, fmt.Errorf("panel JSON nesnesi değil")
	}
	typ := dashString(norm["type"])
	title = strings.TrimSpace(dashString(norm["title"]))
	if !dashPanelTypes[typ] {
		return "", title, nil, fmt.Errorf("bilinmeyen panel türü %q", typ)
	}
	if title == "" {
		title = "Panel"
	}
	title = dashClip(title, dashPanelTitleMax)
	norm["title"], _ = json.Marshal(title)
	if _, ok := norm["width"]; !ok {
		norm["width"] = json.RawMessage("2")
	}
	rawCfg := norm["config"]
	if len(rawCfg) == 0 || rawCfg[0] != '{' {
		return "", title, nil, fmt.Errorf("panel config nesnesi yok")
	}
	var cfg dashPanelCfg
	if typ == "promql" || typ == "spanmetric" {
		if err := json.Unmarshal(rawCfg, &cfg); err != nil {
			return "", title, nil, fmt.Errorf("panel config okunamadı: %v", err)
		}
	}
	switch typ {
	case "promql":
		if strict {
			if err := dashValidatePromQL(cfg.Query, cfg.Source); err != nil {
				return "", title, nil, err
			}
		} else if err := dashValidatePromQLLoose(cfg.Query, cfg.Source); err != nil {
			return "", title, nil, err
		}
	case "spanmetric":
		if strings.TrimSpace(cfg.Agg) == "" {
			return "", title, nil, fmt.Errorf("spanmetric paneli agg ister")
		}
	}
	return dashPanelDedupeKey(typ, cfg, rawCfg), title, norm, nil
}

// dashValidatePromQLLoose — SAF: sohbet dışı (Explore pini) — yalnız boş
// sorgu ve bilinmeyen kaynak reddedilir; sorgu dili panelin işi.
func dashValidatePromQLLoose(query, source string) error {
	switch source {
	case "", "vm", "ch":
	default:
		return fmt.Errorf("bilinmeyen kaynak %q (vm | ch)", source)
	}
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("sorgu boş")
	}
	return nil
}

// dashValidatePromQL — SAF: sohbet izin listesi; MetricsQL'e özgü sorgu yalnız
// source 'vm' ile (VM-only sözdizimi CH yolunda panel hatası olurdu).
func dashValidatePromQL(query, source string) error {
	switch source {
	case "", "vm", "ch":
	default:
		return fmt.Errorf("bilinmeyen kaynak %q (vm | ch)", source)
	}
	chk := validateChartPromQL(query, 0)
	if chk.OK() {
		return nil
	}
	if chk.MetricsQL && source == "vm" {
		return nil
	}
	if chk.MetricsQL {
		return fmt.Errorf("%s — MetricsQL sorgusu kaynak 'vm' ister", chk.Reason)
	}
	return fmt.Errorf("%s", chk.Reason)
}

// dashPanelDedupeKey — SAF: FNV(tür + normalize sorgu/süzgeç + agg + birim +
// kaynak …). Bilinen alanları taşımayan türlerde (stat/gauge/markdown/…) ham
// config da anahtara girer — iki farklı markdown paneli "aynı" sayılmasın.
func dashPanelDedupeKey(typ string, cfg dashPanelCfg, rawCfg json.RawMessage) string {
	h := fnv.New64a()
	parts := []string{typ, dashWS.ReplaceAllString(strings.TrimSpace(cfg.Query), " "), cfg.Filters, cfg.Agg,
		cfg.Unit, cfg.Source, cfg.Field, cfg.GroupBy, strings.TrimSpace(cfg.DSL), cfg.MetricName, cfg.Service}
	if typ != "promql" && typ != "spanmetric" {
		var m map[string]any
		if json.Unmarshal(rawCfg, &m) == nil {
			b, _ := json.Marshal(m) // map anahtarları sıralı → kanonik
			parts = append(parts, string(b))
		}
	}
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum64())
}

// dashClip — SAF: en çok n rune (copilot_offtopic.go clipRunes n+1 verir); kesilirse "…" ile biter.
func dashClip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
