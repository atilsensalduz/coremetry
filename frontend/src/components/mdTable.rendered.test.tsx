// @vitest-environment jsdom
//
// v0.10.1357 — RenderedMarkdown (CopilotExplain / ExplainBody: istisna ve
// trace açıklaması, kod incelemesi cevabı) GFM tablosu çizer.
//
// Boşluk v0.10.1346'da görüldü: sohbet tabloları düzeldi ama aynı model
// cevabı ✨ Explain panelinde `| a | b |` satırları olarak düz paragraf
// basılıyordu (RenderedMarkdown'ın tablo dalı yoktu). Sözleşme: çözücü +
// çizim sohbetle ORTAK (ai/mdTableParse.ts + ai/MdTable.tsx) — düz metinli
// bir tablo iki renderer'da BİREBİR aynı HTML; kod çitindeki tablo literal;
// ayraç gelmeden başlık düz metin; hücredeki link Markdown.tsx'in şema
// kapısından geçer; matematik normalizasyonu çözümden önce.
import { describe, it, expect, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});
vi.mock('@/components/CosreChart', () => ({ CosreChart: () => <div>[grafik]</div> }));

import { RenderedMarkdown } from './Markdown';
import { renderMessage } from './ai/ChatBubble';

const TBL = [
  'Yavaş operasyonlar:',
  '',
  '| Operasyon | p95 (ms) | Durum | Not |',
  '|:---|---:|:---:|---|',
  '| LOGIN_14300.aspx | 480 | AKTİF | ilk istek soğuk önbellekten döndü ve bağlantı havuzu doldu |',
  '| PAYMENT_AUTHORIZE_REQUEST_V2.handler | 1200 | KAPALI | **kritik** |',
  '',
  'Sonuç: veritabanı.',
].join('\n');

function dom(html: string): HTMLDivElement {
  const d = document.createElement('div');
  d.innerHTML = html;
  return d;
}
const md = (text: string, extra: Partial<Parameters<typeof RenderedMarkdown>[0]> = {}) =>
  dom(renderToStaticMarkup(<RenderedMarkdown text={text} {...extra} />));

