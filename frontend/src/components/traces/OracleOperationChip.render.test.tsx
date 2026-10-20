// @vitest-environment jsdom
//
// OracleOperationChip.render — v0.10.1329 (operatör-bildirimli): trace
// 6008dded… kökü POST /BSAWEB/application/directCreditPreApproval, kendi Oracle
// satırı …APPLICATIONTRX_DIRECT_CREDIT_PRE_APPROVAL_REST; çip fonksiyon kodu
// sözlüğünün en çok satırlı adayını (…APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST
// "(+15)") "Operasyon:" diye gösteriyordu.
//
// NE ÇİVİLİYOR:
//   • trace'in kendi Oracle satırı varsa çip O operasyonu gösterir, "+N" bu
//     trace'teki diğer operasyonlardır, sözlük HİÇ istenmez; ipucu/aria
//     kaynağı ("kendi Oracle hata satırları — kesin") söyler;
//   • kesin kaynak yoksa sözlük adayı ETİKETİYLE çizilir ("fonksiyon kodundan,
//     N aday"), ipucu "kesin DEĞİL" der ve adayları listeler;
//   • Oracle satırları yüklenirken (oracleRows undefined) çip bekler — tahmini
//     bir an bile göstermez;
//   • link hedefi değişmedi: fonksiyon kodunun diğer trace'leri.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { LogRow, OracleFunctionCodesResponse, SpanRow } from '@/lib/types';

const TRX = 'LOAN_CONSUMER_APP_APPLICATIONTRX_DIRECT_CREDIT_PRE_APPROVAL_REST';
const INQ = 'LOAN_CONSUMER_APP_APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST';

const dict: OracleFunctionCodesResponse = {
  enabled: true, codes: { LCA01: { ops: [INQ, TRX, 'OTHER_REST'], total: 16 } },
};
const dictHook = vi.fn((enabled: boolean) => ({ data: enabled ? dict : undefined }));
vi.mock('@/lib/queries', () => ({ useOracleFunctionCodes: (enabled: boolean) => dictHook(enabled) }));

import { OracleOperationChip } from './OracleOperationChip';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const sp = (o: Partial<SpanRow> & { spanId: string }): SpanRow => ({
  traceId: '6008dded60bb6da25ec0d11b0045e24b', parentSpanId: '', name: 'POST /BSAWEB/application/directCreditPreApproval',
  kind: 'server', serviceName: 'bsaweb', hostName: '', startTime: 1, endTime: 2, durationMs: 1,
  statusCode: 'error', statusMessage: '', attributes: {}, resourceAttributes: {}, events: null, scopeName: '', ...o,
});
const spans: SpanRow[] = [
  sp({ spanId: 'r', attributes: { FUNCTION_CODE: 'LCA01' } }),
  sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'loan-inq', name: 'GET /inq', startTime: 2 }),
];
const oracleRow = (op: string, spanId: string, timestamp: number): LogRow => ({
  id: timestamp, timestamp, severity: 17, severityText: 'ERROR', body: 'err', serviceName: 'Log Merkezi',
  traceId: spans[0].traceId, spanId, attributes: { 'operation.code': op }, resourceAttributes: {}, origin: 'oracle',
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(oracleRows: LogRow[] | undefined): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(
      <MemoryRouter>
        <OracleOperationChip spans={spans} pageRange={{ preset: '1h' }} oracleRows={oracleRows} />
      </MemoryRouter>,
    );
  });
  return host;
}

beforeEach(() => { dictHook.mockClear(); });
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  host = null; root = null;
});

describe('OracleOperationChip (v0.10.1329)', () => {
  it('kesin: trace\'in kendi Oracle satırları — TRX ana, +1 = bu trace\'teki INQ; sözlük istenmez', async () => {
    const h = await mount([oracleRow(INQ, 'c', 5), oracleRow(TRX, 'r', 9)]);
    const chip = h.querySelector('[data-op-source]') as HTMLElement;
    expect(chip.dataset.opSource).toBe('oracle');
    // v0.10.1418 — operasyon adının yanında kopyala düğmesi (⧉), değeri tam ad.
    expect(chip.textContent).toBe(`Operasyon: ${TRX}⧉ (+1)`);
    const copy = chip.querySelector('button[title="Operasyon adını kopyala"]');
    expect(copy).not.toBeNull();
    expect(chip.textContent).not.toContain('(+15)');
    expect(chip.title).toContain("Kaynak: bu trace'in kendi Oracle hata satırları (operation.code, 2 satır) — kesin.");
    expect(chip.title).toContain(`Bu trace'teki diğer operasyonlar: ${INQ}.`);
    const a = chip.querySelector('a')!;
    expect(a.getAttribute('aria-label')).toContain('kendi Oracle hata satırları');
    expect(a.getAttribute('href')).toContain('LCA01');
    expect(dictHook).toHaveBeenLastCalledWith(false);
  });

  it('yedek: kesin kaynak yoksa sözlük adayı etiketli ("fonksiyon kodundan, 16 aday"), ipucu kesin değil der', async () => {
    const h = await mount([]);
    const chip = h.querySelector('[data-op-source]') as HTMLElement;
    expect(chip.dataset.opSource).toBe('dictionary');
    expect(chip.textContent).toBe(`Operasyon (fonksiyon kodundan, 16 aday): ${INQ}⧉`); // v0.10.1418 kopyala
    expect(chip.querySelector('button[title="Operasyon adını kopyala"]')).not.toBeNull();
    expect(chip.title).toContain('kesin DEĞİL');
    expect(chip.title).toContain(`Bu kod 16 operasyonda görülüyor: ${INQ}, ${TRX}, OTHER_REST (+13 daha).`);
    const a = chip.querySelector('a')!;
    expect(a.getAttribute('aria-label')).toContain('fonksiyon kodu sözlüğü');
    expect(a.getAttribute('href')).toContain('LCA01');
    expect(dictHook).toHaveBeenLastCalledWith(true);
  });

  it('Oracle satırları yüklenirken hiçbir şey çizilmez (tahmin bir an bile görünmez)', async () => {
    const h = await mount(undefined);
    expect(h.querySelector('[data-op-source]')).toBeNull();
    expect(h.textContent).toBe('');
  });
});
