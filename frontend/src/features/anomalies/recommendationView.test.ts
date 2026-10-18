// v0.10.1314 — runbookTarget: editörün elle yazdığı runbook URL'si
// navigate()'e gidiyor (react-router v6 ters bölü açık yönlendirmesi:
// "/\evil.com" v6'nın "//" kontrolünü geçip window.location.assign ile
// dışarı açılırdı). İç yalnız isSafeAppPath, dış yalnız http(s), gerisi null.
import { describe, it, expect } from 'vitest';
import { REC_PENDING, runbookTarget } from './recommendationView';
import type { SRERecommendation } from '@/lib/types';

const rec = (url: string): SRERecommendation => ({
  id: 'r1', text: 't', rationale: 'r', runbook: { title: 'rb', url }, state: REC_PENDING,
});

describe('runbookTarget (v0.10.1314)', () => {
  const CASES: Array<[string, ReturnType<typeof runbookTarget>]> = [
    ['/\\evil.com', null],
    ['//evil.com', null],
    ['/%5Cevil.com', null],
    ['/%2F%2Fevil.com', null],
    ['ftp://x', null],
    ['javascript:alert(1)', null],
    ['/api/runbooks/rb-1/execute', null],
    ['/runbook?id=x', { kind: 'internal', href: '/runbook?id=x' }],
    ['https://wiki', { kind: 'external', href: 'https://wiki' }],
    ['http://wiki.example/rb', { kind: 'external', href: 'http://wiki.example/rb' }],
  ];
  for (const [url, want] of CASES) {
    it(`${JSON.stringify(url)} → ${JSON.stringify(want)}`, () => {
      expect(runbookTarget(rec(url))).toEqual(want);
    });
  }
  it('runbook yoksa null', () => {
    expect(runbookTarget({ ...rec('/x'), runbook: undefined })).toBeNull();
  });
});
