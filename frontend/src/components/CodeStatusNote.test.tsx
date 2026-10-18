// @vitest-environment jsdom
//
// v0.10.1330 — stacktrace'siz trace'te "Kod incelemesi" ana operasyonun
// koduna düşer; durum satırı (codeStatusLine) iki CopilotExplain kartında aynı.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { CopilotExplain } from './CopilotExplain';
import { __resetCopilotEnabledCache } from './ai/useCopilotEnabled';
import { api } from '@/lib/api';
import type { AICodeContext } from '@/lib/types';
import { codeStatusLine, codeStatusOperationTail } from './codeSourceRef';

const OP = 'LOAN_CONSUMER_APP_APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST';
const opOK: AICodeContext = {
  repo: 'loan-service', branch: 'release', source: 'operation', operation: OP, operationSource: 'oracle',
  otherOperations: ['A_OTHER_REST', 'B_OTHER_REST'], operationLookup: 'ok',
  files: [
    { path: '/src/main/java/com/bank/rest/CreditProductInformationController.java', fromLine: 123, toLine: 140, kind: 'operation', url: 'https://tfs/x?line=123' },
    { path: '/src/main/java/com/bank/manager/CreditProductInformationManagerImpl.java', fromLine: 9, toLine: 17, kind: 'callee' },
  ],
};

describe('codeStatusLine', () => {
  it('operasyon kodundan okunduysa dosya#satır + operasyon + diğerleri', () => {
    const s = codeStatusLine(opOK);
    expect(s).toMatchObject({ kind: 'operation', operation: OP, others: 2,
      file: { label: 'CreditProductInformationController.java#L123', url: 'https://tfs/x?line=123' } });
    expect(codeStatusOperationTail(s as { operation: string; others: number }))
      .toBe(` (operasyon: ${OP}, +2 diğer operasyon okunmadı)`);
    expect(codeStatusOperationTail({ operation: OP, others: 0 })).toBe(` (operasyon: ${OP})`);
  });
  it('Code Search kapalı', () => {
    expect(codeStatusLine({ files: [], source: 'operation', operation: OP, operationLookup: 'off', reason: 'x' }))
      .toEqual({ kind: 'missing', detail: 'bu kayıtta stacktrace yok — operasyon kodu araması için Code Search kapalı' });
  });
  it('arama başarısız: operasyon + neden', () => {
    expect(codeStatusLine({ files: [], source: 'operation', operation: OP, operationLookup: 'failed', reason: 'isabet yok' }))
      .toEqual({ kind: 'missing', detail: `bu kayıtta stacktrace yok; operasyon ${OP} için depoda kod bulunamadı: isabet yok` });
  });
  it('operasyonsuz: eski dürüst not aynen; stack penceresinde satır yok', () => {
    expect(codeStatusLine({ files: [], reason: 'bu kayıtta stacktrace yok' }))
      .toEqual({ kind: 'missing', detail: 'bu kayıtta stacktrace yok' });
    expect(codeStatusLine({ source: 'convention', files: [{ path: 'a.java', fromLine: 1, toLine: 2, line: 2 }] })).toBeNull();
    expect(codeStatusLine(null)).toBeNull();
  });
  it('iki CopilotExplain kartı da aynı bileşenden çizer', () => {
    const src = readFileSync(resolve(__dirname, 'CopilotExplain.tsx'), 'utf8');
    expect(src.match(/<CodeStatusNote code=\{(code|codeCtx)\} \/>/g)?.length).toBe(2);
    expect(src).not.toContain('Kod okunamadı — <strong>');
  });
});

let host: HTMLDivElement;
let root: Root;
const settle = async () => { await act(async () => { await Promise.resolve(); }); };

beforeEach(() => {
  window.history.replaceState({}, '', '/');
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  __resetCopilotEnabledCache();
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
  });
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'gemma4' });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function mountTrace(code: AICodeContext) {
  vi.spyOn(api, 'copilotExplainTrace').mockResolvedValue({ explanation: 'öneri: dönüş kodu eşlemesi', exchangeId: 'x1', code });
  await act(async () => { root.render(<CopilotExplain kind="trace" id="t1" auto />); });
  await settle();
}

describe('CopilotExplain — operasyon kodu durum satırı (v0.10.1330)', () => {
  it('başarı: dosya linki + operasyon + okunmayanlar, "Kod okunamadı" YOK', async () => {
    await mountTrace(opOK);
    const t = host.textContent ?? '';
    expect(t).toContain('stacktrace yok → operasyon kodu üzerinden incelendi:');
    expect(t).toContain(`CreditProductInformationController.java#L123 ↗ (operasyon: ${OP}, +2 diğer operasyon okunmadı)`);
    expect(t).not.toContain('Kod okunamadı');
    expect(host.querySelector('a[href="https://tfs/x?line=123"]')).not.toBeNull();
  });
  it('Code Search kapalı: dürüst not + neden', async () => {
    await mountTrace({ files: [], source: 'operation', operation: OP, operationLookup: 'off' });
    const t = host.textContent ?? '';
    expect(t).toContain('Kod okunamadı');
    expect(t).toContain('bu kayıtta stacktrace yok — operasyon kodu araması için Code Search kapalı');
  });
});
