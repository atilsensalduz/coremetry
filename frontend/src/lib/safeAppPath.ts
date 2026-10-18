// safeAppPath — v0.10.1314: uygulama-içi gezinmenin TEK kapısı (SAF).
//
// Neden: Dependabot uyarısı — react-router < 7.18'de ters bölüyle açık
// yönlendirme. v6'nın ABSOLUTE_URL_REGEX'i ve removeDoubleSlashes'ı yalnız
// "//" görür; `<Link to="/\evil.com">` / `navigate('/\\evil.com')` ikisini de
// geçer. Tarayıcı "/\" ile başlayan yolu protokol-göreli ("//evil.com") sayar,
// history.pushState kökenler-arası diye fırlatır ve router window.location
// .assign'a düşer → kullanıcı başka siteye gider. 6.x yaması yok; v7 de
// kurtarmaz (@grafana/ui iç içe react-router 6.30.6 taşıyor). Karar: v6'da
// kal, sabit-olmayan HER uygulama-içi yolu (sunucu/AI/editör kaynaklı href)
// Link/navigate'e vermeden önce bu kapıdan geçir. Ratchet:
// safeAppPath.ratchet.test.ts (yalnız "//"ya bakan yeni kapı kırmızı).
//
// Kural (chatLinks.ts isRelativePath'ten AYNEN taşındı): "/" ile başlar;
// "//" ve "/\" ile başlamaz; hiçbir yerde ters bölü yok; boşluk/kontrol
// karakteri yok; kod çözülmüş YOL kısmı (%2F%2F, %5C) aynı kontrolleri geçer;
// decodeURIComponent hatası ⇒ false. Sunucu tarafı aynası: Go
// buildLinkResultLink (internal/api/chat_tool_links.go) ve sanitizeOIDCNext
// (internal/api/oidc_next.go). postLoginRedirect.ts bundan SIKI (nokta
// segmenti, /login|/api öneki) ve Go'yla 1:1 — o ayrı kalır.
//
// v0.10.1315 (olay linki XSS) — Go aynası: internal/safelink (Classify /
// SafeLink / Sanitize). Internal = isSafeAppPath, External = appLinkKind'in
// http(s) dalı (Go ek olarak Host zorunlu, userinfo yasak). Tablolar kilitli
// adımda: safeAppPath.ratchet.test.ts ↔ internal/safelink/safelink_test.go.
// API/editör kaynaklı URL'yi ÇİZEN tek kapı components/ui/SafeHref.tsx;
// programatik açış openSafeLink (aşağıda).

// Kontrol karakteri ya da boşluk (yol içinde asla).
// eslint-disable-next-line no-control-regex -- yol güvenliği: C0/C1 kontrol karakterlerini reddetmek kuralın amacı
const CTRL_OR_SPACE = /[\s\u0000-\u001f\u007f-\u009f]/;

/**
 * Kök-göreli GÜVENLİ uygulama yolu ("/x"). Reddedilenler: "//host" ve "/\host"
 * (tarayıcı protokol-göreli dış adres sayar), yolda ters bölü, boşluk/kontrol
 * karakteri, ve kod çözülünce "//" ya da "/\" ile başlayan ya da ters bölü
 * içeren yol (%2F%2F, %5C — sunucu/yönlendirici çözünce dışarı açılabilir).
 */
export function isSafeAppPath(u: string): boolean {
  if (!u.startsWith('/') || u.startsWith('//') || u.startsWith('/\\')) return false;
  if (u.includes('\\') || CTRL_OR_SPACE.test(u)) return false;
  const path = u.split(/[?#]/)[0];
  let dec = path;
  try { dec = decodeURIComponent(path); } catch { return false; }
  if (dec.startsWith('//') || dec.startsWith('/\\') || dec.includes('\\') || CTRL_OR_SPACE.test(dec)) return false;
  return true;
}

/** Güvenli uygulama yolu ise kendisi, değilse (ya da boş/yoksa) null. */
export function safeAppPath(u: string | null | undefined): string | null {
  return typeof u === 'string' && isSafeAppPath(u) ? u : null;
}

/**
 * Sunucu/editör kaynaklı bir href nasıl çizilir: iç = isSafeAppPath (react-router
 * `<Link>`/navigate), dış = yalnız http(s) (`<a target=_blank>`), ikisi de
 * değilse null (bağlantı çizilmez — javascript:/ftp:/"/\host" asla).
 */
export function appLinkKind(href: string): 'internal' | 'external' | null {
  if (isSafeAppPath(href)) return 'internal';
  if (/^https?:\/\//i.test(href)) return 'external';
  return null;
}

/**
 * v0.10.1315 — API/editör kaynaklı bir linki programatik açar (olay işareti,
 * tık işleyicileri). Dış (http(s)) → yeni sekme, noopener+noreferrer; iç
 * (isSafeAppPath) → SPA navigate; ikisi de değilse hiçbir şey yapmaz ve false.
 */
export function openSafeLink(href: string | null | undefined, navigate: (to: string) => void): boolean {
  if (typeof href !== 'string') return false;
  const kind = appLinkKind(href);
  if (kind === 'external') {
    window.open(href, '_blank', 'noopener,noreferrer');
    return true;
  }
  if (kind === 'internal') {
    navigate(href);
    return true;
  }
  return false;
}
