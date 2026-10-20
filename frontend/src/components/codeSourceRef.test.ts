import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { codeSourceRef } from './codeSourceRef';

// v0.10.1044 (operatör: "Kod, dalın ucundan değil çalışan sürümden okunsun") —
// "Kaynak:" satırı kodun okunduğu ref'i söyler: çalışan sürüm mü, dal mı.
describe('codeSourceRef', () => {
  it('çalışan sürümün commit\'inden okunduysa sürüm', () => {
    expect(codeSourceRef({ version: '1.4.2', branch: 'release' })).toBe(' · 1.4.2 (çalışan sürüm)');
  });
  it('sürüm yoksa ya da bulunamadıysa dal', () => {
    expect(codeSourceRef({ branch: 'release' })).toBe(' · release (dal)');
  });
  it('ikisi de yoksa boş', () => {
    expect(codeSourceRef({})).toBe('');
  });
  // v0.10.1041 panel kod durumunu `code` ve `codeCtx` diye ayırdı; iki çizim
  // yeri hangi adı okursa okusun aynı fonksiyondan geçmeli.
  // v0.10.1406 — "Kaynak:" satırı tek bileşende (ai/CodeSourceFooter); CopilotExplain'in
  // iki kartı ve sohbet balonunun kod turu onu çizer (CodeSourceFooter.test.tsx).
  it('iki "Kaynak:" çizim yeri de aynı bileşenden (aynı fonksiyondan) çizer', () => {
    const src = readFileSync(resolve(__dirname, 'CopilotExplain.tsx'), 'utf8');
    expect(src.match(/<CodeSourceFooter code=\{(code|codeCtx)\} \/>/g)?.length).toBe(2);
    expect(src).not.toContain('codeSourceRef(');
    const footer = readFileSync(resolve(__dirname, 'ai/CodeSourceFooter.tsx'), 'utf8');
    expect(footer.match(/\{codeSourceRef\(code\)\}/g)?.length).toBe(1);
    expect(footer).not.toMatch(/code\.branch \? ` · \$\{code\.branch\}` : ''/);
    // Sohbet balonunun kod turu aynı bileşeni çizer (kopya yok).
    const bubble = readFileSync(resolve(__dirname, 'ai/ChatBubble.tsx'), 'utf8');
    expect(bubble).toContain('<CodeSourceFooter code={ex.code} />');
    expect(bubble).not.toContain('📄 Kaynak:');
  });
});
