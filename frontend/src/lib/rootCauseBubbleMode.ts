import type { ChangedService, RootCause, RootCauseBubbleMode, RootCauseBubbleUp } from '@/lib/types';

// rootCauseBubbleMode.ts — v0.10.1495 (operatör, prod: JVM GC problemi
// "Where errors concentrate — top attribute over the error spans" ve
// `messaging.message.body.size` gösterdi; problem hatayla ilgili değildi).
//
// Kıyas bölümünün türü SUNUCUDAN gelir (Go rootCauseBubbleMode — problem
// türü): errors / window / none. Bu modül yalnız seçer ve metni verir; eski
// (alan taşımayan) önbellekli yanıtta metrikten türetir — "error" içeren hata,
// diğerleri pencere kıyası (sunucu o yanıtta da zaman-kaydırmalı koşuyordu).

export function bubbleModeOf(rc: Pick<RootCause, 'bubbleMode' | 'metric'>, bu?: RootCauseBubbleUp | null): RootCauseBubbleMode {
  const m = bu?.mode ?? rc.bubbleMode;
  if (m === 'errors' || m === 'window' || m === 'none') return m;
  return (rc.metric ?? '').toLowerCase().includes('error') ? 'errors' : 'window';
}

// bubbleCopyKeys — bölüm başlığı / alt başlık / lejant i18n anahtarları.
export function bubbleCopyKeys(mode: Exclude<RootCauseBubbleMode, 'none'>): { title: string; sub: string; legend: string } {
  return mode === 'errors'
    ? { title: 'rootCause.bubble.errors.title', sub: 'rootCause.bubble.errors.sub', legend: 'rootCause.bubble.errors.legend' }
    : { title: 'rootCause.bubble.window.title', sub: 'rootCause.bubble.window.sub', legend: 'rootCause.bubble.window.legend' };
}

// splitCorrelations — "What else changed" satırları: özneyle topoloji kenarı
// DOĞRULANMIŞ olanlar (relation dolu) tabloda; yalnız zamanla birlikte
// hareket edenler ayrı, kapalı "bağlantı doğrulanmadı" grubunda — asla karışık.
export function splitCorrelations(corr: ChangedService[]): { linked: ChangedService[]; unlinked: ChangedService[] } {
  const linked: ChangedService[] = [];
  const unlinked: ChangedService[] = [];
  for (const c of corr) (c.relation ? linked : unlinked).push(c);
  return { linked, unlinked };
}
