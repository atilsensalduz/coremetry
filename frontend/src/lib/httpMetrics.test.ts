import { describe, it, expect } from 'vitest';
import { httpQueries, serverSel, promStr, rateWindow, seriesLabels, legendOf, relabel, scalarOf, barsOf, bucketDistribution, mergeTable, filterOptions, fmtCount, fmtRatio, fmtSeconds } from './httpMetrics';
import type { SpanMetricSeries } from './types';

// v0.10.1244 — HTTP Metrics sekmesi (Grafana panosu → VictoriaMetrics).
const ser = (labels: Record<string, string>, vals: number[]): SpanMetricSeries => ({
  groupKey: Object.entries(labels).map(([k, v]) => `${k}="${v}"`),
  points: vals.map((v, i) => ({ time: i * 1e9, value: v })),
});

describe('sorgular', () => {
  it('seçici: kaçış + isteğe bağlı route/method', () => {
    expect(promStr('a"b\\c')).toBe('"a\\"b\\\\c"');
    expect(serverSel({ app: 'shop' })).toBe('{service_name="shop"}');
    expect(serverSel({ app: 'shop', route: '/a', method: 'GET' }, 'le="0.5"'))
      .toBe('{service_name="shop", http_route="/a", http_request_method="GET", le="0.5"}');
  });
  it('rate penceresi en az 1 dk, en az 4 adım', () => {
    expect(rateWindow(600)).toBe('60s');
    expect(rateWindow(86400)).toBe(`${4 * 288}s`);
  });
  it('panodaki metrik adları ve pencere', () => {
    const q = httpQueries({ app: 'shop' }, 3600);
    expect(q.total).toBe('sum(increase(http_server_request_duration_seconds_count{service_name="shop"}[3600s]))');
    expect(q.p95).toContain('histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_seconds_bucket{service_name="shop"}[60s])))');
    expect(q.statusClass).toContain('"${1}xx"');
    expect(q.dConnErr).toContain('http_response_status_code=""');
    expect(Object.keys(q).length).toBeGreaterThanOrEqual(38);
  });
});

describe('etiket ve dönüşümler', () => {
  it('groupKey ayrıştırma ve lejant', () => {
    const l = seriesLabels({ groupKey: ['http_route="/a"', 'm="x\\"y"'] });
    expect(l).toEqual({ http_route: '/a', m: 'x"y' });
    expect(legendOf('{{m}} {{http_route}}', l)).toBe('x"y /a');
    expect(relabel([ser({ a: '1' }, [0.5])], '→ {{a}}', 100)[0]).toEqual({ groupKey: ['→ 1'], points: [{ time: 0, value: 50 }] });
  });
  it('stat son geçerli değer; boş → null', () => {
    expect(scalarOf([ser({}, [1, 2, NaN])])).toBe(2);
    expect(scalarOf([])).toBeNull();
  });
  it('çubuklar azalan, sıfırlar atılır', () => {
    expect(barsOf([ser({ r: 'a' }, [1]), ser({ r: 'b' }, [5]), ser({ r: 'c' }, [0])], 'r')).toEqual([{ label: 'b', value: 5 }, { label: 'a', value: 1 }]);
  });
  it('birikimli kovalar → kova başına', () => {
    const d = bucketDistribution([ser({ le: '+Inf' }, [10]), ser({ le: '0.1' }, [6]), ser({ le: '0.5' }, [9])]);
    expect(d).toEqual([{ label: '≤ 0.1s', value: 6 }, { label: '≤ 0.5s', value: 3 }, { label: '> 0.5s', value: 1 }]);
  });
  it('tablo birleştirme anahtarlara göre', () => {
    const rows = mergeTable({
      total: [ser({ http_request_method: 'GET', http_route: '/a' }, [10])],
      p95: [ser({ http_request_method: 'GET', http_route: '/a' }, [0.2]), ser({ http_request_method: 'POST', http_route: '/b' }, [1])],
    }, ['http_request_method', 'http_route']);
    expect(rows).toEqual([
      { http_request_method: 'GET', http_route: '/a', total: 10, p95: 0.2 },
      { http_request_method: 'POST', http_route: '/b', p95: 1 },
    ]);
  });
  it('süzgeç seçenekleri, "none" route hariç', () => {
    expect(filterOptions([ser({ http_route: '/b', http_request_method: 'POST' }, [1]), ser({ http_route: 'none', http_request_method: 'GET' }, [1])]))
      .toEqual({ routes: ['/b'], methods: ['GET', 'POST'] });
  });
});

// v0.10.1245 — prod: eksik tablo hücresi (undefined) sekmeyi düşürüyordu.
describe('biçimlendiriciler eksik değere dayanıklı', () => {
  it.each([undefined, null, NaN, Infinity, ''])('%s → —', v => {
    expect(fmtCount(v as never)).toBe('—');
    expect(fmtRatio(v as never)).toBe('—');
    expect(fmtSeconds(v as never, n => String(n))).toBe('—');
  });
  it('değerler biçimlenir', () => {
    expect(fmtRatio(0.0123)).toBe('%1.23');
    expect(fmtSeconds(0.2, n => `${n}s`)).toBe('0.2s');
    expect(fmtCount(1234, 0)).toBe((1234).toLocaleString('tr-TR'));
  });
});

describe('relabel null noktalara dayanıklı (v0.10.1245)', () => {
  it('points: null → boş', () => {
    expect(relabel([{ groupKey: ['a="1"'], points: null as never }], '{{a}}', 100)).toEqual([{ groupKey: ['1'], points: [] }]);
  });
});
