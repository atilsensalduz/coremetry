import { describe, expect, it } from 'vitest';
import { showSREHypothesisButton } from './sreHypothesisDedup';
import type { ChatFollowup } from '@/lib/types';

// v0.10.1300 — CoSRE: hipotez doğrula çipi varken eski düğme gösterilmez (operatör 2026-10-17)
const action = { problemId: 'p-1', hypothesis: 'deploy' as const };
const chip = (problemId: string, hypothesis: string): ChatFollowup => ({
  text: `${hypothesis} hipotezini doğrula`, kind: 'hypothesis',
  action: { kind: 'sre_hypothesis', problemId, hypothesis, label: `${hypothesis} hipotezini doğrula` },
});
const text: ChatFollowup = { text: 'yavaş endpointler', kind: 'slow' };

describe('showSREHypothesisButton', () => {
  it('aynı problem + tür çipi varken düğme yok', () => {
    expect(showSREHypothesisButton(action, [text, chip('p-1', 'deploy')])).toBe(false);
  });
  it('çip yoksa düğme kalır (arşiv / eski sunucu / çip şeridi başka cevapta)', () => {
    expect(showSREHypothesisButton(action, undefined)).toBe(true);
    expect(showSREHypothesisButton(action, [])).toBe(true);
    expect(showSREHypothesisButton(action, [text])).toBe(true);
  });
  it('farklı tür ya da farklı problem çipi düğmeyi bastırmaz', () => {
    expect(showSREHypothesisButton(action, [chip('p-1', 'config')])).toBe(true);
    expect(showSREHypothesisButton(action, [chip('p-2', 'deploy')])).toBe(true);
  });
  it('bozuk yüklü çip (çizilmez) düğmeyi bastırmaz', () => {
    expect(showSREHypothesisButton(action, [chip('p-1', 'astroloji')])).toBe(true);
  });
});
