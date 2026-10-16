// urlRemountKey.ts (v0.10.1218) — exploreRouteKey (v0.9.805) deseninin
// genel hali, SAF. URL'i mount başına bir kez okuyan sayfalar (Traces)
// sayfa açıkken gelen DIŞ URL değişimini (saved view, derin link, geri/ileri)
// görmüyor, state→URL efekti de eski state'i geri yazıyordu (tek-yön sınıfı).
// Çözüm: sayfanın SAHİP OLDUĞU paramların imzası React key'ine katılır;
// sayfa kendi yazımını navigate'ten önce bildirir (onSelfWrite) — yankı
// remount etmez, dış değişim eder. Bildirim tek değerli (bkz. exploreRouteKey).

// ownedParamSig — yalnız `owned` anahtarlarının boş olmayan değerleri,
// sıralı + FNV-1a. Başka yazıcıların paramları (range, env, s_*) dışarıda:
// onlar canlı okunuyor ve onlara dokunmak remount etmemeli.
export function ownedParamSig(search: string, owned: readonly string[]): string {
  const sp = new URLSearchParams(search);
  const parts: string[] = [];
  for (const k of owned) {
    for (const v of sp.getAll(k)) if (v !== '') parts.push(`${k}=${v}`);
  }
  parts.sort();
  const canon = parts.join('&');
  let h = 2166136261;
  for (let i = 0; i < canon.length; i++) {
    h ^= canon.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return `${canon.length.toString(36)}.${(h >>> 0).toString(36)}`;
}

export interface UrlKeyState {
  key: string;
  sig: string;
  n: number;
}

// nextUrlRemountKey — prev null → ilk mount; aynı imza → aynı; kendi
// yankımız → imzayı benimse, key sabit; aksi → yeni key (remount).
export function nextUrlRemountKey(prev: UrlKeyState | null, sig: string, lastSelfWriteSig: string | null): UrlKeyState {
  if (!prev) return { key: 'k0', sig, n: 0 };
  if (prev.sig === sig) return prev;
  if (sig === lastSelfWriteSig) return { ...prev, sig };
  const n = prev.n + 1;
  return { key: `k${n}`, sig, n };
}
