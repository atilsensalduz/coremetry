// v0.10.1499 — /logs görünüm anahtarı (Logs | Desenler | Şablonlar) saf kuralları.
import { describe, it, expect } from 'vitest';
import { parseLogsView, effectiveLogsView, writeLogsView, logsListEnabled } from './logsView';

describe('logsView', () => {
  it('?panel= → görünüm; yok/bilinmeyen = logs', () => {
    expect(parseLogsView(null)).toBe('logs');
    expect(parseLogsView('')).toBe('logs');
    expect(parseLogsView('bogus')).toBe('logs');
    expect(parseLogsView('patterns')).toBe('patterns');
    expect(parseLogsView('templates')).toBe('templates');
  });
  it('canlı kuyruk Logs görünümünü zorlar', () => {
    expect(effectiveLogsView('patterns', true)).toBe('logs');
    expect(effectiveLogsView('templates', false)).toBe('templates');
  });
  it('yazma: logs paramı siler, diğerleri yazar, yabancı paramlar kalır', () => {
    const prev = new URLSearchParams('q=boom&panel=patterns&range=1h');
    expect(writeLogsView(prev, 'logs').toString()).toBe('q=boom&range=1h');
    expect(writeLogsView(prev, 'templates').toString()).toBe('q=boom&panel=templates&range=1h');
    expect(prev.get('panel')).toBe('patterns'); // girdi değişmez
    expect(parseLogsView(writeLogsView(prev, 'patterns').get('panel'))).toBe('patterns');
  });
  it('liste sorgusu yalnız Logs görünümünde, canlı değil, takım kapısı kapalıyken', () => {
    expect(logsListEnabled('logs', false, false)).toBe(true);
    expect(logsListEnabled('patterns', false, false)).toBe(false);
    expect(logsListEnabled('templates', false, false)).toBe(false);
    expect(logsListEnabled('logs', true, false)).toBe(false);
    expect(logsListEnabled('logs', false, true)).toBe(false);
  });
});
