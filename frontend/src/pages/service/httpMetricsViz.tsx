// httpMetricsViz.tsx — v0.10.1247 (operatör: HTTP Metrics "Grafana ile birebir").
// Grafana panel karşılıkları, bağımlılıksız SVG (uPlot zaman serisi için
// kalır; pasta/gauge uPlot'ta yok, ağır kütüphane eklenmez):
//   stat  (colorMode background + graphMode area) → HttpStat
//   gauge (SLI)                                    → HttpGauge
//   piechart (donut + tablo lejant: değer, %)      → HttpDonut
// Renkler tema token'larından (globals.css); sabit hex yok.
import { Sparkline } from '@/components/Sparkline';
import { seriesPalette, inkOn } from '@/lib/chartFmt';
import type { DonutSlice, HttpTone } from '@/lib/httpMetrics';

const TONE_VAR: Record<'ok' | 'warn' | 'err', string> = { ok: 'var(--ok)', warn: 'var(--warn)', err: 'var(--err)' };
const TONE_TOKEN: Record<'ok' | 'warn' | 'err', string> = { ok: '--ok', warn: '--warn', err: '--err' };
/** Renkli zemin üstü yazı rengi: token'ın çözülmüş değerinden (inkOn). */
function inkFor(tone: 'ok' | 'warn' | 'err'): string {
  const c = getComputedStyle(document.documentElement).getPropertyValue(TONE_TOKEN[tone]).trim();
  return c ? inkOn(c) : 'var(--text)';
}

export type StatTone = HttpTone | 'ok';

/**
 * Grafana stat paneli. `tone` verilirse arka plan eşik rengi (yeşil / sarı /
 * kırmızı, panodaki colorMode=background); verilmezse değer `accent` renginde
 * (Toplam İstek mavi, Throughput mor). Altta alan grafiği (graphMode=area).
 */
export function HttpStat({ label, value, tone, accent = 'var(--info)', spark, title }: {
  label: string; value: string; tone?: StatTone; accent?: string; spark?: (number | null)[]; title?: string;
}) {
  const bg = tone ? TONE_VAR[tone] : undefined;
  const ink = tone ? inkFor(tone) : undefined;
  return (
    <div title={title} style={{
      position: 'relative', overflow: 'hidden', borderRadius: 6, minWidth: 0, minHeight: 104,
      border: '1px solid var(--border)', padding: '8px 10px',
      background: bg ? `color-mix(in srgb, ${bg} 82%, var(--bg1))` : 'var(--bg1)',
    }}>
      <div style={{ fontSize: 12, fontWeight: 600, color: ink ?? 'var(--text2)', position: 'relative', zIndex: 1 }}>{label}</div>
      <div style={{
        fontSize: 30, fontWeight: 600, textAlign: 'center', marginTop: 8, position: 'relative', zIndex: 1,
        color: ink ?? accent, fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap',
      }}>{value}</div>
      {spark && spark.length > 1 && (
        <div style={{ position: 'absolute', left: 0, right: 0, bottom: 0, opacity: bg ? 0.45 : 0.6 }}>
          <Sparkline values={spark} width={400} height={34} color={ink ?? accent} />
        </div>
      )}
    </div>
  );
}