describe('RenderedMarkdown GFM tablosu (v0.10.1357)', () => {
  it('başlık, hizalar ve gövde gerçek tabloda; çubuklar metinde görünmez', () => {
    const d = md(TBL);
    const t = d.querySelector('.table-wrap.cm-md-tw > table.cm-md-table');
    expect(t).toBeTruthy();
    const th = Array.from(t!.querySelectorAll('thead th')).map(x => x.textContent);
    expect(th).toEqual(['Operasyon', 'p95 (ms)', 'Durum', 'Not']);
    const ths = t!.querySelectorAll('thead th');
    expect(ths[1].classList.contains('num')).toBe(true);   // ---: sağa
    expect(ths[2].classList.contains('ta-c')).toBe(true);  // :---: orta
    expect(ths[0].className).toBe('');
    const rows = t!.querySelectorAll('tbody tr');
    expect(rows.length).toBe(2);
    expect(Array.from(rows[0].querySelectorAll('td')).map(x => x.textContent))
      .toEqual(['LOGIN_14300.aspx', '480', 'AKTİF', 'ilk istek soğuk önbellekten döndü ve bağlantı havuzu doldu']);
    expect(d.textContent).not.toContain('|');
    expect(d.textContent).toContain('Yavaş operasyonlar:');
    expect(d.textContent).toContain('Sonuç: veritabanı.');
  });

  it('hücre kararları sohbetle aynı: kısa değer nowrap, uzun kimlik <wbr>, işaretli hücre kalın', () => {
    const d = md(TBL);
    const tds = Array.from(d.querySelectorAll('tbody td'));
    const by = (s: string) => tds.find(td => td.textContent === s) as HTMLElement;
    expect(by('AKTİF').classList.contains('cm-td-nw')).toBe(true);
    expect(by('480').classList.contains('num')).toBe(true);
    expect(by('ilk istek soğuk önbellekten döndü ve bağlantı havuzu doldu').classList.contains('cm-td-nw')).toBe(false);
    expect(by('PAYMENT_AUTHORIZE_REQUEST_V2.handler').innerHTML).toContain('<wbr>');
    expect(by('kritik').querySelector('b')).toBeTruthy();
  });

  it('düz metinli tablo iki renderer\'da BİREBİR aynı HTML (ChatBubble renderMessage)', () => {
    const plain = ['| Servis | p95 | Statü |', '|---|---:|---|', '| checkout-service_long.name | 480 | ok |', ''].join('\n');
    const a = md(plain).querySelector('.table-wrap')!.outerHTML;
    const b = dom(renderToStaticMarkup(<>{renderMessage(plain)}</>)).querySelector('.table-wrap')!.outerHTML;
    expect(a).toBe(b);
  });

  it('kod çiti içindeki tablo ÇİZİLMEZ — literal kalır', () => {
    const d = md(['```', '| a | b |', '|---|---|', '| 1 | 2 |', '```'].join('\n'));
    expect(d.querySelector('table')).toBeNull();
    expect(d.querySelector('.cm-md-code pre')!.textContent).toContain('| a | b |');
    expect(d.querySelector('.cm-md-code pre')!.textContent).toContain('|---|---|');
  });

  it('akış: ayraç gelmeden başlık düz metin; ayraç tamamlanınca tablo; yarım gövde satırı gizli', () => {
    const steps = ['| a | b |', '| a | b |\n|--', '| a | b |\n|---|---|', '| a | b |\n|---|---|\n', '| a | b |\n|---|---|\n| 1 | 2', '| a | b |\n|---|---|\n| 1 | 2 |\n'];
    const out = steps.map(s => md(s, { streaming: true }));
    expect(out[0].querySelector('table')).toBeNull();
    expect(out[0].textContent).toContain('| a | b |');
    expect(out[1].querySelector('table')).toBeNull();
    expect(out[2].querySelector('table')).toBeNull(); // ayraç satırı hâlâ yarım (\n yok)
    expect(out[3].querySelector('table')).toBeTruthy();
    expect(out[4].querySelector('table')).toBeTruthy();
    expect(out[4].querySelectorAll('tbody tr').length).toBe(0);
    expect(out[4].textContent).not.toContain('| 1 | 2');
    expect(out[5].querySelectorAll('tbody tr').length).toBe(1);
    // Akış bitti: ayraçlı tablo `\n` beklemeden çizilir.
    expect(md('| a | b |\n|---|---|').querySelector('table')).toBeTruthy();
  });

  it('hücredeki link Markdown.tsx şema kapısından geçer', () => {
    const d = md(['| Ad | Link |', '|---|---|', '| a | [ok](/problems?problem=p1) |', '| b | [kötü](javascript:alert(1)) |', '| c | [kaçak](//evil.com) |'].join('\n'));
    const as = Array.from(d.querySelectorAll('table a'));
    expect(as.map(a => a.getAttribute('href'))).toEqual(['/problems?problem=p1']);
    expect(d.innerHTML).not.toContain('javascript:');
    expect(d.innerHTML).not.toContain('evil.com"');
    expect(d.textContent).toContain('kötü');
  });

  it('sunucu kimliği geçen hücre kimlik linkini korur', () => {
    const d = md(['| Trace | ms |', '|---|---|', '| abc123def456 | 5 |'].join('\n'),
      { idLinks: [{ id: 'abc123def456', href: '/trace/abc123def456', label: 'trace' }] });
    expect(d.querySelector('tbody a')?.getAttribute('href')).toBe('/trace/abc123def456');
  });

  it('matematik normalizasyonu çözümden önce: hücredeki $\\rightarrow$ → →', () => {
    const d = md(['| Akış | n |', '|---|---|', '| ACT $\\rightarrow$ RCV | 3 |'].join('\n'));
    expect(d.querySelector('tbody td')!.textContent).toBe('ACT → RCV');
  });

  it('çubukla başlamayan satır ya da sayısı tutmayan ayraç tablo başlatmaz', () => {
    expect(md('hızlı | yavaş\n---|---').querySelector('table')).toBeNull();
    expect(md('| önemli | not |\n---').querySelector('table')).toBeNull();
  });
});
