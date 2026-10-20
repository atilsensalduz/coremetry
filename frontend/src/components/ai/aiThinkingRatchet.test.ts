// aiThinkingRatchet — v0.10.1384: AI "düşünüyor" hâlinin TEK dili AiThinking.
//
// Operatör: "CoSRE düşünüyor derken ve explain trace'teki dönme efektleri
// aynı değil." Kök sebep: her yüzey bekleyişi kendisi çiziyordu — sohbet
// balonu `.cm-thinking` parıltısı, Explain çekmecesi dönen OTel halkası
// (LoaderMark), deploy satırı "✨ Thinking…", insight kartı genel Spinner,
// AI özet gövdeleri Spinner + "model yazıyor…". tsc/eslint için hepsi
// kusursuz; ayrışmayı yalnız bu tarama yakalar. Tavanlar 0:
//   (a) `cm-thinking` / `ai-thinking` sınıfı AiThinking.tsx dışında elle yazılmaz,
//   (b) AI bekleme metni ("Düşünüyor…", "Thinking…", "model yazıyor", "kodu
//       okuyor") yalnız bir <AiThinking …> etiketinin İÇİNDE (label/hint) geçer,
//   (c) <Spinner>/<LoaderMark> bir AI bekleyişini (düşünüyor / kodu okuyor) taşımaz —
//       LoaderMark AI DIŞI yüklemeler için kalır (PageLoader); "CoSRE yükleniyor…"
//       gibi yapılandırma yüklemesi düşünme değil, serbest,
//   (d) göçen yüzeyler AiThinking'de kalır (biri elle yazılmış göstergeye
//       dönerse adıyla düşer).
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { jsxOpenTags } from '@/styles/jsxTags';

const SRC = resolve(__dirname, '..', '..');
const TEST_MARK = '.' + 'test' + '.';
const HOME = 'components/ai/AiThinking.tsx';

// buttonStateRatchet / zLayers ile aynı durum makinesi (içe aktarmak o
// dosyaların describe'larını da koştururdu): yorumlar boşluğa döner, satırlar
// korunur, dizgi içi `//` yorum sanılmaz.
function stripTsComments(src: string): string {
  let out = '';
  let mode: 'code' | 'line' | 'block' | '"' | "'" | '`' = 'code';
  for (let i = 0; i < src.length; i++) {
    const c = src[i];
    const n = src[i + 1] ?? '';
    if (mode === 'code') {
      if (c === '/' && n === '/') { mode = 'line'; out += '  '; i++; continue; }
      if (c === '/' && n === '*') { mode = 'block'; out += '  '; i++; continue; }
      if (c === '"' || c === "'" || c === '`') { mode = c; out += c; continue; }
      out += c;
    } else if (mode === 'line') {
      if (c === '\n') { mode = 'code'; out += '\n'; } else out += ' ';
    } else if (mode === 'block') {
      if (c === '*' && n === '/') { mode = 'code'; out += '  '; i++; continue; }
      out += c === '\n' ? '\n' : ' ';
    } else {
      if (c === '\\') { out += '  '; i++; continue; }
      if (c === mode) mode = 'code';
      out += c;
    }
  }
  return out;
}

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (e.name === 'node_modules') continue;
    const p = join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (e.name.endsWith('.tsx') && !e.name.includes(TEST_MARK)) out.push(p);
  }
  return out;
}

const FILES = walk(SRC).map(p => ({ rel: p.slice(SRC.length + 1), src: stripTsComments(readFileSync(p, 'utf8')) }));
const THINK = /Thinking…|Thinking\.\.\.|[Dd]üşünüyor|model yazıyor|kodu okuyor/g;
const lineOf = (src: string, idx: number) => src.slice(0, idx).split('\n').length;

// Bir dosyadaki <AiThinking …> açılış etiketlerini boşlukla örter (satırlar korunur):
// içlerindeki etiket/ipucu metni meşru.
function maskAiThinkingTags(src: string): string {
  let out = src;
  for (const t of jsxOpenTags(src, 'AiThinking')) {
    const at = out.indexOf(t.tag);
    if (at < 0) continue;
    out = out.slice(0, at) + t.tag.replace(/[^\n]/g, ' ') + out.slice(at + t.tag.length);
  }
  return out;
}

describe('AI düşünüyor göstergesi — tek bileşen (v0.10.1384)', () => {
  it('(a) cm-thinking / ai-thinking sınıfı AiThinking.tsx dışında elle yazılmaz', () => {
    const hits = FILES.filter(f => f.rel !== HOME)
      .flatMap(f => [...f.src.matchAll(/\b(cm-thinking|ai-thinking)\b/g)].map(m => `${f.rel}:${lineOf(f.src, m.index ?? 0)}`));
    expect(hits, 'AiThinking bileşenini kullan').toEqual([]);
  });

  it('(b) AI bekleme metni yalnız <AiThinking> etiketinin içinde', () => {
    const hits = FILES.filter(f => f.rel !== HOME).flatMap(f => {
      const masked = maskAiThinkingTags(f.src);
      return [...masked.matchAll(THINK)].map(m => `${f.rel}:${lineOf(masked, m.index ?? 0)} "${m[0]}"`);
    });
    expect(hits, 'elle yazılmış "düşünüyor" metni — <AiThinking label=…> kullan').toEqual([]);
  });

  it('(c) Spinner / LoaderMark bir AI bekleyişi taşımaz', () => {
    const hits = FILES.flatMap(f => ['Spinner', 'LoaderMark'].flatMap(tag =>
      jsxOpenTags(f.src, tag).filter(t => /[Dd]üşün|Thinking|model yazıyor|kodu okuyor/.test(t.tag))
        .map(t => `${f.rel}:${t.line} <${tag}>`)));
    expect(hits).toEqual([]);
    // LoaderMark AI alanında hiç çizilmez.
    const lm = FILES.filter(f => f.rel.startsWith('components/ai/'))
      .filter(f => jsxOpenTags(f.src, 'LoaderMark').length > 0).map(f => f.rel);
    expect(lm).toEqual([]);
  });

  it('(d) göçen yüzeyler AiThinking kullanır', () => {
    const MIGRATED = [
      'components/ai/ChatBubble.tsx',
      'components/DeployHistoryPanel.tsx',
      'components/ai/InsightCard.tsx',
      // v0.10.1407 — CopilotExplain / ServiceChartsExplainBody / LogSpikeExplainBody
      // özne kipiyle silindi; charts / log-spike bekleyişi ChatBubble'ınki.
    ];
    for (const rel of MIGRATED) {
      const f = FILES.find(x => x.rel === rel);
      expect(f, rel).toBeTruthy();
      expect(jsxOpenTags(f!.src, 'AiThinking').length, rel).toBeGreaterThan(0);
    }
  });

  it('kendi kendini sınar: elle yazılmış bir gösterge yakalanır', () => {
    const bad = stripTsComments(`const x = <span className="cm-thinking">Düşünüyor…</span>; const y = <Spinner label="CoSRE düşünüyor…" />;
      const ok = <AiThinking label="CoSRE düşünüyor…" />; // Düşünüyor yorumda serbest`);
    expect([...maskAiThinkingTags(bad).matchAll(THINK)].length).toBe(2);
    expect(jsxOpenTags(bad, 'Spinner').filter(t => /[Dd]üşün/.test(t.tag)).length).toBe(1);
    expect(/\bcm-thinking\b/.test(bad)).toBe(true);
  });
});
