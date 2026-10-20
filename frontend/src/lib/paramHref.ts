// paramHref — v0.10.1414 (tablo satırları gerçek link). URL-parametreli
// çekmece açan satırın (`?host=` …) adresi: bugünkü `setSearchParams(prev =>
// next.set(key, value), { replace: true })` yazımının VARACAĞI adresin aynısı —
// yabancı parametreler (range, env, süzgeçler) korunur. Satırın birincil hücresi
// bu adrese `<Link replace>` olur: Ctrl/⌘/orta tık çekmece açık yeni sekme,
// "bağlantıyı kopyala" aynı görünümü taşır.
export function paramHref(pathname: string, prev: URLSearchParams, key: string, value: string): string {
  const next = new URLSearchParams(prev);
  next.set(key, value);
  return `${pathname}?${next.toString()}`;
}
