import { describe, it, expect } from 'vitest';
import { servicesFilterParams, servicesFilterSearch, servicesFilterFromSearch,
         type ServicesFilterState } from './servicesFilterParams';

// v0.9.1336 regresyonu (denetim K5) — /services filtreleri URL'e yazılmıyordu.
// Copy link ve yenileme operatörün kurduğu daraltmayı kaybediyordu. Dördü
// (ownerTeam/sreTeam/cluster/namespace) URL'den yalnız MOUNT'ta okunuyordu,
// geri hiç yazılmıyordu — bu deponun tek-yön-okuma sınıfı, üç kez gemiye
// gitti (v0.8.256, v0.8.265, v0.8.267).

const EMPTY: ServicesFilterState = {
  committedFilter: '', errorsOnly: false, minSpans: '', minP99: '',
  ownerTeam: '', sreTeam: '', cluster: '', namespace: '',
};

describe('servicesFilterParams', () => {
  it('sekiz filtrenin TAMAMI sahipleniliyor', () => {
    // Sahiplik = adı geçmek. Bir filtre listeden düşerse URL'e hiç yazılmaz
    // VE temizlenemez — sessizce yapışır. Sayı burada çivili.
    const keys = servicesFilterParams(EMPTY).map(([k]) => k);
    expect(keys).toEqual([
      'q', 'err', 'minSpans', 'minP99',
      'ownerTeam', 'sreTeam', 'cluster', 'namespace',
    ]);
  });

  it('boş filtre = anahtarın SİLİNMESİ (yapışmıyor)', () => {
    const prev = '?q=payments&cluster=prod&ownerTeam=core-banking';
    expect(servicesFilterSearch(prev, EMPTY)).toBe('');
  });

  it('errorsOnly boolean → "1" / silme', () => {
    expect(servicesFilterSearch('', { ...EMPTY, errorsOnly: true })).toBe('err=1');
    expect(servicesFilterSearch('err=1', { ...EMPTY, errorsOnly: false })).toBe('');
  });
});

describe('yabancı parametreler KORUNUYOR', () => {
  // Bu blok K5'in asıl riski. rebuildPreserving'in kendi testleri var ama
  // burada çivilenen şey ENTRIES LİSTESİNİN İÇERİĞİ: bu dosyaya yanlışlıkla
  // `page` eklemek testleri değil, ÜRÜNÜ bozar.
  it('page KORUNUYOR — setPage onu ham search üzerinden yazıyor', () => {
    const out = servicesFilterSearch('page=12', { ...EMPTY, cluster: 'prod' });
    expect(out).toContain('cluster=prod');
    expect(out).toContain('page=12');
  });

  it('DataTable sıralaması (s_*) KORUNUYOR — v0.9.878 (K9) vakası', () => {
    // O gün: paylaşılan sıralama linki yazıldığı an siliniyordu, alıcı
    // BAŞLIKTA p99 görüp sunucuda count sıralaması alıyordu.
    const out = servicesFilterSearch('s_services=p99.desc', { ...EMPTY, sreTeam: 'sre-a' });
    expect(out).toContain('s_services=p99.desc');
  });

  it('ai / env / range KORUNUYOR — çekmece kapanmaz, global eksen düşmez', () => {
    const out = servicesFilterSearch(
      'ai=1&env=prod&range=6h', { ...EMPTY, minP99: '500' });
    expect(out).toContain('ai=1');
    expect(out).toContain('env=prod');
    expect(out).toContain('range=6h');
    expect(out).toContain('minP99=500');
  });

  it('idempotent — ikinci yazım aynı dizeyi üretir', () => {
    // rebuildPreserving'in sıra-kararlılık sözleşmesi (urlState.ts:158).
    // Değilse efektin karşılaştırması hiç eşleşmez ve sonsuz döngü kurar.
    const f = { ...EMPTY, cluster: 'prod', ownerTeam: 'core-banking' };
    const once = servicesFilterSearch('page=3&ai=1', f);
    expect(servicesFilterSearch(once, f)).toBe(once);
  });
});

// v0.10.1216 — operatör: paylaşılan /services linki filtreleri kaybediyordu.
describe('servicesFilterFromSearch', () => {
  it('yazıcıyla round-trip', () => {
    const f = { committedFilter: 'pay', errorsOnly: true, minSpans: '100', minP99: '250',
      ownerTeam: '', sreTeam: '', cluster: '', namespace: '' };
    const qs = servicesFilterSearch('page=2', f);
    expect(servicesFilterFromSearch(qs)).toEqual({ committedFilter: 'pay', errorsOnly: true, minSpans: '100', minP99: '250' });
    // okunan state yazıcıya geri verilince URL değişmez (silme döngüsü yok)
    expect(servicesFilterSearch(qs, { ...f, ...servicesFilterFromSearch(qs) })).toBe(qs);
  });
  it('bozuk/eksik → varsayılan', () => {
    expect(servicesFilterFromSearch('err=yes&minSpans=abc&minP99=-5')).toEqual({ committedFilter: '', errorsOnly: false, minSpans: '', minP99: '' });
  });
});
