// traceColumns — the /traces list column ORDER and the default
// attribute set (v0.9.841, operator request 2026-08-09).
//
// Extracted from Traces.tsx because both halves are decisions, not
// mechanics, and one of them had already drifted once: the order lived
// in a one-line useMemo whose shape ("time, then the attribute columns,
// then everything else") was never stated anywhere, so nothing could
// notice when it stopped matching what an operator wanted to read.

/**
 * The six built-in columns, in their canonical order.
 *
 * v0.10.217 (operatör, Dynatrace "Distributed traces" düzeni — mockup
 * onayı 2026-09-01): Name (= operation) baskın kolon, Start time (= time)
 * en sonda. Kolon id'leri DEĞİŞMEDİ (`operation`/`time`) — kalıcı
 * genişlikler, `?cols=` ve sunucu sort anahtarları id'ye bağlı; yalnız
 * etiket ve sıra değişti (Traces.tsx COL_LABEL: Name / Start time).
 *
 * v0.10.220 (operatör, aynı gün): "Start time en sağda ama en solda
 * olmasını tercih ederim" → zaman damgası yine EN BAŞTA, Name onun
 * hemen ardından. Dynatrace'ten alınan geri kalan her şey (Name baskın,
 * Duration → Status → Spans kuyruğu) aynen.
 */
export const FIXED_COLS = ['time', 'operation', 'service', 'duration', 'status', 'spans'] as const;

/**
 * DEFAULT_TRACE_COLUMNS — the attribute columns a fresh session gets.
 *
 * History, because this value has now been decided twice:
 *   • FAZ 2B (2026-07-23) set it EMPTY — attribute columns opt-in, on
 *     the reasoning that a fresh session should get the narrowest,
 *     fastest list.
 *   • 2026-08-09, operator request: the four attributes below ARE the
 *     working set on this deployment, and starting from a list that
 *     omits them meant re-adding the same four columns in every fresh
 *     browser. Coremetry is single-tenant, so "the operator's
 *     attributes" and "the product's default" are the same thing here.
 *
 * These are environment attribute KEYS, not identities — nothing about
 * them names a customer. An attribute a given service never emits is
 * already handled honestly by missingExtraKeys (traceExtrasMerge), so a
 * default that does not apply everywhere degrades into a labelled
 * empty column, not a silent one.
 *
 * Precedence on load is UNCHANGED: URL ?cols= (shareable source of
 * truth) → localStorage (per-browser continuity) → this default. The
 * 8-column ceiling and ColumnManager behaviour are untouched.
 *
 *   • 2026-08-24, operatör isteği (iki adımda): `function_id` EKLENDİ,
 *     `http.status_code` ÇIKARILDI. Küme yine dört ve artık operatörün
 *     canlı oturumunda fiilen kullandığının aynısı — v0.9.1360'ta "da"
 *     denildiği için status_code korunmuştu, operatör aynı gün açıkça
 *     çıkarılmasını istedi.
 *
 *     Gerekçe kayda değer: bu deployment'ta satırı TANIMLAYAN alanlar
 *     cluster/channel/function üçlüsü; `http.status_code` ise satırın
 *     SONUCU ve o bilgi zaten Status kolonunda var (OK/ERROR rozeti).
 *     Yani ikinci kez, daha dar bir biçimde gösteriliyordu.
 *
 *     ⚠ Genişlik bütçesi: beş kolon için Traces.tsx COL_W yeniden
 *     dağıtılmıştı (v0.9.1360); dörde inince o kısıntı GERİ ALINMADI —
 *     kısıntılar içeriğe bakılarak yapılmıştı (duration 104 "44.36ms"e,
 *     spans 58 tek haneye yeter) ve dört kolonla artık rahat pay var.
 */
export const DEFAULT_TRACE_COLUMNS: string[] = [
  'http.method',
  'http.route',
  'deployment.environment',
];

/**
 * traceColumnOrder — the rendered column ids, in order.
 *
 * Start time · Name · Service · <attribute columns> · Duration · Status ·
 * Spans (v0.10.220 — operatör: zaman damgası en solda; v0.10.217 onu
 * Dynatrace gibi en sağa koymuştu). Tarihçe: v0.9.841'de
 * Time · Service · Operation · attrs · Duration · Spans · Status idi
 * (operatör isteği 2026-08-09 — attr kolonları Time'ın hemen arkasından
 * alınıp satırı KİMLİKLEYEN iki alanın, Service ve Operation'ın, sağına
 * konmuştu). O karar korunuyor: attr kolonları yine kimlik alanlarından
 * hemen sonra ("Kalsın böyle", 2026-09-01); değişen, kimliğin başa
 * (Name) ve zaman damgasının sona gitmesi.
 *
 * Header and body both derive from this one array (DataTableHead over
 * `columns`, cells over the same ids), so the two can never disagree
 * about which column is which — the failure mode that makes a table
 * quietly print the wrong value under the right heading.
 *
 * Unknown / duplicate extras are the caller's business (ColumnManager
 * dedupes on add and caps at 8); this function only orders.
 */
export function traceColumnOrder(extraCols: string[]): string[] {
  // v0.10.340 — operatör: "start time'dan sonra service sonra name gelsin".
  return [
    'time', 'service', 'operation',
    ...extraCols,
    ...FIXED_COLS.filter(c => c !== 'time' && c !== 'service' && c !== 'operation'),
  ];
}

