// searchUrl — v0.10.1335 (/search URL codec'i). Çiviler: tur-dönüşü,
// varsayılanların URL'e yazılmaması, yabancı paramların (range/env/ai)
// korunması, sel kodlaması, imza bekçisi, istek parametresi kanonu,
// "N gizli" türetimi ve ⌘K "tüm sonuçlar" hedefi.
import { describe, it, expect } from 'vitest';
import {
  SEARCH_DEFAULTS, SEARCH_TYPES, decodeSearchUrl, decodeSel, encodeSel, hiddenByFilters,
  isUnfaceted, searchApiParams, searchSeeAllHref, searchUrlSig, writeSearchUrl,
  type SearchUrlState,
} from './searchUrl';
import type { SearchResponse } from './types';

const sp = (s: string) => new URLSearchParams(s);

describe('searchUrl — tur-dönüşü (v0.10.1335)', () => {
  it('boş URL varsayılanlara çözülür', () => {
    expect(decodeSearchUrl(sp(''))).toEqual(SEARCH_DEFAULTS);
  });

  it('tam durum yazılıp okununca aynı kalır', () => {
    const st: SearchUrlState = {
      q: 'checkout', types: ['problem', 'trace'], service: 'shop-api', sev: 'warning',
      status: 'all', sort: 'newest', group: 'type', sel: { type: 'endpoint', id: 'shop-api\x1fGET /x:y' },
    };
    const back = decodeSearchUrl(writeSearchUrl(sp(''), st));
    expect(back).toEqual(st);
  });

  it('types kanonik sırada yazılır, bilinmeyen tür düşer', () => {
    const next = writeSearchUrl(sp(''), { types: ['log', 'service'] });
    expect(next.get('types')).toBe('service,log');
    expect(decodeSearchUrl(sp('types=log,bogus,service,log')).types).toEqual(['service', 'log']);
  });
});

describe('searchUrl — varsayılanlar URL’e yazılmaz', () => {
  it('varsayılan değerler paramı SİLER', () => {
    const dirty = sp('q=x&types=log&service=a&sev=info&status=all&sort=oldest&group=type&sel=log:1');
    const next = writeSearchUrl(dirty, { ...SEARCH_DEFAULTS, q: 'x' });
    expect(next.toString()).toBe('q=x');
  });

  it('tüm türler seçiliyken types yazılmaz', () => {
    expect(writeSearchUrl(sp(''), { types: [...SEARCH_TYPES] }).has('types')).toBe(false);
  });

  it('q kırpılarak yazılır, boş q silinir', () => {
    expect(writeSearchUrl(sp(''), { q: '  foo ' }).get('q')).toBe('foo');
    expect(writeSearchUrl(sp('q=foo'), { q: '   ' }).has('q')).toBe(false);
  });
});

describe('searchUrl — yabancı paramlar korunur', () => {
  it('range / env / ai yazımdan sağ çıkar', () => {
    const next = writeSearchUrl(sp('range=6h&env=prod&ai=trace:abc&q=old'), { q: 'new', sort: 'severity' });
    expect(next.get('range')).toBe('6h');
    expect(next.get('env')).toBe('prod');
    expect(next.get('ai')).toBe('trace:abc');
    expect(next.get('q')).toBe('new');
  });

  it('patch dışındaki SAHİP paramlara da dokunulmaz', () => {
    const next = writeSearchUrl(sp('q=a&service=s1&sev=critical'), { group: 'type' });
    expect(next.get('service')).toBe('s1');
    expect(next.get('sev')).toBe('critical');
  });
});

