// occRegion — v0.10.1365 (operatör-bildirimli: 08:39'da 6 kez görülüp susan
// grubun bandı grafiğin tamamını kaplıyor, "NEW · pencere boyu" yazıyordu).
import { describe, it, expect } from 'vitest';
import { occRegion, OCC_ACTIVE_BUCKETS } from './occRegion';

const S = 1e9;
// 08:36 → 09:16 (UTC), 1 dk bucket — operatörün ekranı.
const t0 = Date.UTC(2026, 9, 19, 8, 36) / 1000;
const occTimes = Array.from({ length: 41 }, (_, i) => t0 + i * 60);
const now = t0 + 40 * 60 + 30;                    // 09:16:30
const first = (t0 + 3 * 60) * S;                  // 08:39

describe('occRegion', () => {
  it('susan grup: bant son görülmenin bucket sonunda kapanır, etiket saati söyler', () => {
    const r = occRegion({ firstSeenNs: first, lastSeenNs: first, stateLabel: 'NEW', occTimes, nowSec: now })!;
    expect(r.fromSec).toBe(t0 + 180);
    expect(r.toSec).toBe(t0 + 240);                // tek bucket genişliği
    expect(r.endSec).toBe(t0 + 180);
    expect(r.label).toBe('NEW · son görülme 08:39');
    expect(r.noChronicLabel).toBe(true);
  });

  it('sürüyor: son görülme son ~2 bucket içinde → grafik sonuna, "sürüyor"', () => {
    const last = (now - OCC_ACTIVE_BUCKETS * 60 + 1) * S;
    const r = occRegion({ firstSeenNs: first, lastSeenNs: last, stateLabel: 'NEW', occTimes, nowSec: now })!;
    expect(r.toSec).toBe(occTimes[40] + 60);
    expect(r.label).toBe('NEW · sürüyor');
    expect(r.noChronicLabel).toBe(true);
  });

  it('resolved: resolvedAt\'te biter, etiket durum', () => {
    const res = (t0 + 20 * 60) * S;
    const r = occRegion({ firstSeenNs: first, lastSeenNs: first, resolvedAtNs: res, stateLabel: 'RESOLVED', occTimes, nowSec: now })!;
    expect(r.toSec).toBe(t0 + 1200);
    expect(r.endSec).toBe(t0 + 1200);
    expect(r.label).toBe('RESOLVED');
  });

  it('başka günde son görülme: tarih + saat', () => {
    const r = occRegion({ firstSeenNs: first, lastSeenNs: first, stateLabel: 'ACK', occTimes, nowSec: now + 86400 })!;
    expect(r.label).toBe('ACK · son görülme 19.10 08:39');
  });

  it('boş grafik → bant yok', () => {
    expect(occRegion({ firstSeenNs: first, lastSeenNs: first, stateLabel: 'NEW', occTimes: [], nowSec: now })).toBeUndefined();
  });
});
