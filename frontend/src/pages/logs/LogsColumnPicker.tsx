// LogsColumnPicker — v0.10.1502 (/logs Kibana-Discover dilim 4; operatör
// onaylı taslak 2026-10-24).
//
// "Columns ▾": tablonun orta sütunlarını sırala (↑ ↓), kaldır (×; Message
// kaldırılamaz), ZATEN YÜKLÜ alan listesinden ekle (/api/logs/fields — yeni
// istek YOK; yerleşik level/service/cluster/pod da aday) ve "Reset columns"
// ile DEFAULT_LOG_COLUMNS'a dön. Her değişiklik tek yoldan: `onChange` =
// Logs.tsx changeCols (state + localStorage + sunucu tercihi + URL `cols=`).
// Sunucu tercihine burada YAZILMAZ — tek yazım Logs.tsx changeCols içinde
// (Logs.prefs.test). Saf sunum.
import { useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { IconButton } from '@/components/ui/IconButton';
import { Popover } from '@/components/ui/Popover';
import {
  BUILTIN_LOG_COLUMNS, DEFAULT_LOG_COLUMNS, isRemovableLogColumn, logColumnLabel, normalizeLogColumns,
} from '@/components/LogTable';
import { addLogColumn, addableLogColumns, moveLogColumn, removeLogColumn } from './logColumns';
import { useT } from '@/lib/i18n';

export interface LogsColumnPickerProps {
  columns: string[];
  /** Yüklenmiş alan listesi (alan paneli + KQL tamamlaması ile AYNI state). */
  fields: string[];
  onChange: (next: string[]) => void;
}

export function LogsColumnPicker({ columns, fields, onChange }: LogsColumnPickerProps) {
  const tr = useT();
  const anchorRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const [needle, setNeedle] = useState('');
  const list = normalizeLogColumns(columns);
  const addable = addableLogColumns(columns, BUILTIN_LOG_COLUMNS, fields, needle);
  const isDefault = list.join(',') === normalizeLogColumns(DEFAULT_LOG_COLUMNS).join(',');
  return (
    <>
      <Button ref={anchorRef} variant="secondary" size="sm" aria-haspopup="dialog" aria-expanded={open}
        title={tr('logs.dv.columnsTitle')}
        onClick={() => setOpen(o => !o)}>
        {tr('logs.dv.columns')} ▾
      </Button>
      <Popover anchorRef={anchorRef} open={open} onClose={() => setOpen(false)} kind="dialog"
        ariaLabel={tr('logs.dv.columnsTitle')} width={320}>
        <div className="ld-cols-head">{tr('logs.dv.columnsShown')}</div>
        <ol className="ld-cols-list" aria-label={tr('logs.dv.columnsShown')}>
          {list.map((id, i) => (
            <li key={id} className="ld-cols-row">
              <span className="ld-cols-name" title={id}>{logColumnLabel(id)}</span>
              <IconButton size="xs" variant="bare" icon="↑" aria-label={`${tr('logs.dv.colUp')}: ${logColumnLabel(id)}`}
                disabled={i === 0} onClick={() => onChange(moveLogColumn(columns, id, -1))} />
              <IconButton size="xs" variant="bare" icon="↓" aria-label={`${tr('logs.dv.colDown')}: ${logColumnLabel(id)}`}
                disabled={i === list.length - 1} onClick={() => onChange(moveLogColumn(columns, id, 1))} />
              {isRemovableLogColumn(id) ? (
                <IconButton size="xs" variant="bare" icon="×" aria-label={`${tr('logs.dv.colRemove')}: ${logColumnLabel(id)}`}
                  onClick={() => onChange(removeLogColumn(columns, id))} />
              ) : (
                <span className="ld-cols-fixed" title={tr('logs.dv.colMessageFixed')} aria-label={tr('logs.dv.colMessageFixed')}>—</span>
              )}
            </li>
          ))}
        </ol>
        <input
          placeholder={tr('logs.dv.colAddPlaceholder')}
          aria-label={tr('logs.dv.colAddPlaceholder')}
          value={needle}
          onChange={e => setNeedle(e.target.value)}
          onKeyDown={e => {
            if (e.key === 'Enter' && addable[0]) { e.preventDefault(); onChange(addLogColumn(columns, addable[0])); setNeedle(''); }
          }} />
        <div className="ld-cols-add">
          {addable.length === 0 && <span className="ld-cols-none">{tr('logs.dv.colNoMatch')}</span>}
          {addable.map(f => (
            <Button key={f} variant="ghost" size="xs" className="ld-cols-cand" title={f}
              onClick={() => onChange(addLogColumn(columns, f))}>
              + {logColumnLabel(f)}
            </Button>
          ))}
        </div>
        <Button variant="secondary" size="sm" disabled={isDefault}
          onClick={() => onChange([...DEFAULT_LOG_COLUMNS])}>
          {tr('logs.dv.colReset')}
        </Button>
      </Popover>
    </>
  );
}
