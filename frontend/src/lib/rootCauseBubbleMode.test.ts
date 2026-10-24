import { describe, it, expect } from 'vitest';
import { bubbleModeOf, bubbleCopyKeys, splitCorrelations } from './rootCauseBubbleMode';
import type { ChangedService } from './types';

// v0.10.1495 regresyon tablosu — bölüm türü problem türünden (sunucu), eski
// yanıtta metrikten; bağlantısız co-mover'lar ayrı grupta.
describe('bubbleModeOf', () => {
  const cases: [string, Parameters<typeof bubbleModeOf>[0], Parameters<typeof bubbleModeOf>[1], string][] = [
    ['prod: runtime jvm-gc → none (sunucu)', { metric: 'jvm_gc_pause_ratio', bubbleMode: 'none' }, undefined, 'none'],
    ['bubbleup cevabının mode\'u önce', { metric: 'error_rate', bubbleMode: 'errors' }, { fromNs: 0, toNs: 1, mode: 'none' }, 'none'],
    ['hata kuralı → errors', { metric: 'error_rate', bubbleMode: 'errors' }, null, 'errors'],
    ['gecikme → window', { metric: 'p99_ms', bubbleMode: 'window' }, null, 'window'],
    ['eski yanıt, hata metriği → errors', { metric: 'http_error_rate' }, null, 'errors'],
    ['eski yanıt, gecikme → window (hata dili yok)', { metric: 'p99_ms' }, null, 'window'],
  ];
  for (const [name, rc, bu, want] of cases) {
    it(name, () => expect(bubbleModeOf(rc, bu)).toBe(want));
  }
  it('metin anahtarları türe göre ayrı', () => {
    expect(bubbleCopyKeys('errors').title).not.toBe(bubbleCopyKeys('window').title);
  });
});

describe('splitCorrelations', () => {
  const c = (service: string, relation?: ChangedService['relation']): ChangedService => ({
    service, relation, baselineRate: 0, currentRate: 0, rateDeltaPct: 0, baselineErrorRate: 0, currentErrorRate: 0,
    errDeltaPct: 0, baselineP99Ms: 0, currentP99Ms: 0, p99DeltaPct: 0, score: 1, reasons: [],
  });
  it('kenarlılar tabloda, yalnız zamanla birlikte hareket edenler ayrı (sıra korunur)', () => {
    const { linked, unlinked } = splitCorrelations([c('orders-api', 'downstream'), c('shop-bff'), c('crm-api'), c('pay-api', 'upstream')]);
    expect(linked.map(x => x.service)).toEqual(['orders-api', 'pay-api']);
    expect(unlinked.map(x => x.service)).toEqual(['shop-bff', 'crm-api']);
  });
});
