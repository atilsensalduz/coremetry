import type { ChatQueryEvidence, ChatEvidenceChartPayload } from '@/lib/types';
import type { CorePanelMultiItem } from '@/components/chart/corePanelEntry';
import type { EvidenceBlockView } from '@/lib/chatBlocks';
import type { Tone } from '@/components/ui/Badge';
import { fmtClock, fmtDateTime, fmtDurShort } from '@/lib/utils';
import { fmtMs } from './toolSteps';

// evidenceChip — v0.10.1272 (CoSRE adım 3): kanıt çipinin SAF yarısı.
// Çip metni: `[Kaynak] · pencere · env/cluster · N satır · 123 ms`; tam sorgu
// ipucunda. Kaynak ayrı döner (rozet olarak çizilir), kalan parçalar " · " ile.

/** Pencere etiketi: "1h 14:00–15:00"; 24 saati aşan pencere "7d 10.10 14:00–17.10 14:00". */
export function evidenceWindowLabel(w: ChatQueryEvidence['window']): string {
  if (!w || !(w.toMs > w.fromMs) || w.fromMs <= 0) return '';
  const dur = fmtDurShort((w.toMs - w.fromMs) / 1000);
  const hm = (ms: number) => fmtClock(ms).slice(0, 5);
  if (w.toMs - w.fromMs <= 24 * 3600_000) return `${dur} ${hm(w.fromMs)}–${hm(w.toMs)}`;
  const dm = (ms: number) => fmtDateTime(ms).slice(0, 5);
  return `${dur} ${dm(w.fromMs)} ${hm(w.fromMs)}–${dm(w.toMs)} ${hm(w.toMs)}`;
}

/** Tam pencere (ipucu): "16.10.2026 14:00:00 – 16.10.2026 15:00:00". */
export function evidenceWindowFull(w: ChatQueryEvidence['window']): string {
  if (!w || !(w.toMs > w.fromMs) || w.fromMs <= 0) return '';
  return `${fmtDateTime(w.fromMs)} – ${fmtDateTime(w.toMs)}`;
}

/** Kaynak dışındaki parçalar (sıra sabit; boş olan atlanır). */
export function evidenceChipParts(ev: ChatQueryEvidence): string[] {
  const parts: string[] = [];
  const win = evidenceWindowLabel(ev.window);
  if (win) parts.push(win);
  const scope = [ev.env, ev.cluster].filter(Boolean).join('/');
  if (scope) parts.push(scope);
  if (typeof ev.rows === 'number') parts.push(`${ev.rows} satır`);
  else if (typeof ev.series === 'number') parts.push(`${ev.series} seri`);
  if (typeof ev.durationMs === 'number') parts.push(fmtMs(ev.durationMs));
  return parts;
}

/** Erişilebilir ad / düz metin: "ClickHouse · 1h 14:00–15:00 · prod · 2 satır · 37 ms". */
export function evidenceChipText(ev: ChatQueryEvidence): string {
  return [ev.source, ...evidenceChipParts(ev)].join(' · ');
}

const KIND_LABEL: Record<string, string> = { promql: 'PromQL', sql: 'SQL', summary: 'okuyucu özeti' };

/** Sorgu türü etiketi (bilinmeyen tür olduğu gibi). */
export function evidenceKindLabel(kind: string): string {
  return KIND_LABEL[kind] ?? kind;
}

/**
 * Yalnız kök-göreli uygulama yolu tıklanır (sunucu zaten öyle üretir; arşivden
 * gelen kanıt istemciden geçtiği için burada da sınır). "//host" ve "/\host" asla.
 */
export function evidenceHref(ev: ChatQueryEvidence): string | null {
  const h = ev.href ?? '';
  if (!h.startsWith('/') || h.startsWith('//') || h.startsWith('/\\')) return null;
  return h;
}

/**
 * evidenceDefaultOpen — varsayılan açık çip: bloğu olan SON kanıtın ref'i
 * (cevabın son araç sonucu); hiçbirinin bloğu yoksa null.
 */
export function evidenceDefaultOpen(
  items: readonly ChatQueryEvidence[], blocks: ReadonlyMap<string, EvidenceBlockView> | undefined,
): string | null {
  for (let i = items.length - 1; i >= 0; i--) {
    const r = items[i].ref;
    if (r && blocks?.has(r)) return r;
  }
  return null;
}

/** Veri kipi chart yükü → birim başına CorePanel kalemleri (nokta ms → ns). */
export function evidenceChartGroups(p: ChatEvidenceChartPayload): { unit: string; items: CorePanelMultiItem[] }[] {
  const byUnit = new Map<string, CorePanelMultiItem[]>();
  for (const s of p.series ?? []) {
    if (!Array.isArray(s.points) || s.points.length === 0) continue;
    const name = s.group ? `${s.name} · ${s.group}` : s.name;
    const item: CorePanelMultiItem = {
      name,
      role: s.name === 'error_rate' || s.name === 'errors' ? 'error' : 'data',
      series: [{ groupKey: [name], points: s.points.map(([t, v]) => ({ time: t * 1e6, value: v })) }],
    };
    const u = s.unit ?? '';
    byUnit.set(u, [...(byUnit.get(u) ?? []), item]);
  }
  return [...byUnit.entries()].map(([unit, items]) => ({ unit, items }));
}

/** Tablo hücresinin düz metni (sayı 4 anlamlı basamak; boş "—"). */
export function evidenceCellText(v: unknown, kind: string): string {
  if (v == null || v === '') return '—';
  if (kind === 'num' && typeof v === 'number') return String(Number(v.toPrecision(4)));
  return String(v);
}

/** Log seviyesi rozeti: yalnız sapan seviye renk alır (tablo standardı). */
export function evidenceSeverityTone(sev: string): Tone | null {
  const s = sev.toUpperCase();
  if (s.startsWith('ERR') || s.startsWith('FATAL') || s.startsWith('CRIT')) return 'danger';
  if (s.startsWith('WARN')) return 'warning';
  return null;
}
