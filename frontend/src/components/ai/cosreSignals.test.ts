import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';

// CoSRE öğrenme Faz 2 — örtük olumlu sinyallerin bağlanması (kaynak pinleri).
// Sinyal kaybı sessizdir; bu pinler bir refaktörde kablonun kopmasını yakalar.
const src = (f: string) => readFileSync(new URL(f, import.meta.url), 'utf8');

describe('örtük sinyal kablolaması', () => {
  it('ChatBubble kopya / link / kanıt / probleme not sinyali gönderir', () => {
    const b = src('./ChatBubble.tsx');
    expect(b).toContain("signal('copy')");
    expect(b).toContain("signal('evidence_click')");
    expect(b).toContain("onSaved={() => signal('problem_note')}");
    expect(b.match(/signal\('link_click'/g)?.length).toBe(2); // gövde linkleri + "Aç →" çipleri
    expect(b).toContain('setEvId={openEvidence}');
  });
  it('sinyal ateşle-unut ve keepalive', () => {
    const api = src('../../lib/api.ts');
    expect(api).toMatch(/aiSignal: [\s\S]{0,200}keepalive: true/);
  });
  it('ProblemNoteButton yalnız BAŞARILI kayıtta onSaved çağırır', () => {
    const p = src('./ProblemNoteButton.tsx');
    expect(p.indexOf('onSaved?.()')).toBeGreaterThan(p.indexOf("setState('done')"));
    expect(p.indexOf('onSaved?.()')).toBeLessThan(p.indexOf('} catch'));
  });
});
