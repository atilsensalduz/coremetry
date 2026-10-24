// contextPackForm.test.ts — Context Pack formu: Go NormalizeContextPackSettings
// sınırlarının aynası, boş kutu = varsayılan, baseline sırası sunucu sözlüğü.
import { describe, it, expect } from 'vitest';
import { packDefaultForm, packFormToWire, packToForm, toggleBaseline, validatePackForm } from './contextPackForm';

describe('contextPackForm', () => {
  it('varsayılan form Go varsayılanı (kapalı, boş kutular, previous + week_before)', () => {
    expect(packDefaultForm()).toEqual({ enabled: false, maxTokens: '', topAnomalies: '', baselines: ['previous', 'week_before'] });
    expect(packToForm(null)).toEqual(packDefaultForm());
  });
  it('tel → form: bilinmeyen baseline düşer, boş liste varsayılana', () => {
    expect(packToForm({ enabled: true, maxTokens: 3000, topAnomalies: 0, baselines: ['day_before', 'x' as never] }))
      .toEqual({ enabled: true, maxTokens: '3000', topAnomalies: '', baselines: ['day_before'] });
    expect(packToForm({ enabled: false, maxTokens: 0, topAnomalies: 3, baselines: [] }).baselines).toEqual(['previous', 'week_before']);
  });
  it.each([
    ['maxTokens', '511', '512–8000 aralığında olmalı'],
    ['maxTokens', '8001', '512–8000 aralığında olmalı'],
    ['maxTokens', '1.5', 'Tam sayı olmalı'],
    ['topAnomalies', '0', '1–10 aralığında olmalı'],
    ['topAnomalies', '11', '1–10 aralığında olmalı'],
  ] as const)('%s=%s → %s', (k, v, msg) => {
    expect(validatePackForm({ ...packDefaultForm(), [k]: v })[k]).toBe(msg);
  });
  it('baseline boş → hata, PUT yok', () => {
    const r = packFormToWire({ ...packDefaultForm(), baselines: [] });
    expect(r.ok).toBe(false);
  });
  it('form → tel: boş kutu varsayılan, sınır değerler geçer, baseline sunucu sırasıyla', () => {
    const r = packFormToWire({ enabled: true, maxTokens: '8000', topAnomalies: '', baselines: ['week_before', 'previous'] });
    expect(r).toEqual({ ok: true, wire: { enabled: true, maxTokens: 8000, topAnomalies: 5, baselines: ['previous', 'week_before'] } });
  });
  it('toggleBaseline ekler / çıkarır', () => {
    expect(toggleBaseline(['previous'], 'day_before')).toEqual(['previous', 'day_before']);
    expect(toggleBaseline(['previous', 'day_before'], 'previous')).toEqual(['day_before']);
  });
});
