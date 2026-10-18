// ResultRow — v0.10.1335: /search tek sonuç satırı.
//
// Satır = bir eylem (tablo standardı T8): gövde bir OptionRow — tık / Enter
// detay çekmecesini açar (?sel=). Yanındaki ↗ GERÇEK bir bağlantı (SafeHref →
// <Link>, yeni sekme çalışır); düğmenin İÇİNE konmaz (etkileşimli iç içe öğe
// geçersiz HTML). Vurgulama istemcide: highlightSegments → <mark>.
import { Badge, PriorityBadge, OptionRow } from '@/components/ui';
import { SafeHref } from '@/components/ui/SafeHref';
import { highlightSegments } from '@/lib/logFilters';
import type { SearchResult } from '@/lib/types';
import { tsCompact, tsLong } from '@/lib/utils';

export function Highlight({ text, terms }: { text: string; terms: string[] }) {
  if (!text) return null;
  return (
    <>
      {highlightSegments(text, terms).map((s, i) =>
        s.hl ? <mark key={i} className="srch-mark">{s.text}</mark> : <span key={i}>{s.text}</span>)}
    </>
  );
}

/** Satırın önem rozeti — yalnız sapma varsa (renk yalnız sapan değerde). */
export function ResultSeverity({ r }: { r: SearchResult }) {
  switch (r.type) {
    case 'problem': {
      const s = (r.severity ?? '').toLowerCase();
      if (!s) return null;
      return <Badge tone={s === 'critical' ? 'danger' : s === 'warning' ? 'warning' : 'info'}>{s}</Badge>;
    }
    case 'exception': {
      const p = r.payload?.priority || r.severity;
      return p === 'P1' || p === 'P2' || p === 'P3' ? <PriorityBadge p={p} /> : null;
    }
    case 'log': {
      const s = (r.severity ?? '').toUpperCase();
      if (!s) return null;
      const tone = /ERROR|FATAL|CRIT/.test(s) ? 'danger' : /WARN/.test(s) ? 'warning' : 'neutral';
      return <Badge tone={tone}>{s}</Badge>;
    }
    case 'trace':
      return r.payload?.hasError ? <Badge tone="danger">error</Badge> : null;
    default:
      return null;
  }
}

export function ResultRow({ r, idx, selected, terms, typeLabel, openHref, openLabel, onOpen }: {
  r: SearchResult;
  idx: number;
  selected: boolean;
  terms: string[];
  typeLabel: string;
  openHref: string | null;
  openLabel: string;
  onOpen: (r: SearchResult, idx: number) => void;
}) {
  return (
    <div className="srch-row" data-row-idx={idx}>
      <OptionRow selected={selected} className="srch-main" onClick={() => onOpen(r, idx)}>
        <Badge tone="neutral" className="srch-type">{typeLabel}</Badge>
        <span className="srch-text">
          <span className="srch-title"><Highlight text={r.title} terms={terms} /></span>
          {r.subtitle && <span className="srch-sub"><Highlight text={r.subtitle} terms={terms} /></span>}
        </span>
        <ResultSeverity r={r} />
        {r.ts ? <span className="srch-ts" title={tsLong(r.ts)}>{tsCompact(r.ts)}</span> : null}
      </OptionRow>
      <SafeHref href={openHref} className="srch-open" title={openLabel}>
        <span aria-hidden="true">↗</span><span className="sr-only">{openLabel}</span>
      </SafeHref>
    </div>
  );
}
