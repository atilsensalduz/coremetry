// rolloutLabels.test.ts — v0.10.1421 — Operator-reported (prod /rollouts):
// "Türkçe 'ilk gözlem' ve 'tamamlandı' vs yerine İngilizce yazsın." Durum ve
// değişiklik türü rozetleri İngilizce; tablo, çekmece başlığı, Servis GitOps
// sekmesi, kök neden paneli ve olay işaretçisi aynı sözlükten (rolloutRow)
// okur. Açıklayıcı ipuçları (statusTitle / changeKindTitle) Türkçe kalır.
import { describe, it, expect } from 'vitest';
import { statusLabel, changeKindLabel, statusTitle, type RolloutChangeKind } from './rolloutRow';
import { rolloutHeadline } from './serviceRolloutLabel';

const TR = /[çğıöşüÇĞİÖŞÜ]|tamamland|geri al|devral|takıld|sürüyor|ilk gözlem|bilinmiyor/;

describe('v0.10.1421 — rollout rozetleri İngilizce', () => {
  it.each([
    ['in_progress', 'In progress'],
    ['completed', 'Completed'],
    ['rolled_back', 'Rolled back'],
    ['superseded', 'Superseded'],
    ['stalled', 'Stalled'],
    ['', '—'],
    ['weird_value', 'weird_value'], // sözlük dışı değer aynen
  ])('statusLabel(%j) = %j', (s, want) => {
    expect(statusLabel(s)).toBe(want);
    expect(statusLabel(s)).not.toMatch(TR);
  });

  it.each<[RolloutChangeKind, string]>([
    ['deployment', 'Deployment'],
    ['config', 'Rollout (config)'],
    ['initial', 'First seen'],
    ['rollback', 'Rollback'],
    ['unknown', 'Unknown'],
  ])('changeKindLabel(%j) = %j', (k, want) => {
    expect(changeKindLabel(k)).toBe(want);
    expect(changeKindLabel(k)).not.toMatch(TR);
  });

  it('açıklayıcı ipucu Türkçe kalır (yalnız rozet kelimesi değişti)', () => {
    expect(statusTitle('rolled_back', true)).toBe('Sonraki rollout bu revizyondan önceki bir revizyona geri döndü');
  });

  it('servis olay başlığı rozet sözlüğünü kullanır', () => {
    expect(rolloutHeadline({ timeUnixNs: 1, kind: 'deploy', podsAdded: 0, podsRemoved: 0, activePods: 1, source: 'ksm',
      workloadKind: 'Deployment', workload: 'orders-api', status: 'rolled_back' })).toBe('↻ Deployment orders-api · Rolled back');
  });
});
