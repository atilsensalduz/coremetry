import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { RenderedMarkdown } from './Markdown';

// mdLinkSafety.test.tsx — v0.10.1315 (link XSS). Markdown `[x](href)` bağlantı
// kapısı artık appLinkKind (lib/safeAppPath) + mailto:/#: eski `^\/` öneki
// "/\evil.com" ve "//evil.com"u da geçiriyordu (tarayıcı protokol-göreli
// sayar → başka siteye gider). Güvensiz href düz metne düşer.

const html = (md: string) => renderToStaticMarkup(<RenderedMarkdown text={md} />);

describe('Markdown link kapısı (v0.10.1315)', () => {
  const LINKED: Array<[string, string]> = [
    ['[a](https://ci.example/b)', 'href="https://ci.example/b"'],
    ['[a](HTTP://ci.example)', 'href="HTTP://ci.example"'],
    ['[a](/problems?problem=p1)', 'href="/problems?problem=p1"'],
    ['[a](mailto:x@y.z)', 'href="mailto:x@y.z"'],
    ['[a](#sec)', 'href="#sec"'],
  ];
  for (const [md, want] of LINKED) {
    it(`${md} → bağlantı`, () => expect(html(md)).toContain(want));
  }

  const PLAIN = [
    '[a](javascript:alert(1))',
    '[a](JavaScript:alert(1))',
    '[a](data:text/html,x)',
    '[a](//evil.com)',
    '[a](/\\evil.com)',
    '[a](/%2F%2Fevil.com)',
    '[a](vbscript:x)',
  ];
  for (const md of PLAIN) {
    it(`${md} → düz metin, <a> yok`, () => {
      const out = html(md);
      expect(out).not.toContain('<a');
      expect(out).toContain('a');
    });
  }
});
