// logSpikeView — v0.10.1284 (AI SRE Faz 1a): log artışı çekmecesinin SAF
// görünüm modeli. Sunucunun deterministik sonucu (mcptools.LogSpikeResult) →
// grafik serileri + x-bölgeleri, katkı tablosu yükü, boyut notları. React'ten
// bağımsız (vitest node ortamı).
import type { ChatEvidenceTablePayload, LogSpikeContributor, LogSpikeResult } from '@/lib/types';
import type { ChartTimeRegion } from '@/lib/chart/overlays';
import type { TimeChartSeries } from '@/components/charts/TimeChart';

/** Boyutun Türkçe adı (sunucunun spikeDimTR'si ile aynı sözlük). */
export function logSpikeDimLabel(dim: string): string {
  switch (dim) {
    case 'severity': return 'seviye';
    case 'service': return 'servis';
    case 'error_type': return 'hata türü';
    case 'pattern': return 'desen';
  }
  return dim;
}

/** Yüzde: tam sayıda ondalıksız, değilse tek ondalık, Türkçe virgül. */
export function trPct(v: number): string {
  const r = Math.round(v * 10) / 10;
  return Number.isInteger(r) ? String(r) : r.toFixed(1).replace('.', ',');
}

/** Normalde % hücresi: yeni → "yeni", taban bilinmiyor → "≤ x". */
export function logSpikeBaseCell(c: LogSpikeContributor): string {
  if (c.new) return 'yeni';
  if (c.baseUnknown) return `≤ ${trPct(c.basePct)}`;
  return trPct(c.basePct);
}

/** Grafik: toplam (gri çubuk) + ERROR (kırmızı çubuk), taban/artış bölgeleri. */
export function logSpikeChart(res: LogSpikeResult): { times: number[]; series: TimeChartSeries[]; regions: ChartTimeRegion[] } {
  const h = res.data?.histogram ?? [];
  const times = h.map(p => Math.round(p[0] / 1000));
  const series: TimeChartSeries[] = [
    { key: 'total', label: 'logs', data: h.map(p => p[1]), type: 'bar', axis: 'left', color: 'color-mix(in srgb, var(--text3) 45%, transparent)' },
    { key: 'error', label: 'errors', data: h.map(p => p[2]), type: 'bar', axis: 'left', color: 'var(--err)' },
  ];
  const regions: ChartTimeRegion[] = [];
  const sp = res.spike;
  if (sp && sp.toMs > sp.fromMs) {
    if (sp.baseToMs > sp.baseFromMs) {
      regions.push({ fromSec: sp.baseFromMs / 1000, toSec: sp.baseToMs / 1000, endSec: sp.baseToMs / 1000, color: 'var(--text3)', label: 'taban' });
    }
    regions.push({ fromSec: sp.fromMs / 1000, toSec: sp.toMs / 1000, endSec: sp.toMs / 1000, color: 'var(--err)', label: 'artış' });
  }
  return { times, series, regions };
}

/** Katkı tablosu yükü — sohbetteki kanıt tablosuyla aynı şekil (EvidenceTable). */
export function logSpikeContributorTable(res: LogSpikeResult): ChatEvidenceTablePayload | null {
  if (!res.contributors?.length) return null;
  return {
    title: 'artışı ne açıklıyor', evidenceRef: '',
    columns: [
      { key: 'dim', label: 'Boyut', kind: 'text' }, { key: 'value', label: 'Değer', kind: 'text' },
      { key: 'share', label: 'Artıştaki pay %', kind: 'num' }, { key: 'spikePct', label: 'Artışta %', kind: 'num' },
      { key: 'basePct', label: 'Normalde %', kind: 'text' }, { key: 'reason', label: 'Gerekçe', kind: 'text' },
    ],
    rows: res.contributors.slice(0, 5).map(c => [
      logSpikeDimLabel(c.dim), c.value, c.sharePct, c.spikePct, logSpikeBaseCell(c), c.reason,
    ]),
    truncated: !!res.contributorsHasMore,
  };
}

/** ok olmayan boyutların tek satırlık notları (okunamadı ≠ yok). */
export function logSpikeDimNotes(res: LogSpikeResult): string[] {
  const out: string[] = [];
  for (const d of res.dims ?? []) {
    if (d.state === 'ok') continue;
    const what = d.state === 'unmapped' ? 'eşlenmedi'
      : d.state === 'empty' ? 'değer yok'
        : d.state === 'skipped' ? 'atlandı'
          : `okunamadı (${d.state})`;
    out.push(`${logSpikeDimLabel(d.dim)}: ${what}${d.note ? ` — ${d.note}` : ''}`);
  }
  return out;
}

/** Artış özeti: "14:05–14:12 · ×5,9 · dakikada 1840 (taban 310)". */
export function logSpikeSummary(res: LogSpikeResult, clock: (ms: number) => string): string {
  const sp = res.spike;
  if (!sp) return '';
  const parts = [`${clock(sp.fromMs)}–${clock(sp.toMs)}`];
  if (sp.factor > 0) parts.push(`×${trPct(sp.factor)}`);
  parts.push(`dakikada ${trPct(sp.ratePerMinSpike)} (taban ${trPct(sp.ratePerMinBase)})`);
  if (sp.source === 'level_shift') parts.push('seviye değişimi');
  return parts.join(' · ');
}
