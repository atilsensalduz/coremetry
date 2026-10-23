import { describe, expect, it } from 'vitest';
import type { ChatTurn } from '@/lib/types';
import {
  PERSIST_MESSAGE_CAP, hasCompletedExchange, persistMessages, restoreTurns, turnArchiveId,
} from './chatPersist';

// v0.9.1139 (AI Faz 4.1) — arşive NE gider, ekrana NE geri döner.
//
// Üç iddia sessizce kırılabilir:
//   1. HATA turları arşive girmemeli. "Sunucuya ulaşamadım" satırı
//      geçmişte hiçbir işe yaramaz; dahası bir sonraki gönderimde tele
//      geri konup modele "önceki cevabım buydu" diye sunulurdu;
//   2. AKAN (pending) tur yarım metin taşır — kaydetme akış BİTİNCE
//      tetiklenir ama zamanlayıcı yarışırsa yarım cevap kalıcılaşır;
//   3. Geri yüklenen cevaplar exchangeId TAŞIMAZ, yani 👍/👎 gizli
//      kalır (arşiv cevabına oy = ölü affordance, v0.9.592 dersi).

const t = (turn: Partial<ChatTurn> & { role: ChatTurn['role'] }): ChatTurn => turn as ChatTurn;

describe('persistMessages', () => {
  it('tamamlanmış turları role+text olarak taşır (v0.10.1444 — asistan + exchangeId)', () => {
    expect(persistMessages([
      t({ role: 'user', text: 'checkout yavaş mı?' }),
      t({ role: 'assistant', text: 'p95 480ms', exchangeId: 'x1', steps: ['red'] }),
    ])).toEqual([
      { role: 'user', text: 'checkout yavaş mı?' },
      { role: 'assistant', text: 'p95 480ms', exchangeId: 'x1' },
    ]);
  });

  it('hata / akan / boş turlar DÜŞER', () => {
    expect(persistMessages([
      t({ role: 'user', text: 'soru' }),
      t({ role: 'assistant', text: 'yarım', pending: true }),
      t({ role: 'assistant', text: '', error: 'stream kesildi' }),
      t({ role: 'assistant', text: '   ' }),
    ])).toEqual([{ role: 'user', text: 'soru' }]);
  });

  it('son 40 ile sınırlı (tavan sunucuda da var)', () => {
    const many = Array.from({ length: 90 }, (_, i) =>
      t({ role: i % 2 ? 'assistant' : 'user', text: `m${i}` }));
    const got = persistMessages(many);
    expect(got.length).toBe(PERSIST_MESSAGE_CAP);
    // En YENİLER kalır.
    expect(got[got.length - 1].text).toBe('m89');
    expect(got[0].text).toBe('m50');
  });
});

describe('restoreTurns', () => {
  it('mesajları çizilebilir turlara çevirir', () => {
    expect(restoreTurns([
      { role: 'user', text: 'soru' },
      { role: 'assistant', text: 'cevap' },
    ])).toEqual([
      { role: 'user', text: 'soru' },
      { role: 'assistant', text: 'cevap' },
    ]);
  });

  it('geri yüklenen cevap exchangeId TAŞIMAZ (oylama gizli kalır)', () => {
    const [, answer] = restoreTurns([
      { role: 'user', text: 'soru' },
      { role: 'assistant', text: 'cevap' },
    ]);
    expect(answer.exchangeId).toBeUndefined();
  });

  it('boş/eksik girdi boş turlar', () => {
    expect(restoreTurns(undefined)).toEqual([]);
    expect(restoreTurns([])).toEqual([]);
    expect(restoreTurns([{ role: 'assistant', text: '  ' }])).toEqual([]);
  });
});

describe('hasCompletedExchange', () => {
  it('soru + tamamlanmış cevap varsa true', () => {
    expect(hasCompletedExchange([
      t({ role: 'user', text: 'soru' }),
      t({ role: 'assistant', text: 'cevap' }),
    ])).toBe(true);
  });

  it('yalnız soru (ya da yalnız hatalı cevap) varsa false — boş kabuk açılmaz', () => {
    expect(hasCompletedExchange([t({ role: 'user', text: 'soru' })])).toBe(false);
    expect(hasCompletedExchange([
      t({ role: 'user', text: 'soru' }),
      t({ role: 'assistant', text: '', error: 'kesildi' }),
    ])).toBe(false);
    expect(hasCompletedExchange([])).toBe(false);
  });
});

// v0.10.1444 — arşiv turun KİMLİĞİNİ taşır (sunucu blok zarfını bununla bulur),
// blokları ASLA (64 KB blob; sunucu zaten düşürür). Geri yükleme sunucunun
// doldurduğu blokları tura koyar; exchangeId yine boş (canlı-tur sözleşmesi).
describe('v0.10.1444 — tur blokları arşivi', () => {
  const blk = { id: 'b1', type: 'charts_signals' as const, seq: 1, final: true, payload: { service: 'orders-api' } };

  it('persistMessages — asistan turu exchangeId taşır, blocks taşımaz; turnId (önbellek isabeti) önce gelir', () => {
    expect(persistMessages([
      t({ role: 'user', text: 'q', exchangeId: 'u1' }),
      t({ role: 'assistant', text: 'a', exchangeId: 'x1', blocks: [blk] }),
      t({ role: 'assistant', text: 'b', exchangeId: 'stored', turnId: 'x2' }),
      t({ role: 'assistant', text: 'c', archiveExchangeId: 'x3', blocks: [blk], blocksAt: 5 }),
    ])).toEqual([
      { role: 'user', text: 'q' },
      { role: 'assistant', text: 'a', exchangeId: 'x1' },
      { role: 'assistant', text: 'b', exchangeId: 'x2' },
      { role: 'assistant', text: 'c', exchangeId: 'x3' },
    ]);
    expect(turnArchiveId(t({ role: 'assistant' }))).toBeUndefined();
  });

  it('restoreTurns — blocks/archiveExchangeId/blocksAt/blocksDropped taşır, exchangeId boş', () => {
    const [u, a, old] = restoreTurns([
      { role: 'user', text: 'q' },
      { role: 'assistant', text: 'a', exchangeId: 'x1', blocks: [blk], blocksAt: 7, blocksDropped: ['table'] },
      { role: 'assistant', text: 'eski' },
    ]);
    expect(u).toEqual({ role: 'user', text: 'q' });
    expect(a).toEqual({ role: 'assistant', text: 'a', archiveExchangeId: 'x1', blocks: [blk], blocksAt: 7, blocksDropped: ['table'] });
    expect(a.exchangeId).toBeUndefined();
    expect(old).toEqual({ role: 'assistant', text: 'eski' });
    // Geri yüklenen tur yeniden kaydedilince kimliği kaybolmaz.
    expect(persistMessages([a])).toEqual([{ role: 'assistant', text: 'a', exchangeId: 'x1' }]);
  });
});
