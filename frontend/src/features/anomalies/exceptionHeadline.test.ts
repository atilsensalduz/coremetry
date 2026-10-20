// v0.10.1398 — operatör: "Çok uzun bir exception geldiğinde tüm sayfayı
// kaplıyor. Halbuki zaten aşağıda aynısı yazıyor." Exception detay
// başlığı gömülü yığın çerçevelerinden önce ve HEADLINE_MAX'ta kesilir.
import { describe, expect, it } from 'vitest';
import { exceptionHeadline, HEADLINE_MAX } from './exceptionHeadline';

describe('exceptionHeadline (v0.10.1398)', () => {
  it.each([
    ['boş', '', '', false],
    ['undefined', undefined, '', false],
    ['kısa mesaj aynen', 'Connection refused', 'Connection refused', false],
    [
      'gömülü JVM çerçevesinden önce kesilir',
      'Technical error: [404 Not Found] during [GET] to [http://orders.example.test/v2] at deployment.APP.war//feign.FeignException.clientErrorStatus(FeignException.java:228) at x.Y.z(Y.java:1)',
      'Technical error: [404 Not Found] during [GET] to [http://orders.example.test/v2] …',
      true,
    ],
    [
      '.NET çerçevesi',
      'Object reference not set\n   at Orders.Api.Handler.Run(Handler.cs:12)',
      'Object reference not set …',
      true,
    ],
    ['"at" sözcüğü çerçeve değil', 'Timeout at gateway after 30s', 'Timeout at gateway after 30s', false],
  ])('%s', (_name, msg, want, cut) => {
    const h = exceptionHeadline(msg as string | undefined);
    expect(h.text).toBe(want);
    expect(h.cut).toBe(cut);
  });

  it('çerçevesiz uzun mesaj HEADLINE_MAX civarında kelime sınırında kesilir', () => {
    const msg = Array.from({ length: 200 }, (_, i) => `word${i}`).join(' ');
    const h = exceptionHeadline(msg);
    expect(h.cut).toBe(true);
    expect(h.text.endsWith(' …')).toBe(true);
    expect(h.text.length).toBeLessThanOrEqual(HEADLINE_MAX + 2);
    expect(msg.startsWith(h.text.slice(0, -2))).toBe(true);
  });
});
