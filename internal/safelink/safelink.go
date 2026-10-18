// Package safelink — v0.10.1315 (olay linki XSS): API/editör kaynaklı bir
// URL'nin bir href'e / window.open'a ulaşıp ulaşamayacağına karar veren TEK
// sunucu tarafı kapı (SAF, yalnız stdlib).
//
// Neden: /api/operator-events `link` alanı serbest saklanıyor ve
// `<a href={ev.link}>` / `window.open(e.link)` ile çiziliyordu; React 18
// `javascript:` href'lerini engellemez → saklanan XSS. Kapı iki uçta:
// yazarken ret (internal/api/events.go validateEventLink → 400), okurken
// süzme (internal/chstore/event.go ListEvents → Sanitize; eski satırlar).
//
// AYNA: Internal kuralı frontend/src/lib/safeAppPath.ts isSafeAppPath'in
// birebir aynası, External kuralı appLinkKind'in 'external' dalı (+ Go'da
// ek: Host zorunlu, userinfo yasak). İki tablo testi kilitli adımda:
// safelink_test.go ↔ frontend/src/lib/safeAppPath.ratchet.test.ts — biri
// değişirse öteki de değişir (keep in lockstep).
//
// sanitizeOIDCNext (internal/api/oidc_next.go, daha SIKI) ve
// buildLinkResultLink (internal/api/chat_tool_links.go) bilinçli olarak ayrı.
package safelink

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind — bir URL nasıl çizilir.
type Kind int

const (
	// None — çizilmez (javascript:, data:, "//host", "/\host", ftp:, …).
	None Kind = iota
	// Internal — kök-göreli güvenli uygulama yolu (react-router Link/navigate).
	Internal
	// External — mutlak http(s) URL (yeni sekme, noopener).
	External
)

// MaxLen — saklanan/çizilen link üst sınırı (bayt).
const MaxLen = 4096

// badRune — JS `/[\s\u0000-\u001f\u007f-\u009f]/` aynası: C0/DEL/C1 kontrol
// karakterleri ve Unicode boşlukları (JS \s = unicode.IsSpace + U+FEFF).
func badRune(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f) || unicode.IsSpace(r) || r == 0xFEFF
}

func hasBad(s string) bool {
	return strings.IndexFunc(s, badRune) >= 0
}

// pathChecks — isSafeAppPath'in ham ve kod çözülmüş yola uyguladığı ortak
// kontroller: "/" ile başlar, "//" ve "/\" ile başlamaz, ters bölü ve
// kontrol/boşluk yok.
func pathChecks(s string) bool {
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, `/\`) {
		return false
	}
	return !strings.Contains(s, `\`) && !hasBad(s)
}

// isInternal — frontend isSafeAppPath aynası. Yol kısmı (? / # öncesi)
// url.PathUnescape ile çözülür (decodeURIComponent: bozuk kodlama ya da
// geçersiz UTF-8 ⇒ ret) ve aynı kontrolleri yeniden geçer — %2F%2F, %5C ret.
func isInternal(u string) bool {
	if !pathChecks(u) {
		return false
	}
	p := u
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	dec, err := url.PathUnescape(p)
	if err != nil || !utf8.ValidString(dec) {
		return false
	}
	// JS'te çözülmüş yol her zaman "/" ile başlar (ham yol öyle başladı);
	// startsWith('//') / '/\' / ters bölü / kontrol aynı pathChecks.
	return pathChecks(dec)
}

// isExternal — ham önek tam olarak http:// ya da https:// (büyük/küçük harf
// duyarsız; "http:evil" değil), url.Parse başarılı, Host dolu, userinfo yok.
func isExternal(u string) bool {
	l := strings.ToLower(u)
	if !strings.HasPrefix(l, "http://") && !strings.HasPrefix(l, "https://") {
		return false
	}
	pu, err := url.Parse(u)
	if err != nil || pu.Host == "" || pu.User != nil {
		return false
	}
	return true
}

// Classify — u'nun (TrimSpace sonrası) türü. Boş, MaxLen'den uzun, geçersiz
// UTF-8 ya da kontrol karakteri/boşluk içeren her şey None. Internal önce.
func Classify(u string) Kind {
	u = strings.TrimSpace(u)
	if u == "" || len(u) > MaxLen || !utf8.ValidString(u) || hasBad(u) {
		return None
	}
	if isInternal(u) {
		return Internal
	}
	if isExternal(u) {
		return External
	}
	return None
}

// SafeLink — güvenliyse kırpılmış URL ve true; boş/None ise "" ve false.
func SafeLink(u string) (string, bool) {
	if Classify(u) == None {
		return "", false
	}
	return strings.TrimSpace(u), true
}

// Sanitize — güvenliyse kırpılmış URL, değilse "".
func Sanitize(u string) string {
	s, _ := SafeLink(u)
	return s
}
