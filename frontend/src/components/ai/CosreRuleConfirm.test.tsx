// @vitest-environment jsdom
// CosreRuleConfirm.test.tsx — v0.10.1289 (CoSRE kurallar / hafıza: "bunu
// hatırla"). Sözleşme: sohbetin onay kartı HİÇBİR ŞEYİ kendiliğinden
// kaydetmez; metin ve kapsam düzenlenir, yalnız "Kaydet" tıkı POST eder.
// Viewer (canSave=false) kartı görür, düğme gerekçesiyle kapalı. Aksiyon
// ayrıştırıcısı bozuk yükü reddeder, "genel" seçeneği her zaman vardır.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({ creates: [] as unknown[][] }));
vi.mock('@/lib/api', () => ({
  api: {
    createCosreRule: vi.fn(async (...a: unknown[]) => { m.creates.push(a); return { rule: {}, rev: 1, rules: [] }; }),
  },
}));

import { CosreRuleConfirm } from './CosreRuleConfirm';
import { parseCosreRuleAction, type CosreRuleAction } from '@/lib/pageActions';
import { ruleTextError, singleLine } from './cosreRules';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.creates = []; });

async function render(action: CosreRuleAction) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => { root!.render(<QueryClientProvider client={qc}><CosreRuleConfirm action={action} /></QueryClientProvider>); });
  return host;
}

function setValue(el: HTMLTextAreaElement | HTMLSelectElement, v: string) {
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLSelectElement.prototype;
  act(() => {
    Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(el, v);
    el.dispatchEvent(new Event(el instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }));
  });
}

const button = (el: HTMLElement, text: string) =>
  Array.from(el.querySelectorAll('button')).find(b => b.textContent?.trim() === text) as HTMLButtonElement | undefined;

const payload = {
  kind: 'cosre_rule', text: "gece 02:00 batch'i 5xx üretir", label: 'Kuralı kaydet', canSave: true,
  scope: { kind: 'service', value: 'checkout' },
  scopeOptions: [
    { kind: 'service', value: 'checkout', label: 'servis checkout' },
    { kind: 'team', value: 'payments', label: 'takım payments' },
    { kind: 'global', label: 'genel' },
  ],
};

describe('parseCosreRuleAction', () => {
  it('geçerli yükü çözer, genel seçeneği her zaman var, yinelenen düşer', () => {
    const a = parseCosreRuleAction({ ...payload, scopeOptions: [...payload.scopeOptions, { kind: 'service', value: 'Checkout', label: 'x' }] })!;
    expect(a.scope).toEqual({ kind: 'service', value: 'checkout' });
    expect(a.scopeOptions.map(o => o.label)).toEqual(['servis checkout', 'takım payments', 'genel']);
    expect(a.canSave).toBe(true);
    const noGlobal = parseCosreRuleAction({ ...payload, scopeOptions: [{ kind: 'team', value: 'sre', label: 'takım sre' }] })!;
    expect(noGlobal.scopeOptions.map(o => o.label)).toEqual(['takım sre', 'servis checkout', 'genel']);
  });
  it('bozuk yükü reddeder; canSave yalnız true ise açık', () => {
    expect(parseCosreRuleAction({ ...payload, kind: 'problem_note' })).toBeNull();
    expect(parseCosreRuleAction({ ...payload, text: 42 })).toBeNull();
    expect(parseCosreRuleAction(null)).toBeNull();
    const bad = parseCosreRuleAction({ ...payload, scope: { kind: 'pod', value: 'x' }, canSave: 'yes', scopeOptions: [{ kind: 'service' }] })!;
    expect(bad.scope).toEqual({ kind: 'global' });
    expect(bad.canSave).toBe(false);
    expect(bad.scopeOptions.map(o => o.kind)).toEqual(['global']);
  });
  it('tek satır + uzunluk sınırı', () => {
    expect(singleLine('a\nb\r\nc\td')).toBe('a b c d');
    expect(ruleTextError('kısa')).toBeTruthy();
    expect(ruleTextError('yeterince uzun')).toBeUndefined();
    expect(ruleTextError('ç'.repeat(201))).toBeTruthy();
  });
});

describe('CosreRuleConfirm', () => {
  it('tıklanmadan kaydetmez; düzenlenen metin + seçilen kapsam POST edilir', async () => {
    const el = await render(parseCosreRuleAction(payload)!);
    expect(m.creates).toHaveLength(0);
    const ta = el.querySelector('textarea')!;
    expect(ta.value).toBe("gece 02:00 batch'i 5xx üretir");
    setValue(ta, "gece 02:00–02:20 batch'i\n5xx üretir, alarm değil");
    setValue(el.querySelector('select')!, 'team:payments');
    await act(async () => { button(el, 'Kaydet')!.click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(m.creates).toEqual([[{ text: "gece 02:00–02:20 batch'i 5xx üretir, alarm değil", scope: { kind: 'team', value: 'payments' } }]]);
    expect(button(el, 'Kaydedildi')?.disabled).toBe(true);
  });

  it('kısa metinde Kaydet kapalı', async () => {
    const el = await render(parseCosreRuleAction(payload)!);
    setValue(el.querySelector('textarea')!, 'kısa');
    expect(button(el, 'Kaydet')?.disabled).toBe(true);
    expect(el.textContent).toContain('En az 8 karakter');
  });

  it('viewer kartı görür, düğme gerekçesiyle kapalı, alanlar salt-okunur', async () => {
    const el = await render(parseCosreRuleAction({ ...payload, canSave: false, reason: 'Kural kaydetmek editör ya da admin rolü ister.' })!);
    const b = button(el, 'Kaydet')!;
    expect(b.disabled).toBe(true);
    expect(el.textContent).toContain('editör ya da admin');
    expect(el.querySelector('textarea')!.readOnly).toBe(true);
    expect(el.querySelector('select')!.disabled).toBe(true);
    await act(async () => { b.click(); });
    expect(m.creates).toHaveLength(0);
  });
});
