// v0.10.1306 — /metrics PromQL sekmesi URL kodeği: kanıt çipi linki
// (evidence_link.go promqlEditorHref) çözülür, editör değişikliği yabancı
// paramları koruyarak geri yazılır, imza yalnız sahip olunan paramları taşır.
import { describe, it, expect } from 'vitest';
import { applyPromqlParams, decodePromqlParams, promqlSig, PROMQL_MAX_LEN } from './metricsPromqlUrl';

const sp = (s: string) => new URLSearchParams(s);

describe('decodePromqlParams', () => {
  it('kanıt çipi linki (Thanos) çözülür', () => {
    const q = 'sum by (pod) (rate(x{cluster="c1",a="b&c"}[5m]))';
    const u = new URLSearchParams({ editor: '1', tab: 'promql', promql: q, src: 'thanos', cluster: 'c1', range: 'custom:1-2' });
    expect(decodePromqlParams(u)).toEqual({ tab: 'promql', promql: q, src: 'thanos', cluster: 'c1' });
  });
  it('VM / CH kaynakları; cluster yalnız thanos ile', () => {
    expect(decodePromqlParams(sp('tab=promql&promql=up&src=vm&cluster=c1'))).toEqual({ tab: 'promql', promql: 'up', src: 'vm', cluster: '' });
    expect(decodePromqlParams(sp('src=ch')).src).toBe('ch');
  });
  it('thanos cluster olmadan varsayılan kaynağa düşer', () => {
    expect(decodePromqlParams(sp('tab=promql&promql=up&src=thanos'))).toEqual({ tab: 'promql', promql: 'up', src: '', cluster: '' });
    expect(decodePromqlParams(sp('src=thanos&cluster=%20%20')).src).toBe('');
  });
  it('tanınmayan değerler varsayılana', () => {
    expect(decodePromqlParams(sp('tab=nope&src=influx'))).toEqual({ tab: 'builder', promql: '', src: '', cluster: '' });
    expect(decodePromqlParams(sp('')).tab).toBe('builder');
  });
  it('8 KiB üstü sorgu düşer, tam sınır kalır', () => {
    const ok = 'a'.repeat(PROMQL_MAX_LEN);
    expect(decodePromqlParams(new URLSearchParams({ promql: ok })).promql).toBe(ok);
    expect(decodePromqlParams(new URLSearchParams({ promql: ok + 'a' })).promql).toBe('');
  });
});

describe('applyPromqlParams', () => {
  it('yabancı paramlar korunur, varsayılanlar silinir', () => {
    const prev = sp('editor=1&range=1h&mq=xyz&metricsrc=vm&tab=promql&promql=old&src=thanos&cluster=c1');
    const next = applyPromqlParams(prev, { tab: 'builder', promql: '', src: '', cluster: '' });
    expect(next.toString()).toBe('editor=1&range=1h&mq=xyz&metricsrc=vm');
    expect(prev.get('promql')).toBe('old'); // prev değişmez
  });
  it('gidiş-dönüş', () => {
    const p = { tab: 'promql' as const, promql: 'rate(x{a="{b}"}[5m])\n+ 1', src: 'thanos' as const, cluster: 'ocp a' };
    expect(decodePromqlParams(applyPromqlParams(sp('range=15m'), p))).toEqual(p);
  });
  it('thanos cluster yoksa src yazılmaz; vm iken cluster silinir', () => {
    expect(applyPromqlParams(sp(''), { tab: 'promql', promql: 'up', src: 'thanos', cluster: '' }).has('src')).toBe(false);
    const n = applyPromqlParams(sp('cluster=c1'), { tab: 'promql', promql: 'up', src: 'vm', cluster: 'c1' });
    expect(n.get('src')).toBe('vm');
    expect(n.has('cluster')).toBe(false);
  });
  it('8 KiB üstü sorgu URL’e yazılmaz', () => {
    const n = applyPromqlParams(sp('promql=x'), { tab: 'promql', promql: 'a'.repeat(PROMQL_MAX_LEN + 1), src: '', cluster: '' });
    expect(n.has('promql')).toBe(false);
  });
});

describe('promqlSig', () => {
  it('range / mq değişimi imzayı değiştirmez, sorgu değişimi değiştirir', () => {
    const a = decodePromqlParams(sp('tab=promql&promql=up&range=1h&mq=a'));
    const b = decodePromqlParams(sp('tab=promql&promql=up&range=6h&mq=b'));
    const c = decodePromqlParams(sp('tab=promql&promql=up2&range=6h'));
    expect(promqlSig(a)).toBe(promqlSig(b));
    expect(promqlSig(a)).not.toBe(promqlSig(c));
  });
});
