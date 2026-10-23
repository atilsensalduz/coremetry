// rolloutOrigin.test.ts — v0.10.1445 — operatör: "KSM dedektörü doğru mu".
// Rollouts satırının git kaynağı rozeti: yalnız sapmada (rollback (git) /
// redeploy (older)); forward / unknown / origin yok → tabloda rozet YOK.
// Rozet kelimesi İngilizce (v0.10.1421 kuralı), ipucu prev → new revizyonu taşır.
import { describe, it, expect } from 'vitest';
import { originBadge, originLabel, originTitle, originAppLine, shortGitRevision } from './rolloutRow';
import type { RolloutOrigin } from './types';

const TR = /[çğıöşüÇĞİÖŞÜ]|tamamland|geri al|devral|takıld|sürüyor|ilk gözlem|bilinmiyor/;

const base: RolloutOrigin = {
  kind: 'rollback', confidence: 95, basis: 'argocd_webhook',
  reason: 'Argo re-pointed to an earlier synced revision and Kubernetes re-activated the known ReplicaSet',
  prevRevision: '4b7d0aa3c1d2e3f4', newRevision: '9f3c2e1b0a9b8c7d', firstSyncedAt: Date.UTC(2026, 9, 18, 9, 12),
  prevTag: '2026.10.18.3', newTag: '2026.10.17.1', tagOrder: 'older',
  app: { instanceId: 'argo-main', appNamespace: 'argocd', appName: 'shop-checkout-prod', matchClass: 'exact' },
  automated: true, historyDepth: 7,
};

describe('v0.10.1445 — originBadge', () => {
  it.each<[string, string | null, string | null]>([
    ['rollback', 'Rollback (git)', 'danger'],
    ['redeploy_older', 'Redeploy (older)', 'warning'],
    ['forward', null, null],
    ['unknown', null, null],
  ])('%s → %s', (kind, label, tone) => {
    const b = originBadge({ origin: { ...base, kind } });
    if (label === null) { expect(b).toBeNull(); return; }
    expect(b?.label).toBe(label);
    expect(b?.tone).toBe(tone);
    expect(b?.label).not.toMatch(TR);
  });

  it('origin yok (v1 satırı / Argo kapalı) → rozet yok', () => {
    expect(originBadge({})).toBeNull();
  });

  it('etiketler İngilizce', () => {
    for (const k of ['rollback', 'redeploy_older', 'forward', 'unknown', 'weird']) expect(originLabel(k)).not.toMatch(TR);
  });
});

describe('v0.10.1445 — originTitle', () => {
  it('rollback: prev → new kısa sha + ilk senkron + güven + gerekçe', () => {
    const t = originTitle(base);
    expect(t).toContain('4b7d0aa → 9f3c2e1');
    expect(t).toContain('18.10.2026');
    expect(t).toContain('güven %95');
    expect(t).toContain(base.reason);
  });

  it('redeploy_older: imaj tag yönü', () => {
    const t = originTitle({ ...base, kind: 'redeploy_older', confidence: 70, firstSyncedAt: undefined });
    expect(t).toContain('4b7d0aa → 9f3c2e1');
    expect(t).toContain('2026.10.18.3 → 2026.10.17.1');
  });

  it('kanıt yok: güven yazılmaz, gerekçe gelir', () => {
    const t = originTitle({ kind: 'unknown', confidence: 0, basis: 'none', reason: 'no Argo sync matched this rollout (±5m); git source unknown' });
    expect(t).not.toContain('güven');
    expect(t).toContain('no Argo sync matched');
  });

  it('app satırı + kısa revizyon', () => {
    expect(originAppLine(base)).toBe('argocd/shop-checkout-prod (exact) · auto-sync · geçmiş 7 senkron');
    expect(shortGitRevision('v1.2.3')).toBe('v1.2.3');
    expect(shortGitRevision('')).toBe('?');
  });
});
