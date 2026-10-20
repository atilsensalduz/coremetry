// paletteIdentity — v0.10.674 (operatör, prod: "aynı function_id global
// search'ten bulunmuyor ama Traces sayfasında girince buluyor").
//
// Komut paleti eşleştirme için sorguyu küçük harfe çevirir (servis/sayfa
// puanlaması harf-duyarsız olsun diye) ve v0.10.350'nin "Kimlikle trace
// ara" önerisi de o küçük harfli değeri /traces?traceId= ile gönderiyordu.
// Sunucunun kimlik-önce yolu (v0.10.342-344) attr_function_id = ? ile
// harf-DUYARLI eşitlik yapar: "…vzXA…" → "…vzxa…" sıfır satır.
//
// Sözleşme: kimlik değeri HAM sorgudan türer — kırpılır, harfi korunur.
// Trace id dalı (hex) ayrı; orada küçük harf doğru.

// IDENTITY_RE — tek parça, ≥8, [A-Za-z0-9._:-] (sunucu identityToken ile
// aynı sınıf). v0.10.350'de CommandPalette'te yaşıyordu; tek yazım burada.
export const IDENTITY_RE = /^[A-Za-z0-9._:-]{8,}$/;

export function paletteIdentityQuery(rawQuery: string): string | null {
  const v = rawQuery.trim();
  if (!IDENTITY_RE.test(v) || !/\d/.test(v)) return null;
  return v;
}

// v0.10.1423 — /traces'in Trace ID kutusu kalktı; kimlik operasyon kutusunun
// terimi (`?search=`). Eski `?traceId=` linkleri sayfada bir kez yönlenir
// (pages/traces/traceIdJump.ts), ama palet onları artık ÜRETMEZ.
export function identityTracesHref(v: string): string {
  return `/traces?search=${encodeURIComponent(v)}`;
}

// v0.10.706 — problem görüntü kimliği: "P-3f9a2" (harf duyarsız; kanonik
// küçük harf). /inbox?problem=P-… → sunucu GetProblemByDisplayID çözer.
export const PROBLEM_DISPLAY_ID_RE = /^[Pp]-[0-9A-Za-z]{1,7}$/;

export function paletteProblemId(rawQuery: string): string | null {
  const v = rawQuery.trim();
  if (!PROBLEM_DISPLAY_ID_RE.test(v)) return null;
  return 'P-' + v.slice(2).toLowerCase();
}

export function problemDisplayHref(displayId: string): string {
  return `/inbox?problem=${encodeURIComponent(displayId)}`;
}
