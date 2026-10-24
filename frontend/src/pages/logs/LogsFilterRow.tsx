// LogsFilterRow — v0.10.1498 (/logs Kibana-Discover yeniden tasarımı, dilim 1;
// operatör onaylı taslak 2026-10-24).
//
// Etkin süzgeçlerin TEK satırı. Eskiden dört ayrı tam genişlik bant +
// bir pill şeridi + "Uygulanan arama" bandıydı (trace kilidi, desen çipi,
// takım çipi, pill'ler, derlenmiş sorgu) ve dolu bir derin linkte tablo
// ekranın yarısından aşağı itiliyordu. Şimdi hepsi aynı sarılan satırda çip;
// derlenmiş sorgu sağdaki "KQL ⓘ" popover'ında (kopyalanabilir). Davranış
// aynı: her çip eylemi oto-uygular (state + sayfalama + URL Logs.tsx'te).
import { useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { LinkButton } from '@/components/ui/LinkButton';
import { Popover } from '@/components/ui/Popover';
import { CopyButton } from '@/components/CopyButton';
import { LogPillEditor } from '@/components/LogPillEditor';
import { TeamPicker } from '@/components/TeamPicker';
import type { LogFilter } from '@/lib/logFilters';
import type { LogsUrlFilter } from '@/lib/logsUrl';
import { isSameTeam } from '@/lib/logsTeam';
import { useT } from '@/lib/i18n';

export interface LogsFilterRowProps {
  filter: LogsUrlFilter;
  onClearTrace: () => void;
  onClearPattern: () => void;
  onToggleHasTrace: () => void;
  // Takım
  myTeamName: string;
  teamGate: boolean;
  teamPickerOpen: boolean;
  teamDraft: string;
  onTeamDraft: (v: string) => void;
  onToggleTeamPicker: () => void;
  onSetTeam: (team: string) => void;
  onClearTeam: () => void;
  // Pill'ler (Kibana Discover pill modeli)
  filters: LogFilter[];
  editPill: number | null;
  onEditPill: (i: number | null) => void;
  onNegatePill: (i: number) => void;
  onDisablePill: (i: number) => void;
  onRemovePill: (i: number) => void;
  onReplacePill: (i: number, next: LogFilter) => void;
  onClearPills: () => void;
  since?: string;
  /** Backend'e giden `search=` cümlesi (pill'ler + serbest metin). */
  compiledSearch: string;
  /** KQL kutusunda çıplak trace id varsa o id ('' = öneri yok). */
  traceHint: string;
  onUseTraceHint: () => void;
}

export function LogsFilterRow(p: LogsFilterRowProps) {
  const tr = useT();
  const { filter, filters, compiledSearch } = p;
  const kqlRef = useRef<HTMLButtonElement>(null);
  const [kqlOpen, setKqlOpen] = useState(false);
  const suggestTeam = !filter.team && !!p.myTeamName && !p.teamGate;
  const any = !!filter.traceId || !!filter.pattern || !!filter.team || filter.hasTrace
    || filters.length > 0 || suggestTeam || !!p.traceHint || !!compiledSearch;
  if (!any) return null;
  return (
    <div className="ld-frow" role="group" aria-label="Active filters">
      {filter.traceId && (
        <span className="trace-lock">
          <span>Filtered to trace</span>
          <code>{filter.traceId}</code>
          {filter.spanId && (<>
            <span>· span</span>
            <code>{filter.spanId}</code>
          </>)}
          <Button variant="secondary" onClick={p.onClearTrace}>✕ Clear</Button>
        </span>
      )}
      {/* v0.10.1071 — desen süzgeci ÇİP olarak, arama kutusunda metin olarak
          DEĞİL: sunucu dedektörün yüklemini uygular, kutu boş kalır, yazılan
          sorgu desenle AND'lenir. × yalnız deseni kaldırır. */}
      {filter.pattern && (
        <span className="trace-lock" data-testid="logs-pattern-chip">
          <span>desen:</span>
          <code title="Küratörlü log deseni — dedektörün eşleşme yüklemi sunucuda uygulanır">{filter.pattern}</code>
          <Button variant="ghost" size="sm" onClick={p.onClearPattern}
            aria-label={`Desen süzgecini kaldır: ${filter.pattern}`}
            title="Desen süzgecini kaldır (arama metni ve diğer süzgeçler kalır)">×</Button>
        </span>
      )}
      {/* v0.10.1477 — takım süzgeci ÇİP olarak: "Değiştir" TeamPicker'ı açar;
          × süzgeci kaldırır ve otomatik varsayılandan vazgeçmeyi hatırlar. */}
      {filter.team && (
        <span className="trace-lock" data-testid="logs-team-chip">
          <span>{p.myTeamName && isSameTeam(filter.team, p.myTeamName) ? tr('logs.team.mine') : tr('logs.team.label')}:</span>
          <code title={tr('logs.team.chipTitle')}>{filter.team}</code>
          <Button variant="ghost" size="sm" data-testid="logs-team-change"
            onClick={p.onToggleTeamPicker}>{tr('logs.team.change')}</Button>
          {p.teamPickerOpen && (
            <TeamPicker value={p.teamDraft} onChange={p.onTeamDraft} width={190}
              placeholder={tr('logs.team.pick')}
              onEnter={v => { const t = (v ?? p.teamDraft).trim(); if (t) p.onSetTeam(t); }} />
          )}
          <Button variant="ghost" size="sm" onClick={p.onClearTeam}
            aria-label={`${tr('logs.team.remove')}: ${filter.team}`}
            title={tr('logs.team.removeTitle')}>×</Button>
        </span>
      )}
      {filter.hasTrace && (
        <span className="trace-lock" data-testid="logs-hastrace-chip">
          <span>◆ {tr('logs.dv.withTrace')}</span>
          <Button variant="ghost" size="sm" onClick={p.onToggleHasTrace}
            aria-label={tr('logs.dv.withTraceRemove')} title={tr('logs.dv.withTraceRemove')}>×</Button>
        </span>
      )}
      {/* Filter pills (Discover revamp step 1). ≠ toggles NOT (red tone), ◐
          disables without removing (drops out of the compiled query), ×
          removes; click the text to edit in place (v0.9.1219). */}
      {filters.map((f, i) => {
        const tone = f.negated ? 'var(--err)' : 'var(--accent2)';
        return (
          <span key={`${f.exists ? 'E' : 'V'}\u0000${f.key}\u0000${f.value}\u0000${(f.values ?? []).join(',')}`}
            className={`ld-pill${f.negated ? ' is-neg' : ''}${f.disabled ? ' is-off' : ''}`}>
            <LinkButton
              onClick={() => p.onEditPill(p.editPill === i ? null : i)}
              title="Düzenle — alan/operatör/değer"
              style={{ color: tone, textDecoration: f.disabled ? 'line-through' : undefined }}
              className="ld-pill-text">
              {f.negated && <b>NOT </b>}
              {f.exists ? <>∃ {f.key}</>
                : f.op ? <>{f.key} {f.op === 'gte' ? '≥' : '≤'} {f.value}</>
                : f.values && f.values.length > 1
                  ? <>{f.key} ∈ ({f.values.join(', ')})</>
                  : <>{f.key}: {f.value}</>}
            </LinkButton>
            {p.editPill === i && (
              <LogPillEditor filter={f} since={p.since}
                onApply={next => { p.onEditPill(null); p.onReplacePill(i, next); }}
                onCancel={() => p.onEditPill(null)} />
            )}
            <Button variant="ghost" size="sm" className={f.negated ? 'is-err' : undefined}
              onClick={() => p.onNegatePill(i)}
              title={f.negated ? 'Include (drop the NOT)' : 'Negate — exclude matching logs'}>≠</Button>
            <Button variant="ghost" size="sm"
              onClick={() => p.onDisablePill(i)}
              title={f.disabled ? 'Re-enable this filter' : 'Temporarily disable (keeps the pill)'}>◐</Button>
            <Button variant="ghost" size="sm"
              onClick={() => p.onRemovePill(i)}
              title="Remove this filter">×</Button>
          </span>
        );
      })}
      {filters.length > 0 && (
        <Button variant="ghost" size="sm"
          onClick={p.onClearPills}
          title="Remove all field filters (free-text search stays)">
          Clear all
        </Button>
      )}
      {suggestTeam && (
        <Button variant="ghost" size="sm" data-testid="logs-team-apply"
          title={tr('logs.team.applyTitle')}
          onClick={() => p.onSetTeam(p.myTeamName)}>{tr('logs.team.mine')}: {p.myTeamName}</Button>
      )}
      {p.traceHint && (
        <Button variant="ghost" size="sm" data-testid="logs-trace-hint" onClick={p.onUseTraceHint}
          title={tr('logs.dv.asTraceTitle')}>
          ⌗ {tr('logs.dv.asTrace')}
        </Button>
      )}
      {/* v0.10.418 (B7) — UYGULANAN arama cümlesi: compileSearch çıktısı,
          backend'e giden `search=` parametresinin kendisi (servis/env/seviye/
          trace ayrı parametre). Kutudaki taslak değil: Enter/Search'e dek
          gecikir. Canlı kuyrukta da geçerli (aynı dize SSE'ye gider). Pill'ler
          OLMADAN da (yalnız serbest metinle) görünür. */}
      {compiledSearch && (
        <>
          <Button ref={kqlRef} variant="ghost" size="sm" className="ld-kql-btn"
            aria-haspopup="dialog" aria-expanded={kqlOpen}
            title={tr('logs.dv.appliedSearch')}
            onClick={() => setKqlOpen(o => !o)}>
            KQL ⓘ
          </Button>
          <Popover anchorRef={kqlRef} open={kqlOpen} onClose={() => setKqlOpen(false)} kind="dialog"
            ariaLabel={tr('logs.dv.appliedSearch')} width={420}>
            <div className="ld-kql-head">
              <span>{tr('logs.dv.appliedSearch')}</span>
              <CopyButton value={compiledSearch} title="Arama cümlesini kopyala (KQL/Lucene alt kümesi)" />
            </div>
            <code title={compiledSearch}>{compiledSearch}</code>
          </Popover>
        </>
      )}
    </div>
  );
}
