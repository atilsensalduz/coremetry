package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
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
