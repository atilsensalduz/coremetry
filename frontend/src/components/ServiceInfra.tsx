import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Sparkline } from './Sparkline';
import { api } from '@/lib/api';
import { useServiceRuntime } from '@/lib/queries';
import { fmtBytes } from '@/lib/utils';
import type { InfraMetricSeries, ServiceRuntime } from '@/lib/types';

// ServiceInfra renders curated runtime / process metrics for the
// inspected service alongside the trace-side panels on
// /service?name=…. Lets the SRE answer "p99 spiked at 14:32 — was
// the pod CPU starved?" in one glance, without leaving the page.
//
// Slots are canonical (cpu / memory / rps / runtime); the server
// picks the most-specific source per slot for the service's
// runtime (jvm.* for Java, process.runtime.* for Go, k8s.pod.*
// when available). Empty slots collapse silently.
export function ServiceInfra({ service, since = '15m' }: {
  service: string;
  since?: string;
}) {
  const [data, setData] = useState<InfraMetricSeries[] | null | undefined>(undefined);

  useEffect(() => {
    if (!service) return;
    setData(undefined);
    api.serviceInfraMetrics(service, since)
      .then(d => setData(d ?? []))
      .catch(() => setData(null));
  }, [service, since]);

  if (data === undefined || data === null || data.length === 0) return null;

  return (
    <div style={{
      background: 'var(--bg1)', border: '1px solid var(--border)',
      borderRadius: 8, padding: 14, marginBottom: 14,
    }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, marginBottom: 10, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 13, fontWeight: 600 }}>
          Infra (last {since})
        </span>
        {/* Runtime badge — language + runtime version. Sits
            inline with the panel title so the operator
            instantly knows whether they're investigating a
            JVM service vs a Go binary vs a .NET app vs a
            Node.js process. Hidden when the resource attrs
            don't have enough info (some SDKs only emit one
            of language/runtime). */}
        <RuntimeBadge service={service} />
        <span style={{ fontSize: 11, color: 'var(--text3)', flex: 1 }}>
          process / pod metrics correlated with span timeline · click a tile to drill into the metric explorer
        </span>
      </div>
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))',
        gap: 12,
      }}>
        {data.map(s => (
          <InfraTile key={s.metric} s={s} service={service} />
        ))}
      </div>
    </div>
  );
}

// RuntimeBadge renders a small "Java OpenJDK 21" / "Go 1.22"
// pill from the service's resource attributes. Layered detail:
//
//   • Language icon (text glyph) — fastest visual cue
//   • Runtime + version — the actionable detail (Java 17 vs
//     21 changes which debugger flags you reach for)
//   • Host + OS shown on hover via title attribute so the
//     panel header doesn't get crowded
//
// Falls back to nothing visible when the SDK didn't emit
// any usable metadata — the badge component returns null in
// that case rather than rendering "Unknown".
function RuntimeBadge({ service }: { service: string }) {
  const q = useServiceRuntime(service);
  const rt = q.data;
  if (!rt) return null;
  const display = formatRuntime(rt);
  if (!display) return null;
  const titleParts = [
    rt.runtimeDesc,
    rt.host && `host: ${rt.host}`,
    rt.os && `os: ${rt.os}`,
    rt.sdkVersion && `OTel SDK ${rt.sdkVersion}`,
  ].filter(Boolean) as string[];
  return (
    <span title={titleParts.join(' · ')}
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 4,
        fontSize: 11, padding: '2px 8px',
        background: 'var(--bg3)', border: '1px solid var(--border)',
        borderRadius: 12, color: 'var(--text)',
        fontFamily: 'ui-monospace, monospace',
      }}>
      <span style={{ color: languageColor(rt.language) }}>
        {languageGlyph(rt.language)}
      </span>
      <span>{display}</span>
    </span>
  );
}

// formatRuntime turns a (language, runtime name, version) trio
// into a one-line label like "Java 21" / "Go 1.22.5" / ".NET
// 8.0.4" / "Node.js 20.10.0" / "Python 3.12". Tries to use the
// most operator-recognisable name for each language rather
// than the SDK-emitted free-text.
function formatRuntime(rt: ServiceRuntime): string {
  const lang = (rt.language || '').toLowerCase();
  const name = friendlyLanguageName(lang) || rt.runtimeName || '';
  const ver = simplifyVersion(rt.runtimeVersion || '');
  if (name && ver) return `${name} ${ver}`;
  if (name)        return name;
  if (rt.runtimeName) return rt.runtimeName;
  if (rt.sdkVersion)  return `OTel ${rt.sdkVersion}`;
  return '';
}

function friendlyLanguageName(lang: string): string {
  switch (lang) {
    case 'go':       return 'Go';
    case 'java':     return 'Java';
    case 'kotlin':   return 'Kotlin';
    case 'dotnet':
    case 'csharp':   return '.NET';
    case 'nodejs':
    case 'javascript':
    case 'webjs':    return 'Node.js';
    case 'python':   return 'Python';
    case 'ruby':     return 'Ruby';
    case 'php':      return 'PHP';
    case 'rust':     return 'Rust';
    case 'erlang':   return 'Erlang';
    case 'swift':    return 'Swift';
    default:         return '';
  }
}

