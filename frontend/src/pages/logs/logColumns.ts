// logColumns — v0.10.1502 (/logs Kibana-Discover dilim 4, Columns ▾).
//
// Sütun seçicisinin SAF dönüşümleri. Girdi sayfanın `logCols`u, çıktı
// changeCols'a giden yeni liste — tek yazım yolu (state + localStorage +
// sunucu tercihi + URL `cols=`) değişmez. Sıra normalizeLogColumns ile aynı
// anatomide: 'message' listede yoksa sona eklenir (eski kayıtlı tercihler).
import { normalizeLogColumns, isRemovableLogColumn } from '@/components/LogTable';

/** id'yi bir sola (-1) / sağa (+1) taşır; uçta ya da yoksa liste aynen. */
export function moveLogColumn(cols: string[], id: string, dir: -1 | 1): string[] {
  const list = normalizeLogColumns(cols);
  const i = list.indexOf(id);
  const j = i + dir;
  if (i < 0 || j < 0 || j >= list.length) return list;
  const next = [...list];
  [next[i], next[j]] = [next[j], next[i]];
  return next;
}

/** Kaldırılamayan (Message) dokunulmaz. */
export function removeLogColumn(cols: string[], id: string): string[] {
  if (!isRemovableLogColumn(id)) return normalizeLogColumns(cols);
  return normalizeLogColumns(cols).filter(c => c !== id);
}

/** Sona ekler; zaten varsa ya da boşsa liste aynen. */
export function addLogColumn(cols: string[], id: string): string[] {
  const list = normalizeLogColumns(cols);
  const k = id.trim();
  if (!k || list.includes(k) || k === 'time' || k === 'trace') return list;
  return [...list, k];
}

/** Eklenebilir adaylar: yerleşik + YÜKLENMİŞ alan listesi (yeni fetch yok), süzgeçle, ≤limit. */
export function addableLogColumns(cols: string[], builtin: readonly string[], fields: string[], needle: string, limit = 40): string[] {
  const have = new Set(normalizeLogColumns(cols));
  const n = needle.trim().toLowerCase();
  const out: string[] = [];
  for (const f of [...builtin, ...fields]) {
    if (have.has(f) || out.includes(f)) continue;
    if (n && !f.toLowerCase().includes(n)) continue;
    out.push(f);
    if (out.length >= limit) break;
  }
  return out;
}
