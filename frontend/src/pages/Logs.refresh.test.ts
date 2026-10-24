import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.440 (log arama denetimi B3) — sayfa-yerel ↻ + tazelik etiketi:
// sayaç memo bağımlılığında (pencere gerçekten hareket eder), tık cursor'ı
// sıfırlar (v0.7.81), canlı kuyrukta çizilmez, özel aralıkta yalnız
// refetch, otomatik aralık YOK, etiket pencere sonundan (fetch anı değil).
describe('Logs ↻ refresh (B3)', () => {
  const src = readFileSync(resolve(__dirname, 'Logs.tsx'), 'utf8');
  it('nowTick drives the window memo', () => {
    expect(src).toContain('[useTimeRange, range, nowTick]');
    expect(src).toContain('const [nowTick, setNowTick] = useState(0);');
  });
  it('the handler resets paging, refetches on custom, ticks on presets, hidden while live', () => {
    // v0.10.1498 — ↻ ile Search tek düğme (pages/logs/LogsQueryBar.tsx):
    // taslak = uygulanan VE canlı değilken ↻ kipi; işleyici Logs.tsx `refresh`.
    const i = src.indexOf('const refresh = () => {');
    expect(i).toBeGreaterThan(0);
    const block = src.slice(i, i + 200);
    expect(block).toContain('resetPaging();');
    expect(block).toContain("range.preset === 'custom'");
    expect(block).toContain('staticQ.refetch()');
    expect(block).toContain('setNowTick(t => t + 1)');
    expect(src).toContain('onRefresh={refresh} dirty={draftDirty} to={to}');
    expect(src).toContain('const draftDirty = isDraftDirty(draft, filter);');
    const bar = readFileSync(resolve(__dirname, 'logs/LogsQueryBar.tsx'), 'utf8');
    expect(bar).toContain('const refreshMode = !p.dirty && !p.live;');
    const j = bar.indexOf('aria-label="Yenile"');
    expect(j).toBeGreaterThan(0);
    const btn = bar.slice(j - 120, j + 400);
    expect(btn).toContain('{refreshMode ? (');
    expect(btn).toContain('onClick={p.onRefresh}');
    expect(btn).toContain('fmtClock(p.to / 1e6)');
  });
  it('Search always re-queries (v0.10.447): apply ticks on presets, refetches on custom', () => {
    const i = src.indexOf('const apply = () => {');
    expect(i).toBeGreaterThan(0);
    const block = src.slice(i, i + 400);
    expect(block).toContain('setNowTick(t => t + 1)');
    expect(block).toContain("range.preset === 'custom'");
    expect(block).toContain('staticQ.refetch()');
  });
  it('never adds an automatic interval around the tick', () => {
    expect(src).not.toMatch(/setInterval\([^)]*setNowTick/);
  });
});