// simplifyVersion strips Java's "+12-LTS" suffix and Go's
// "go" prefix so the badge reads "21" / "1.22.5" rather than
// "21+12-LTS" / "go1.22.5". Keep the full string available via
// the title hover for operators who care.
function simplifyVersion(v: string): string {
  if (!v) return '';
  v = v.trim();
  if (v.startsWith('go')) return v.slice(2);
  // Java often emits "21.0.1+12-LTS" — keep "21.0.1".
  const plusIdx = v.indexOf('+');
  if (plusIdx > 0) v = v.slice(0, plusIdx);
  return v;
}

function languageGlyph(lang?: string): string {
  // Pure typographic glyphs (no emoji) — small visual hint of
  // the language family next to the text label. Distinct
  // shapes per family so a glance is enough.
  switch ((lang || '').toLowerCase()) {
    case 'go':         return '◆';
    case 'java':
    case 'kotlin':     return '◢';
    case 'dotnet':
    case 'csharp':     return '⌬';
    case 'nodejs':
    case 'javascript': return '◬';
    case 'python':     return '◇';
    case 'ruby':       return '◈';
    case 'php':        return '◐';
    case 'rust':       return '◉';
    default:           return '·';
  }
}

function languageColor(lang?: string): string {
  switch ((lang || '').toLowerCase()) {
    case 'go':       return '#00ADD8';
    case 'java':
    case 'kotlin':   return '#f89820';
    case 'dotnet':
    case 'csharp':   return '#512BD4';
    case 'nodejs':
    case 'javascript': return '#3C873A';
    case 'python':   return '#3776AB';
    case 'ruby':     return '#CC342D';
    case 'php':      return '#777BB4';
    case 'rust':     return '#CE412B';
    default:         return 'var(--text2)';
  }
}

function InfraTile({ s, service }: { s: InfraMetricSeries; service: string }) {
  const last = s.points.length > 0 ? s.points[s.points.length - 1].v : 0;
  const max  = s.points.length > 0 ? Math.max(...s.points.map(p => p.v)) : 0;
  const min  = s.points.length > 0 ? Math.min(...s.points.map(p => p.v)) : 0;
  const label = LABELS[s.metric] ?? s.metric.toUpperCase();
  // Drill-down to the metrics explorer with this exact source
  // metric pre-loaded for the same service. The explorer reads
  // ?source/?service/?metric on mount (see /explore page).
  const href = `/explore?source=metrics&service=${encodeURIComponent(service)}&metric=${encodeURIComponent(s.source)}`;
  return (
    <Link to={href} title={`Open ${s.source} in metric explorer`}
      style={{
        padding: 10, border: '1px solid var(--border)',
        borderRadius: 6, background: 'var(--bg2)',
        textDecoration: 'none', color: 'inherit',
        display: 'block', cursor: 'pointer',
        transition: 'border-color 120ms, background 120ms',
      }}
      className="infra-tile">
      <div style={{
        fontSize: 10, color: 'var(--text3)',
        textTransform: 'uppercase', letterSpacing: 0.4,
      }}>
        {label}
      </div>
      <div style={{ fontSize: 18, fontWeight: 700, marginTop: 2 }}>
        {fmtValue(last, s.metric, s.unit)}
      </div>
      <div style={{ marginTop: 4 }}>
        <Sparkline values={s.points.map(p => p.v)}
                   color={COLORS[s.metric] ?? 'var(--accent2)'}
                   title={`${s.source} · last ${s.points.length} buckets`} />
      </div>
      <div style={{
        display: 'flex', justifyContent: 'space-between',
        fontSize: 10, color: 'var(--text3)', marginTop: 2,
        fontFamily: 'ui-monospace, monospace',
      }}>
        <span>min {fmtValue(min, s.metric, s.unit)}</span>
        <span>max {fmtValue(max, s.metric, s.unit)}</span>
      </div>
      <div style={{ fontSize: 10, color: 'var(--text3)', marginTop: 2 }} title={s.source}>
        src: {s.source} ↗
      </div>
    </Link>
  );
}

const LABELS: Record<string, string> = {
  cpu:     'CPU',
  memory:  'Memory',
  rps:     'Requests',
  runtime: 'Runtime',
  heap:    'Heap',
};

const COLORS: Record<string, string> = {
  cpu:     'var(--warn)',
  memory:  'var(--accent)',
  rps:     'var(--accent2)',
  runtime: 'var(--text2)',
  heap:    'var(--err)',
};

function fmtValue(v: number, slot: string, unit: string): string {
  if (!isFinite(v)) return '—';
  // CPU often comes as 0..1 ratio — display as %.
  if (slot === 'cpu' || unit === '%') {
    if (v >= 1) return `${v.toFixed(1)}%`;
    return `${(v * 100).toFixed(1)}%`;
  }
  if (slot === 'memory' || slot === 'heap' || unit === 'bytes') {
    return fmtBytes(v);
  }
  if (slot === 'rps' || unit === '/s') {
    return v >= 1000 ? `${(v / 1000).toFixed(1)}k/s` : `${v.toFixed(1)}/s`;
  }
  // generic numeric
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k`;
  return v.toFixed(0);
}