/** Grafana gauge (yarım yay): 0–1 oran, eşik rengi ile dolu. */
export function HttpGauge({ label, ratio, tone, text }: { label: string; ratio: number | null; tone?: StatTone; text: string }) {
  const r = 46, cx = 60, cy = 58, w = 10;
  const v = ratio === null ? 0 : Math.max(0, Math.min(1, ratio));
  const pt = (f: number) => {
    const a = Math.PI * (1 - f);
    return `${cx + r * Math.cos(a)},${cy - r * Math.sin(a)}`;
  };
  const arc = (f: number) => `M ${pt(0)} A ${r} ${r} 0 0 1 ${pt(f)}`;
  // Dış eşik halkası (Grafana showThresholdMarkers): %0–95 kırmızı, %95–99 sarı, %99+ yeşil.
  const R2 = r + 8;
  const at = (f: number, rr: number) => { const a = Math.PI * (1 - f); return `${cx + rr * Math.cos(a)},${cy - rr * Math.sin(a)}`; };
  const band = (a: number, b: number) => `M ${at(a, R2)} A ${R2} ${R2} 0 0 1 ${at(b, R2)}`;
  const color = TONE_VAR[tone ?? 'ok'];
  return (
    <div style={{ borderRadius: 6, border: '1px solid var(--border)', padding: '8px 10px', background: 'var(--bg1)', minHeight: 104, minWidth: 0 }}>
      <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--text2)' }}>{label}</div>
      <svg viewBox="-4 -6 128 72" style={{ width: '100%', maxHeight: 84, display: 'block' }} role="img" aria-label={`${label}: ${text}`}>
        <path d={arc(1)} fill="none" stroke="var(--bg2)" strokeWidth={w} strokeLinecap="round" />
        {/* Eşik bandı (panodaki showThresholdMarkers): %95 kırmızı · %99 sarı · üstü yeşil. */}
        <path d={band(0, 0.95)} fill="none" stroke="var(--err)" strokeWidth={3} />
        <path d={band(0.95, 0.99)} fill="none" stroke="var(--warn)" strokeWidth={3} />
        <path d={band(0.99, 1)} fill="none" stroke="var(--ok)" strokeWidth={3} />
        {v > 0 && <path d={arc(v)} fill="none" stroke={color} strokeWidth={w} strokeLinecap="round" />}
        <text x={cx} y={cy - 4} textAnchor="middle" fontSize={15} fontWeight={600} fill="var(--text)">{text}</text>
      </svg>
    </div>
  );
}

/** Grafana piechart (donut) + tablo lejant (değer, yüzde). */
export function HttpDonut({ slices, fmt, colorOf }: {
  slices: DonutSlice[]; fmt: (v: number) => string; colorOf?: (label: string, i: number) => string | undefined;
}) {
  const pal = seriesPalette();
  const color = (l: string, i: number) => colorOf?.(l, i) || pal[i % pal.length];
  const R = 42, C = 2 * Math.PI * R;
  let acc = 0;
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'minmax(120px, 180px) minmax(0, 1fr)', gap: 12, alignItems: 'center' }}>
      <svg viewBox="0 0 120 120" style={{ width: '100%', display: 'block' }} role="img" aria-label="Dağılım">
        <g transform="rotate(-90 60 60)">
          {slices.map((s, i) => {
            const len = (s.pct / 100) * C;
            const el = (
              <circle key={s.label} cx={60} cy={60} r={R} fill="none" stroke={color(s.label, i)} strokeWidth={18}
                strokeDasharray={`${len} ${C - len}`} strokeDashoffset={-acc}>
                <title>{`${s.label}: ${fmt(s.value)} (%${s.pct.toFixed(1)})`}</title>
              </circle>
            );
            acc += len;
            return el;
          })}
        </g>
        {slices[0] && slices[0].pct >= 8 && (
          <text x={60} y={64} textAnchor="middle" fontSize={12} fontWeight={600} fill="var(--text)">%{slices[0].pct.toFixed(0)}</text>
        )}
      </svg>
      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) auto auto', columnGap: 10, rowGap: 3, fontSize: 12, alignItems: 'center' }}>
        {slices.map((s, i) => (
          <div key={s.label} style={{ display: 'contents' }}>
            <span className="mono dt-trunc" title={s.label} style={{ display: 'flex', gap: 6, alignItems: 'center', minWidth: 0 }}>
              <span aria-hidden="true" style={{ flex: '0 0 auto', width: 10, height: 3, borderRadius: 1, background: color(s.label, i) }} />
              <span className="dt-trunc">{s.label}</span>
            </span>
            <span style={{ fontVariantNumeric: 'tabular-nums', textAlign: 'right' }}>{fmt(s.value)}</span>
            <span style={{ fontVariantNumeric: 'tabular-nums', textAlign: 'right', color: 'var(--text2)' }}>%{s.pct.toFixed(0)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
