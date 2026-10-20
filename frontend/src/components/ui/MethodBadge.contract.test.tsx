// @vitest-environment jsdom
//
// MethodBadge.contract.test.tsx — v0.10.1425 (HTTP method rozeti: method'a
// göre renk).
//
// Üç sözleşme:
//   1. EŞLEME TABLOSU — GET mavi, POST yeşil, PUT/PATCH amber, DELETE
//      kırmızı, geri kalanı (HEAD/OPTIONS/CONNECT/TRACE/_OTHER/boş/
//      bilinmeyen) nötr. Küçük harf ve boşluk normalize edilir.
//   2. DOM — `.badge.mono.m-<ton>` + `data-method="GET"`; metin büyük harf;
//      `className` geçer.
//   3. KAYNAK PİNİ — atom DIŞINDA method rozeti çizen sayfa yok. Dört ayrı
//      yazımı (`<Badge>{x.method}</Badge>`, `badge b-*` span'ı, …) tek
//      tek eşleyen kapı yerine "method değerini .badge/Badge içine basan"
//      JSX aranır; istisna listesi gerekçeli ve BAYAT OLAMAZ (girdi
//      kullanılmayınca kırmızı: silinsin).
//
// KAPSAM DÜRÜSTÇE: DOM sözleşmesi test ediliyor, GÖRÜNÜM değil; sınıfların
// CSS karşılığı `primitiveClasses.test.ts` (harita) ve
// `styles/methodBadge.pin.test.ts` (token çiftleri, üç temada AA).

import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { MethodBadge, methodTone, normalizeMethod, type MethodTone } from './MethodBadge';

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function render(method: string, className?: string): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MethodBadge method={method} className={className} />); });
  return host.firstElementChild as HTMLElement;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

describe('MethodBadge — eşleme tablosu (v0.10.1425)', () => {
  const TABLE: Array<[string, MethodTone]> = [
    ['GET', 'get'],
    ['POST', 'post'],
    ['PUT', 'put'],
    ['PATCH', 'patch'],
    ['DELETE', 'delete'],
    ['HEAD', 'other'],
    ['OPTIONS', 'other'],
    ['CONNECT', 'other'],
    ['TRACE', 'other'],
    ['_OTHER', 'other'],
    ['', 'other'],
    ['FOO', 'other'],
    ['get', 'get'],
    [' delete ', 'delete'],
  ];
  for (const [m, tone] of TABLE) {
    it(`${JSON.stringify(m)} → ${tone}`, () => {
      expect(methodTone(m)).toBe(tone);
    });
  }

  it('null/undefined → other (nötr); normalize büyük harf + kırpma', () => {
    expect(methodTone(null)).toBe('other');
    expect(methodTone(undefined)).toBe('other');
    expect(normalizeMethod(' post ')).toBe('POST');
    expect(normalizeMethod(undefined)).toBe('');
  });
});

describe('MethodBadge — DOM', () => {
  it('sınıf: badge + mono + m-<ton>; data-method ve metin büyük harf', () => {
    const el = render('get');
    expect(el.tagName).toBe('SPAN');
    expect(el.classList.contains('badge')).toBe(true);
    expect(el.classList.contains('mono')).toBe(true);
    expect(el.classList.contains('m-get')).toBe(true);
    expect(el.getAttribute('data-method')).toBe('GET');
    expect(el.textContent).toBe('GET');
  });

  it('her ton kendi sınıfını basar, bilinmeyen nötr', () => {
    for (const [m, cls] of [['POST', 'm-post'], ['PUT', 'm-put'], ['PATCH', 'm-patch'], ['DELETE', 'm-delete'], ['OPTIONS', 'm-other'], ['_OTHER', 'm-other']] as const) {
      const el = render(m);
      expect(el.classList.contains(cls), m).toBe(true);
      // Tek ton: başka bir m-* sınıfı YOK (ör. m-get artığı).
      expect([...el.classList].filter(c => c.startsWith('m-'))).toEqual([cls]);
      act(() => { root?.unmount(); }); host?.remove();
    }
  });

  it('className geçer, b-* ton sınıfı basmaz (renk yalnız m-*)', () => {
    const el = render('DELETE', 'x-extra');
    expect(el.classList.contains('x-extra')).toBe(true);
    expect([...el.classList].some(c => c.startsWith('b-'))).toBe(false);
  });
});

