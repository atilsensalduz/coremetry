// traceSearchTerm.ts — v0.10.523: /traces'in DÖRT yüzeyinin (liste, hacim
// şeridi, sayım, RED paneli) sunucuya gönderdiği TEK arama terimi.
//
// Operatör (prod): şerit 2,4M span sayarken liste 8 trace buluyordu; şeridin
// isteğinde `search` yoktu. v0.10.343 "Trace ID kutusuna function_id yazıldı"
// düzeltmesi kimlik terimini (32-hex olmayan kimlik kutusu değeri) yalnız
// LİSTEYE eklemişti; şerit ve sayım hâlâ yalnız arama kutusunu okuyordu
// ([[feedback-fixes-have-second-halves]]). Kural: serbest metin kutusu
// doluysa o; değilse kimlik kutusundaki 32-hex olmayan değer; ikisi de
// boşsa undefined (parametre yazılmaz). 32-hex trace id ARAMA değil atlayış
// (apply() navigate eder), o yüzden terim olarak dönmez.
//
// v0.10.1423 — kimlik kutusu kalktı (operatör: "Search'ten zaten
// aranabiliyor"): kimlik değeri artık operasyon kutusunun kendisi, yani
// `search`. Kural tek satıra indi: kırpılmış arama terimi ya da undefined.
export function effectiveTraceSearch(f: { search?: string }): string | undefined {
  const s = (f.search ?? '').trim();
  return s || undefined;
}
