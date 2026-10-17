package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/sre"
)

// chat_actions.go — v0.10.542 (CoSRE v2 Faz 3.4): `action` bloğu.
//
// Kabul 6: cevaptaki "Filtreyi uygula" düğmesi operatörün AÇIK OLDUĞU
// sayfanın filtresini yeni sekme açmadan günceller. Aksiyon YALNIZ tool
// sonucundan türeyen bir linkten üretilir (deep_link / arg-türevi köprü) —
// model metninden asla (audit §2.8: injection'ın yürütme kanalı kapalı).
// Güvenlik: kök-göreli href, aynı pathname; istemci URL'yi
// mergeOpenHref ile birleştirir (sayfa-sahipli param'lar yer değiştirir,
// yabancı param'lar korunur) ve replace:true ile gezinir — sayfa URL'yi
// kaynak-of-truth olarak yeniden okur.

// actionForLink — link hedefi operatörün açık sayfasıysa (pagePath) apply
// aksiyonu; değilse yok (link çipi zaten navigasyon). Saf; tablo-testli.
func actionForLink(l guidedAnswerLink, pagePath string) (map[string]any, bool) {
	if pagePath == "" || l.Href == "" || !strings.HasPrefix(l.Href, "/") || strings.HasPrefix(l.Href, "//") {
		return nil, false
	}
	u, err := url.Parse(l.Href)
	if err != nil || u.Path != pagePath || u.RawQuery == "" {
		return nil, false
	}
	return map[string]any{"kind": "apply", "href": l.Href, "label": applyLabelTR(pagePath)}, true
}

func applyLabelTR(pagePath string) string {
	switch pagePath {
	case "/traces", "/logs", "/explore", "/endpoints", "/inbox", "/problems":
		return "Filtreyi bu sayfada uygula"
	}
	return "Bu sayfada uygula"
}

// ── v0.10.1169 — "Bulguyu probleme not olarak kaydet" ───────────────────
//
// Aksiyon YALNIZ sunucu tarafında, başarılı bir TOOL SONUCUNDAN türer —
// model metninden asla: hedef problem sohbetin sayfa bağlamından
// (pageCtx.ProblemID, istemcinin açık sayfası; sanitize edilmiş), not metni
// tool'un döndürdüğü içerikten kırpılır. İstemci metni değiştiremez: jeton
// (auth.SignAction, notify "Sustur" bağlantısıyla aynı anahtar) problem +
// metin özetini + çağıranı imzalar; POST /api/problems/{id}/notes
// (problem_notes.go) imzayı, sahibi ve süreyi doğrular. Kaynak kodu okuyan
// araç (read_source_code) not üretmez: kod gövdesi probleme yazılmaz.

const (
	problemNoteTokenKind = "cosre-note" // notify ignore ucu bu türü REDDEDER (default dalı)
	problemNoteTTL       = 2 * time.Hour
	problemNoteMaxRunes  = 1500
)

// problemNoteText — SAF: tool sonucundan not metni (boşluk sıkıştırılmış, kırpılmış).
func problemNoteText(tool, content string) string {
	body := clampChatRunes(collapseChatWS(content), problemNoteMaxRunes)
	if body == "" {
		return ""
	}
	return "CoSRE bulgusu (" + tool + "): " + body
}

// problemNoteDigest — SAF: jetonun imzaladığı kimlik (problem + metin özeti).
func problemNoteDigest(problemID, text string) string {
	sum := sha256.Sum256([]byte(text))
	return problemID + "|" + hex.EncodeToString(sum[:])
}

// problemNoteAction — aksiyon bloğu; problem bağlamı / kimlik / metin yoksa yok.
func (s *Server) problemNoteAction(ctx context.Context, problemID, tool, content string) (map[string]any, bool) {
	problemID = strings.TrimSpace(problemID)
	c := auth.FromContext(ctx)
	if problemID == "" || tool == "read_source_code" || c == nil || c.UserID == "" || s.auth == nil {
		return nil, false
	}
	text := problemNoteText(tool, content)
	if text == "" {
		return nil, false
	}
	tok, err := s.auth.SignAction(problemNoteTokenKind, problemNoteDigest(problemID, text), c.UserID, problemNoteTTL)
	if err != nil {
		return nil, false
	}
	return map[string]any{
		"kind": "problem_note", "problemId": problemID, "tool": tool, "text": text,
		"token": tok, "label": "Bulguyu probleme not olarak kaydet",
	}, true
}

// ── v0.10.1288 (AI SRE Faz 2) — "<hipotez> hipotezini doğrula" ──────────
//
// problemNoteAction deseni: aksiyon YALNIZ sunucu tarafında, başarılı bir
// get_problem TOOL SONUCUNDAN türer — model metninden asla. Hedef problem
// sohbetin sayfa bağlamıyla (pageCtx.ProblemID) EŞLEŞMELİ; hipotez türü
// kodun kapalı kümesinden (sre.IsHypothesisKind). Tık-only: istemci
// POST /api/problems/{id}/investigation {kind:"verify", hypothesis} çağırır
// (editor+, audit; uç türü yeniden doğrular). İmzalı jeton GEREKMEZ: yükte
// serbest metin yok (problem_note'un jetonu metin bütünlüğü içindi), uç
// idempotent kuyruğa alma ve rol kapılı. Viewer'a aksiyon önerilmez.

// sreHypothesisPick — SAF: get_problem çıktısından doğrulanacak hipotez.
// Kural: problem sayfanın problemi; kartın ilk hipotezi düşük bantta değil ve
// operatör onu elememiş/onaylamamış (işaretli kart zaten karara bağlanmış).
func sreHypothesisPick(problemID, tool, content string) (kind, label string, ok bool) {
	if tool != "get_problem" || strings.TrimSpace(problemID) == "" {
		return "", "", false
	}
	var out struct {
		ID            string `json:"id"`
		DisplayID     string `json:"displayId"`
		Investigation *struct {
			Hypotheses []struct {
				Kind     string `json:"kind"`
				Label    string `json:"label"`
				Band     string `json:"band"`
				Operator string `json:"operator"`
			} `json:"hypotheses"`
		} `json:"investigation"`
	}
	if json.Unmarshal([]byte(content), &out) != nil || out.Investigation == nil || len(out.Investigation.Hypotheses) == 0 {
		return "", "", false
	}
	if out.ID != problemID && out.DisplayID != problemID {
		return "", "", false
	}
	h := out.Investigation.Hypotheses[0]
	if !sre.IsHypothesisKind(h.Kind) || h.Band == sre.BandLow || h.Operator != "" {
		return "", "", false
	}
	return h.Kind, sre.HypothesisLabel(h.Kind), true
}

// sreHypothesisAction — aksiyon bloğu; editor+ değilse ya da seçim yoksa yok.
func (s *Server) sreHypothesisAction(ctx context.Context, problemID, tool, content string) (map[string]any, bool) {
	c := auth.FromContext(ctx)
	if c == nil || (c.Role != auth.RoleAdmin && c.Role != auth.RoleEditor) {
		return nil, false
	}
	kind, label, ok := sreHypothesisPick(strings.TrimSpace(problemID), tool, content)
	if !ok {
		return nil, false
	}
	return map[string]any{
		"kind": "sre_hypothesis", "problemId": strings.TrimSpace(problemID), "hypothesis": kind,
		"label": label + " hipotezini doğrula",
	}, true
}