// ── v0.10.1516 — kullanıcı sütun sırası (operatör isteği 2026-10-24:
// "kolon sıralamasını da değiştirebilsin isteyen kullanıcılar") ──────────
//
// traceColumnOrder VARSAYILAN sıradır ve öyle kalır; kullanıcı sırası onun
// ÜSTÜNE bir katman. Durum iki parça: `extras` (hangi attribute kolonları —
// fetch'in extraAttrs projeksiyonu, ColumnManager tavanı) ve `order`
// (tam sıra ya da null = varsayılan). Kararlar:
//   • Start time EN SOLDA sabit (v0.10.220 operatör kararı + v0.10.723
//     stickyLeft: yatay kaydırmada zaman bağlamı kalsın). stickyLeftOffsets
//     baştaki kesintisiz sabit diziyi zincirler; ortadaki sabit kolon
//     zincirden düşer. Bu yüzden `time` taşınmaz, önüne hiçbir şey girmez.
//   • Paylaşım linki: YENİ URL parametresi yok — sıra mevcut `cols=`
//     parametresine biner. Varsayılan sırada `cols=` bugünkü gibi yalnız
//     attribute listesi (eski linkler bayt bayt aynı); özel sırada TAM sıra
//     (yerleşik id'ler dahil). Okurken listede yerleşik bir id varsa tam
//     sıradır. Yerleşik id'ler (time/service/…) zaten attribute adı olarak
//     kullanılamıyordu: colIds'te çift anahtar olurdu.
//   • Kalıcılık: localStorage `traces-col-order` (attribute listesi eski
//     `traces-extra-cols` anahtarında AYNEN — geri dönüşte eski kod yerleşik
//     id'yi attribute sanmasın) + sunucu tercihi (`traces-list` modeli zaten
//     tam `order` taşıyordu; artık gerçek sırayı).

export const PINNED_TRACE_COL = 'time';
const BUILTIN = new Set<string>(FIXED_COLS);

/** Yerleşik (attribute olmayan) kolon mu. */
export function isBuiltinTraceCol(id: string): boolean {
  return BUILTIN.has(id);
}

/** Sıradaki attribute kolonları, sırayla. */
export function extrasOfOrder(order: readonly string[]): string[] {
  return order.filter(id => !BUILTIN.has(id));
}

/**
 * resolveTraceOrder — çizilen kolon id'leri. `order` null → varsayılan.
 * Bilinmeyen (kaldırılmış attribute) ve çift id düşer; sırada olmayan
 * (yeni eklenen attribute, eski kayıtta eksik yerleşik) varsayılan
 * dizideki en yakın ÖNCEKİ komşusunun arkasına girer — yeni attribute
 * böylece son attribute'un (yoksa Name'in) yanına düşer. `time` her
 * durumda ilk.
 */
export function resolveTraceOrder(extras: readonly string[], order: readonly string[] | null | undefined): string[] {
  const def = traceColumnOrder([...extras]);
  if (!order || order.length === 0) return def;
  const known = new Set(def);
  const out: string[] = [];
  for (const id of order) {
    if (known.has(id) && !out.includes(id) && id !== PINNED_TRACE_COL) out.push(id);
  }
  def.forEach((id, i) => {
    if (id === PINNED_TRACE_COL || out.includes(id)) return;
    let at = 0;
    for (let j = i - 1; j >= 0; j--) {
      const p = out.indexOf(def[j]);
      if (p >= 0) { at = p + 1; break; }
    }
    out.splice(at, 0, id);
  });
  return [PINNED_TRACE_COL, ...out];
}

/** Çizilen sıra varsayılan mı (attribute'ların kendi sırasıyla). */
export function isDefaultTraceOrder(rendered: readonly string[]): boolean {
  return rendered.join(',') === traceColumnOrder(extrasOfOrder(rendered)).join(',');
}

/** Çizilen sıradan durum: attribute listesi + (varsayılan değilse) tam sıra. */
export function splitTraceOrder(rendered: readonly string[]): { extras: string[]; order: string[] | null } {
  const extras = extrasOfOrder(rendered);
  return { extras, order: isDefaultTraceOrder(rendered) ? null : [...rendered] };
}

/** `cols=` / tercih yazımı: varsayılan sırada yalnız attribute'lar, değilse tam sıra. */
export function encodeTraceCols(rendered: readonly string[]): string[] {
  return isDefaultTraceOrder(rendered) ? extrasOfOrder(rendered) : [...rendered];
}

/** `cols=` okuması: yerleşik id içeren liste tam sıradır; içermeyen eski biçim (yalnız attribute). */
export function decodeTraceCols(list: readonly string[]): { extras: string[]; order: string[] | null } {
  const ids = list.map(s => s.trim()).filter(Boolean).filter((s, i, a) => a.indexOf(s) === i);
  if (!ids.some(id => BUILTIN.has(id))) return { extras: ids, order: null };
  return splitTraceOrder(resolveTraceOrder(extrasOfOrder(ids), ids));
}

/**
 * moveTraceColumn — klavye yolu (Sütunlar ▾ ↑/↓): bir sola (-1) / sağa (+1).
 * `time` taşınmaz ve hiçbir kolon onun önüne geçemez; uçta / bilinmeyen
 * id → sıra aynen.
 */
export function moveTraceColumn(rendered: readonly string[], id: string, dir: -1 | 1): string[] {
  const i = rendered.indexOf(id);
  const j = i + dir;
  if (i < 0 || id === PINNED_TRACE_COL || j < 0 || j >= rendered.length || rendered[j] === PINNED_TRACE_COL) return [...rendered];
  const next = [...rendered];
  [next[i], next[j]] = [next[j], next[i]];
  return next;
}
