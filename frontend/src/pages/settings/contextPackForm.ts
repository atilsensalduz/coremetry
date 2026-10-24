// contextPackForm.ts — CoSRE Context Pack ayar bölümü (2026-10-24). SAF: form ↔ tel.
//
// Sınırlar ve varsayılanlar internal/sre/contextpack_settings.go'nun AYNASI
// (DefaultContextPackSettings + NormalizeContextPackSettings). Sunucu aralık
// dışını SESSİZCE kırpar; form aralık dışını alan hatası olarak gösterir ve PUT
// göndermez (sreSettingsForm.ts ile aynı ders). Boş kutu = varsayılan.
import type { SREContextPackBaseline, SREContextPackSettings } from '@/lib/types';

export const PACK_MAX_TOKENS = { min: 512, max: 8000, def: 2000 } as const;
export const PACK_TOP_ANOMALIES = { min: 1, max: 10, def: 5 } as const;

/** Sunucunun doğruladığı baseline türleri — gösterim sırası. */
export const PACK_BASELINES: readonly { value: SREContextPackBaseline; label: string; hint: string }[] = [
  { value: 'previous', label: 'Önceki pencere', hint: 'araştırma penceresinin hemen öncesi' },
  { value: 'day_before', label: 'Dün aynı saat', hint: '24 saat önce' },
  { value: 'week_before', label: 'Geçen hafta aynı saat', hint: '7 gün önce' },
];
export const PACK_DEFAULT_BASELINES: readonly SREContextPackBaseline[] = ['previous', 'week_before'];

export interface PackForm {
  enabled: boolean;
  maxTokens: string;
  topAnomalies: string;
  baselines: SREContextPackBaseline[];
}
export type PackFormErrors = Partial<Record<'maxTokens' | 'topAnomalies' | 'baselines', string>>;

export function packDefaultForm(): PackForm {
  return { enabled: false, maxTokens: '', topAnomalies: '', baselines: [...PACK_DEFAULT_BASELINES] };
}

const isBaseline = (b: string): b is SREContextPackBaseline => PACK_BASELINES.some(x => x.value === b);

/** Tel → form. 0/eksik sayı boş kutu (placeholder varsayılanı gösterir). */
export function packToForm(s: Partial<SREContextPackSettings> | null | undefined): PackForm {
  if (!s) return packDefaultForm();
  const bl = (s.baselines ?? []).filter(isBaseline);
  return {
    enabled: s.enabled === true,
    maxTokens: typeof s.maxTokens === 'number' && s.maxTokens > 0 ? String(s.maxTokens) : '',
    topAnomalies: typeof s.topAnomalies === 'number' && s.topAnomalies > 0 ? String(s.topAnomalies) : '',
    baselines: bl.length ? Array.from(new Set(bl)) : [...PACK_DEFAULT_BASELINES],
  };
}

function checkInt(t: string, b: { min: number; max: number }): string | undefined {
  const v = t.trim();
  if (v === '') return undefined;
  if (!/^\d+$/.test(v)) return 'Tam sayı olmalı';
  const n = Number(v);
  return n < b.min || n > b.max ? `${b.min}–${b.max} aralığında olmalı` : undefined;
}

export function validatePackForm(f: PackForm): PackFormErrors {
  const e: PackFormErrors = {};
  const mt = checkInt(f.maxTokens, PACK_MAX_TOKENS);
  if (mt) e.maxTokens = mt;
  const ta = checkInt(f.topAnomalies, PACK_TOP_ANOMALIES);
  if (ta) e.topAnomalies = ta;
  if (f.baselines.length === 0) e.baselines = 'En az bir referans seçin';
  return e;
}

/** Form → tel. Boş kutu varsayılan değerle gider (sunucu 0'ı da varsayılana çevirir;
 *  açık değer denetim kaydında okunur). Baselines gösterim sırasıyla. */
export function packFormToWire(f: PackForm):
  { ok: true; wire: SREContextPackSettings } | { ok: false; errors: PackFormErrors } {
  const errors = validatePackForm(f);
  if (Object.keys(errors).length) return { ok: false, errors };
  return {
    ok: true,
    wire: {
      enabled: f.enabled,
      maxTokens: f.maxTokens.trim() ? Number(f.maxTokens) : PACK_MAX_TOKENS.def,
      topAnomalies: f.topAnomalies.trim() ? Number(f.topAnomalies) : PACK_TOP_ANOMALIES.def,
      baselines: PACK_BASELINES.map(b => b.value).filter(v => f.baselines.includes(v)),
    },
  };
}

export function toggleBaseline(cur: SREContextPackBaseline[], b: SREContextPackBaseline): SREContextPackBaseline[] {
  return cur.includes(b) ? cur.filter(x => x !== b) : [...cur, b];
}
