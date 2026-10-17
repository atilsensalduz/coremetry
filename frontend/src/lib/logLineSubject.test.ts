import { describe, it, expect } from 'vitest';
import { logBodyHash, logLineSubjectOf, LOG_BODY_HASH_RUNES } from './logLineSubject';
import { formatAiParam } from './aiSubject';
import type { LogRow } from './types';

// v0.10.1291 — log satırı ✨ öznesi. Sunucu ikizi internal/mcptools/
// log_line_subject.go AYNI fixture'ı pinler (TestLogBodyHash: "ca29aa39").
describe('logBodyHash', () => {
  it('Go ikiziyle aynı FNV-1a 32', () => {
    expect(logBodyHash('connection reset by peer')).toBe('ca29aa39');
  });
  it('yalnız ilk 120 kod noktası (sohbet 300 rune kırpar)', () => {
    const long = 'ğ'.repeat(400);
    expect(logBodyHash(long)).toBe(logBodyHash('ğ'.repeat(LOG_BODY_HASH_RUNES)));
    expect(logBodyHash(long)).not.toBe(logBodyHash('ğ'.repeat(LOG_BODY_HASH_RUNES - 1)));
  });
  it("FenceSafe ˋˋˋ → ``` geri dönüşü", () => {
    expect(logBodyHash('x ``` y')).toBe(logBodyHash('x ˋˋˋ y'));
  });
});

describe('logLineSubjectOf', () => {
  const row = (p: Partial<LogRow>): LogRow => ({
    id: 1, timestamp: 1700000000123000000, severity: 17, severityText: 'ERROR', body: 'connection reset by peer',
    serviceName: 'pay ments', traceId: '0AF7651916CD43DD8448EB211C80319C', spanId: '',
    attributes: {}, resourceAttributes: { 'k8s.pod.name': 'pod:1' }, ...p,
  });
  it('LogRow → sunucu kodeğiyle aynı özne', () => {
    const s = logLineSubjectOf(row({}));
    expect(s && formatAiParam(s)).toBe('log:1700000000123000000:pay%20ments:pod%3A1:0af7651916cd43dd8448eb211c80319c:ca29aa39');
  });
  it('servissiz / zamansız satır → null; geçersiz trace düşer', () => {
    expect(logLineSubjectOf(row({ serviceName: '' }))).toBeNull();
    expect(logLineSubjectOf(row({ timestamp: 0 }))).toBeNull();
    const s = logLineSubjectOf(row({ traceId: 'nope' }));
    expect(s && s.kind === 'log' ? s.traceId : 'x').toBe('');
  });
});
