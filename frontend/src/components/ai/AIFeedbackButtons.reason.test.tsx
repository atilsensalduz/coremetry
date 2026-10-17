// @vitest-environment jsdom
//
// v0.10.1280 — CoSRE adım 8: kalıcılık + geri bildirim → ai_calls.
//
// Oy rayının iki yeni sözleşmesi (gerçek mount; hepsi çalışma-zamanı dalı):
//   (1) callId — sohbet cevabının answer.callId'si verilirse HER POST onu
//       taşır (oy somut ai_calls satırına bağlanır); verilmezse gövde bayt
//       bayt eski (alan hiç yok → sunucu saklananı korur, exchange düzeyi);
//   (2) hazır neden çipleri — YALNIZ 👎 seçiliyken; tık neden + oy (+callId)
//       gönderir, seçili çipe ikinci tık '' ile temizler, POST patlarsa seçim
//       geri alınır; yorum kutusu da callId taşır.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { AIFeedbackButtons } from './AIFeedbackButtons';
import { FEEDBACK_REASONS, feedbackReasonLabel } from '@/lib/aiFeedbackReasons';
import { api } from '@/lib/api';

let host: HTMLDivElement;
let root: Root;

const buttons = () => Array.from(host.querySelectorAll('button'));
const up = () => buttons().find(b => b.getAttribute('aria-label') === 'Cevabı faydalı işaretle')!;
const down = () => buttons().find(b => b.getAttribute('aria-label') === 'Cevabı faydasız işaretle')!;
const chip = (label: string) => buttons().find(b => (b.textContent ?? '').trim() === label);
const presetGroup = () => host.querySelector('[role="group"][aria-label="Faydasız oyunun hazır nedeni"]');

async function mount(props: { exchangeId?: string; callId?: string }) {
  await act(async () => { root.render(<AIFeedbackButtons {...props} />); });
}

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

describe('AIFeedbackButtons — callId (v0.10.1280)', () => {
  it('👍 ve 👎 callId ile gider', async () => {
    const post = vi.spyOn(api, 'postAIFeedback').mockResolvedValue({ ok: true });
    await mount({ exchangeId: 'x1', callId: 'c0ffee' });
    await act(async () => { up().click(); });
    expect(post).toHaveBeenLastCalledWith({ exchangeId: 'x1', verdict: 1, callId: 'c0ffee' });
    await act(async () => { down().click(); });
    expect(post).toHaveBeenLastCalledWith({ exchangeId: 'x1', verdict: -1, callId: 'c0ffee' });
  });

  it('callId yoksa gövdede alan HİÇ yok (eski istemci / exchange düzeyi)', async () => {
    const post = vi.spyOn(api, 'postAIFeedback').mockResolvedValue({ ok: true });
    await mount({ exchangeId: 'x1' });
    await act(async () => { down().click(); });
    const body = post.mock.calls[0][0];
    expect('callId' in body).toBe(false);
    expect('reason' in body).toBe(false);
  });

  it('yorum Gönder de callId taşır', async () => {
    const post = vi.spyOn(api, 'postAIFeedback').mockResolvedValue({ ok: true });
    await mount({ exchangeId: 'x1', callId: 'c1' });
    await act(async () => { down().click(); });
    await act(async () => {
      const ta = host.querySelector('textarea')!;
      const set = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
      set.call(ta, 'log kanıtı yok');
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => { buttons().find(b => (b.textContent ?? '').includes('Gönder'))!.click(); });
    expect(post).toHaveBeenLastCalledWith({ exchangeId: 'x1', verdict: -1, comment: 'log kanıtı yok', callId: 'c1' });
  });
});

describe('AIFeedbackButtons — 👎 hazır neden çipleri (v0.10.1280)', () => {
  it('üç çip YALNIZ 👎 seçiliyken; etiketler yanlış / eksik / alakasız', async () => {
    vi.spyOn(api, 'postAIFeedback').mockResolvedValue({ ok: true });
    await mount({ exchangeId: 'x1' });
    expect(presetGroup()).toBeNull();
    await act(async () => { up().click(); });
    expect(presetGroup()).toBeNull();
    await act(async () => { down().click(); });
    const labels = Array.from(presetGroup()!.querySelectorAll('button')).map(b => (b.textContent ?? '').trim());
    expect(labels).toEqual(['yanlış', 'eksik', 'alakasız']);
    expect(FEEDBACK_REASONS.map(r => r.value)).toEqual(['wrong', 'incomplete', 'irrelevant']);
  });

  it('çip tıkı neden + oy + callId gönderir ve seçili durumu duyurur', async () => {
    const post = vi.spyOn(api, 'postAIFeedback').mockResolvedValue({ ok: true });
    await mount({ exchangeId: 'x1', callId: 'c1' });
    await act(async () => { down().click(); });
    await act(async () => { chip('eksik')!.click(); });
    expect(post).toHaveBeenLastCalledWith({ exchangeId: 'x1', verdict: -1, reason: 'incomplete', callId: 'c1' });
    expect(chip('eksik')!.getAttribute('aria-pressed')).toBe('true');
    expect(chip('yanlış')!.getAttribute('aria-pressed')).toBe('false');
  });

  it('başka çipe geçiş yeniden POST eder; seçili çipe ikinci tık nedeni temizler', async () => {
    const post = vi.spyOn(api, 'postAIFeedback').mockResolvedValue({ ok: true });
    await mount({ exchangeId: 'x1' });
    await act(async () => { down().click(); });
    await act(async () => { chip('yanlış')!.click(); });
    await act(async () => { chip('alakasız')!.click(); });
    expect(post).toHaveBeenLastCalledWith({ exchangeId: 'x1', verdict: -1, reason: 'irrelevant' });
    await act(async () => { chip('alakasız')!.click(); });
    expect(post).toHaveBeenLastCalledWith({ exchangeId: 'x1', verdict: -1, reason: '' });
    expect(chip('alakasız')!.getAttribute('aria-pressed')).toBe('false');
  });

  it('POST patlarsa çip seçimi geri alınır', async () => {
    const post = vi.spyOn(api, 'postAIFeedback').mockResolvedValue({ ok: true });
    await mount({ exchangeId: 'x1' });
    await act(async () => { down().click(); });
    post.mockRejectedValueOnce(new Error('boom'));
    await act(async () => { chip('yanlış')!.click(); });
    expect(chip('yanlış')!.getAttribute('aria-pressed')).toBe('false');
  });

  it('kimlik değişince seçili neden taşınmaz', async () => {
    vi.spyOn(api, 'postAIFeedback').mockResolvedValue({ ok: true });
    await mount({ exchangeId: 'first' });
    await act(async () => { down().click(); });
    await act(async () => { chip('eksik')!.click(); });
    await mount({ exchangeId: 'second' });
    expect(presetGroup()).toBeNull(); // yeni cevapta oy da yok
  });

  it('etiket eşlemesi tek kaynaktan', () => {
    expect(feedbackReasonLabel('wrong')).toBe('yanlış');
    expect(feedbackReasonLabel('incomplete')).toBe('eksik');
    expect(feedbackReasonLabel('irrelevant')).toBe('alakasız');
    expect(feedbackReasonLabel('bogus')).toBe('');
    expect(feedbackReasonLabel(undefined)).toBe('');
  });
});