// ── KAYNAK PİNİ ────────────────────────────────────────────────────────────
const SRC = resolve(__dirname, '../..');
const HOME = 'components/ui/MethodBadge.tsx';

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !p.includes('.test.')) out.push(p);
  }
  return out;
}
const stripComments = (s: string) =>
  s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\{\/\*[\s\S]*?\*\/\}/g, '').split('\n').map(l => l.replace(/^\s*\/\/.*$/, '')).join('\n');

// Method değerini doğrudan bir rozetin içine basan JSX: `<Badge …>{…method…}</Badge>`
// ya da `<span className="badge …">{…method…}</span>`. `method` burada bir
// ÖZELLİK erişimi (`r.method`, `refObj.method`, `r['http_request_method']`)
// ya da `http_request_method` anahtarı.
const BADGE_WITH_METHOD = /<(Badge|span)\b[^>]*?(className=["'{][^>]*\bbadge\b|tone=)[^>]*>\s*\{[^}]*(\.method\b|http_request_method|http\.method)[^}]*\}\s*<\/\1>/g;

/**
 * Gerekçeli istisnalar — her girdi hâlâ EŞLEŞMEK zorunda (bayat girdi
 * kırmızı). Yeni satır = bilinçli karar.
 */
const EXEMPT = new Map<string, string>([
  ['pages/Endpoints.tsx',
    'v0.10.1425 dalgasında dosya paralel ajanın (v0.10.1424 fmtNum göçü) — düzenleme kilidi. '
    + 'Göç tek satır: `<Badge tone="neutral" className="mono">{r.method}</Badge>` → `<MethodBadge method={r.method} />` '
    + '(import `MethodBadge` from \'@/components/ui\'; `Badge` importu düşer) + bu girdi silinir.'],
]);

describe('MethodBadge — atom dışında method rozeti yok (kaynak pini)', () => {
  it('method değeri .badge/Badge içine yalnız atomdan basılır', () => {
    const offenders: string[] = [];
    const matchedExempt = new Set<string>();
    for (const f of walk(SRC)) {
      const rel = f.slice(SRC.length + 1);
      if (rel === HOME) continue;
      const src = stripComments(readFileSync(f, 'utf8'));
      for (const m of src.matchAll(BADGE_WITH_METHOD)) {
        if (EXEMPT.has(rel)) { matchedExempt.add(rel); continue; }
        offenders.push(`${rel}:${src.slice(0, m.index).split('\n').length} — ${m[0].slice(0, 80)}`);
      }
    }
    expect(offenders, `Method rozeti atom dışında çiziliyor — ui/MethodBadge kullan:\n${offenders.join('\n')}`).toEqual([]);
    for (const rel of EXEMPT.keys()) {
      expect(matchedExempt.has(rel), `bayat istisna: ${rel} artık elle rozet basmıyor — EXEMPT'ten çıkar`).toBe(true);
    }
  });

  it('göç eden siteler atomu okur', () => {
    for (const rel of ['pages/EndpointDetail.tsx', 'pages/service/ServiceHttpMetricsTab.tsx']) {
      const src = readFileSync(resolve(SRC, rel), 'utf8');
      expect(src, rel).toMatch(/<MethodBadge\b/);
      expect(src, `${rel} MethodBadge'i barrel'dan almalı`).toMatch(/import \{[^}]*\bMethodBadge\b[^}]*\} from '@\/components\/ui'/);
    }
  });

  it('atom barrel\'da', () => {
    const idx = readFileSync(resolve(SRC, 'components/ui/index.ts'), 'utf8');
    expect(idx).toMatch(/export \{ MethodBadge, methodTone, normalizeMethod \} from '\.\/MethodBadge'/);
  });

  it('pin dedektörü kendi düzyazısını doğrulamıyor (pozitif/negatif örnek)', () => {
    const bad1 = `<Badge tone="neutral" className="mono">{r.method}</Badge>`;
    const bad2 = `<span className="badge b-info" style={{ fontSize: 10 }}>{row?.method || refObj.method}</span>`;
    const ok1 = `<span className="dt-trunc">{String(r[k] ?? '') || '—'}</span>`;
    const ok2 = `<MethodBadge method={r.method} />`;
    expect([...bad1.matchAll(BADGE_WITH_METHOD)].length).toBe(1);
    expect([...bad2.matchAll(BADGE_WITH_METHOD)].length).toBe(1);
    expect([...ok1.matchAll(BADGE_WITH_METHOD)].length).toBe(0);
    expect([...ok2.matchAll(BADGE_WITH_METHOD)].length).toBe(0);
  });
});
