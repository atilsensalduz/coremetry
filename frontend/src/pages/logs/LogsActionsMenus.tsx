// LogsActionsMenus — v0.10.1498 (/logs Kibana-Discover yeniden tasarımı,
// dilim 1; operatör onaylı taslak 2026-10-24).
//
// Sorgu çubuğunun sağ kümesindeki üç açılır yüzey. Eskiden bunların her biri
// çubukta ya da üstündeki satırda ayrı bir düğmeydi (Trace ID kutusu, ◆ With
// trace, Takımım, Copy link, ↗ Discover in Elastic, Reset, ✨ Desenleri
// anlat, CSV/NDJSON) ve çubuk 1280px'te üç satıra sarıyordu. Hepsi TEK
// TIKLA ulaşılır kaldı (capabilityReach.test.tsx çiviliyor):
//   • + Filter ▾  (dialog — içinde girdi var): Trace ID, ◆ With trace,
//                 Takımım, + alan süzgeci (LogPillEditor, yeni pill).
//   • Share ▾     (menu): Copy link · ↗ Discover in Elastic (desen/takım
//                 etkinken çizilmez — aynı dilim sözü tutmaz, bugünkü kural).
//   • ⋯           (menu): Reset · ✨ Desenleri anlat · Export CSV / NDJSON.
// Saf sunum: durum ve eylemler Logs.tsx'ten prop olarak gelir.
import { useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { MenuItem } from '@/components/ui/Menu';
import { Popover, PopoverHrefItem } from '@/components/ui/Popover';
import { IconSparkles } from '@/components/icons';
import { LogPillEditor } from '@/components/LogPillEditor';
import type { LogFilter } from '@/lib/logFilters';
import { copyToClipboard } from '@/lib/clipboard';
import { absoluteShareHref } from '@/lib/shareUrl';
import { toast } from '@/lib/toast';
import { useT } from '@/lib/i18n';

// Share affordance — copies a link to the CURRENT filtered logs view
// (filters live in the URL; recipients sign in to Coremetry). v0.8.102:
// open to every role. v0.9.1280: the relative window is pinned to absolute
// times on the way to the clipboard (lib/shareUrl.ts) — same as ShareButton.
const LOG_SHARE_TITLE =
  'Copy a shareable link to this filtered logs view (filters are '
  + 'encoded in the URL; recipients sign in to Coremetry to open it)';

const NEW_PILL: LogFilter = { key: '', value: '', negated: false, disabled: false };

export interface LogsAddFilterMenuProps {
  traceIdDraft: string;
  onTraceIdDraft: (v: string) => void;
  onApply: () => void;
  hasTrace: boolean;
  onToggleHasTrace: () => void;
  /** Kullanıcının çözülmüş takımı ('' = yok / özellik kapalı). */
  myTeamName: string;
  /** Takım süzgeci zaten etkin mi (etkinse Takımım önerilmez). */
  teamActive: boolean;
  onApplyMyTeam: () => void;
  onAddPill: (f: LogFilter) => void;
  since?: string;
}

export function LogsAddFilterMenu(p: LogsAddFilterMenuProps) {
  const tr = useT();
  const anchorRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const [adding, setAdding] = useState(false);
  return (
    <span className="ld-addf">
      <Button ref={anchorRef} variant="secondary" aria-haspopup="dialog" aria-expanded={open}
        title={tr('logs.dv.addFilterTitle')}
        onClick={() => { setAdding(false); setOpen(o => !o); }}>
        {tr('logs.dv.addFilter')} ▾
      </Button>
      <Popover anchorRef={anchorRef} open={open} onClose={() => setOpen(false)} kind="dialog"
        ariaLabel={tr('logs.dv.addFilterTitle')} width={300}>
        {/* Trace ID filter — operators paste a trace ID from a problem /
            incident and see only its log lines. Mirrors the ?traceId= URL
            param. Exact term match on trace.id (ES) / trace_id (CH). Trimmed
            + lowercased so `0xABC…` or whitespace padding still works. */}
        <label className="ld-addf-label">
          <span>{tr('logs.dv.traceId')}</span>
          <input
            placeholder="Trace ID"
            aria-label="Filter logs by trace ID"
            value={p.traceIdDraft}
            onChange={e => p.onTraceIdDraft(e.target.value.trim().toLowerCase().replace(/^0x/, ''))}
            onKeyDown={e => { if (e.key === 'Enter') { p.onApply(); setOpen(false); } }}
            title="Filter logs to a single trace. An absolute (custom) range is honoured; with a relative range the server derives the trace's own window (falls back to full retention)."
            className="mono ld-addf-trace" />
        </label>
        <div className="ld-addf-row">
          {/* v0.8.406 — "sadece trace'i olan loglar"; oto-uygular (K3: durum = active tint). */}
          <Button variant="secondary" size="sm" active={p.hasTrace}
            onClick={p.onToggleHasTrace}
            title="Show only logs correlated with a trace — every row can pivot to its trace">
            ◆ With trace
          </Button>
          {!!p.myTeamName && !p.teamActive && (
            <Button variant="secondary" size="sm" title={tr('logs.team.applyTitle')}
              onClick={() => { p.onApplyMyTeam(); setOpen(false); }}>
              {tr('logs.team.mine')}: {p.myTeamName}
            </Button>
          )}
        </div>
        <Button variant="ghost" size="sm" onClick={() => { setOpen(false); setAdding(true); }}>
          {tr('logs.dv.fieldFilter')}
        </Button>
      </Popover>
      {adding && (
        <LogPillEditor filter={NEW_PILL} since={p.since}
          onApply={f => { setAdding(false); p.onAddPill(f); }}
          onCancel={() => setAdding(false)} />
      )}
    </span>
  );
}

export interface LogsShareMenuProps {
  /** buildKibanaURL sonucu; null = bağlantı yok (yapılandırılmamış / desen / takım). */
  kibanaHref: string | null;
}

export function LogsShareMenu({ kibanaHref }: LogsShareMenuProps) {
  const tr = useT();
  const anchorRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const copy = async () => {
    setOpen(false);
    if (await copyToClipboard(absoluteShareHref(window.location.href, Date.now()))) {
      toast.success(tr('logs.dv.linkCopied'));
    }
  };
  return (
    <>
      <Button ref={anchorRef} variant="secondary" aria-haspopup="menu" aria-expanded={open}
        onClick={() => setOpen(o => !o)}>
        {tr('logs.dv.share')} ▾
      </Button>
      <Popover anchorRef={anchorRef} open={open} onClose={() => setOpen(false)} kind="menu"
        ariaLabel={tr('logs.dv.share')} width={240}>
        <MenuItem onClick={() => void copy()} title={LOG_SHARE_TITLE}>{tr('logs.dv.copyLink')}</MenuItem>
        {/* v0.9.574 — Kibana derin linki: servis / trace-id / arama → KQL,
            pencere → zaman aralığı. Settings → Kibana link boşsa, desen ya da
            takım süzgeci etkinse (KQL'e çevrilemez) çizilmez. */}
        {kibanaHref && (
          <PopoverHrefItem href={kibanaHref} onSelect={() => setOpen(false)}
            title="Open the current filter slice in Elastic Discover">
            ↗ Discover in Elastic
          </PopoverHrefItem>
        )}
      </Popover>
    </>
  );
}

export interface LogsMoreMenuProps {
  onReset: () => void;
  onExplainPatterns: () => void;
  explainBusy: boolean;
  /** Sayfada görünen (yüklenmiş + yerel daraltılmış) satır sayısı. */
  exportCount: number;
  onExport: (kind: 'csv' | 'ndjson') => void;
}

export function LogsMoreMenu(p: LogsMoreMenuProps) {
  const tr = useT();
  const anchorRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const pick = (fn: () => void) => () => { setOpen(false); fn(); };
  const n = p.exportCount.toLocaleString();
  // v0.9.302 — export what is ALREADY loaded: zero backend calls, the file
  // matches the screen (local narrow filter included). A synchronous
  // unbounded export is deliberately not offered.
  const exportTitle = p.exportCount > 0
    ? `Downloads the ${n} rows currently in the page — not the whole query. Load more first if you need more, or narrow the query.`
    : tr('logs.dv.exportNone');
  return (
    <>
      <Button ref={anchorRef} variant="secondary" aria-haspopup="menu" aria-expanded={open}
        aria-label={tr('logs.dv.more')} title={tr('logs.dv.more')}
        onClick={() => setOpen(o => !o)}>
        ⋯
      </Button>
      <Popover anchorRef={anchorRef} open={open} onClose={() => setOpen(false)} kind="menu"
        ariaLabel={tr('logs.dv.more')} width={260}>
        <MenuItem onClick={pick(p.onReset)} title="Clear every filter and the search box">Reset</MenuItem>
        {/* v0.9.1100 (F3.5) — ✨ desen anlatımı. Fetch YALNIZ tıkla (ES-maliyet disiplini). */}
        <MenuItem icon={<IconSparkles />} disabled={p.explainBusy} onClick={pick(p.onExplainPatterns)}
          title="Seçili süzgeç ve penceredeki log desenlerini AI anlatır; filo geneli yeni/patlayan desenler ve sürekli gürültü şablonları ayrıca etiketlenir">
          Desenleri anlat
        </MenuItem>
        <div className="ld-menu-sep" role="separator" />
        <MenuItem disabled={p.exportCount === 0} title={exportTitle} onClick={pick(() => p.onExport('csv'))}>
          Export CSV ({n})
        </MenuItem>
        <MenuItem disabled={p.exportCount === 0}
          title={p.exportCount > 0 ? 'One JSON object per line — unambiguous for bodies containing commas, quotes or newlines, and what log pipelines ingest.' : exportTitle}
          onClick={pick(() => p.onExport('ndjson'))}>
          Export NDJSON ({n})
        </MenuItem>
      </Popover>
    </>
  );
}