describe('searchUrl — sel kodlaması', () => {
  it('tür öneki ilk ":"a kadar; kimlik ":" ve \\x1f taşıyabilir', () => {
    const sel = { type: 'endpoint' as const, id: 'svc\x1fGET /a:b' };
    expect(decodeSel(encodeSel(sel))).toEqual(sel);
    // URLSearchParams ikinci kez kodlar; get() bir kez çözer, decodeSel ikinciyi.
    const next = writeSearchUrl(sp(''), { sel });
    expect(decodeSearchUrl(sp(next.toString())).sel).toEqual(sel);
  });

  it('bilinmeyen tür / boş kimlik / bozuk kodlama → null', () => {
    expect(decodeSel('bogus:1')).toBeNull();
    expect(decodeSel('trace:')).toBeNull();
    expect(decodeSel('trace')).toBeNull();
    expect(decodeSel('log:%E0%A4%A')).toBeNull();
    expect(decodeSel(null)).toBeNull();
  });
});

describe('searchUrlSig — içe aktarma bekçisi', () => {
  it('yabancı param ve sel imzayı DEĞİŞTİRMEZ', () => {
    const base = searchUrlSig(sp('q=foo&service=a'));
    expect(searchUrlSig(sp('q=foo&service=a&range=6h&env=prod&ai=x'))).toBe(base);
    expect(searchUrlSig(sp('q=foo&service=a&sel=trace:abc'))).toBe(base);
  });

  it('sahip süzgeç değişimi imzayı değiştirir', () => {
    const base = searchUrlSig(sp('q=foo'));
    expect(searchUrlSig(sp('q=bar'))).not.toBe(base);
    expect(searchUrlSig(sp('q=foo&types=log'))).not.toBe(base);
    expect(searchUrlSig(sp('q=foo&group=type'))).not.toBe(base);
  });

  it('varsayılanla yazılmış ve hiç yazılmamış URL aynı imza', () => {
    expect(searchUrlSig(sp('q=foo&sort=relevance&status=open&group=list'))).toBe(searchUrlSig(sp('q=foo')));
  });
});

describe('searchApiParams — kanonik istek', () => {
  const win = { from: 1, to: 2 };
  it('varsayılanlar gönderilmez', () => {
    expect(searchApiParams({ ...SEARCH_DEFAULTS, q: ' foo ' }, '', win)).toEqual({ q: 'foo', from: 1, to: 2 });
  });
  it('facet + env + status/sort taşınır', () => {
    const p = searchApiParams({ ...SEARCH_DEFAULTS, q: 'x', types: ['trace'], service: 's', sev: 'critical', status: 'all', sort: 'newest' }, 'prod', win);
    expect(p).toEqual({ q: 'x', from: 1, to: 2, types: 'trace', service: 's', env: 'prod', sev: 'critical', status: 'all', sort: 'newest' });
    expect(isUnfaceted(p)).toBe(false);
  });
});

describe('hiddenByFilters — "N gizli" yalnız önbellekten', () => {
  const resp = (total: number): SearchResponse => ({
    results: [], facets: { type: {}, service: {}, env: {}, sev: {}, scope: '' }, sources: {}, total,
    queryKind: 'text', fromNs: 1, toNs: 2,
  });
  const cur = { q: 'x', from: 1, to: 2, service: 's' };
  it('facet’siz önbellek cevabı → toplamı', () => {
    expect(hiddenByFilters([[{ q: 'x', from: 1, to: 2, sort: 'newest' }, resp(7)]], cur)).toBe(7);
  });
  it('önbellek yoksa / başka pencere ise → null (sayı uydurulmaz)', () => {
    expect(hiddenByFilters([], cur)).toBeNull();
    expect(hiddenByFilters([[{ q: 'x', from: 1, to: 3 }, resp(7)]], cur)).toBeNull();
    expect(hiddenByFilters([[{ q: 'x', from: 1, to: 2, env: 'prod' }, resp(7)]], cur)).toBeNull();
  });
  it('mevcut istek zaten facet’siz → null', () => {
    expect(hiddenByFilters([[{ q: 'x', from: 1, to: 2 }, resp(7)]], { q: 'x', from: 1, to: 2 })).toBeNull();
  });
});

describe('searchSeeAllHref — ⌘K hedefi', () => {
  it('q ≥ 2 kodlanmış /search?q=; kısa → null', () => {
    expect(searchSeeAllHref(' a&b ')).toBe('/search?q=a%26b');
    expect(searchSeeAllHref('a')).toBeNull();
  });
});
