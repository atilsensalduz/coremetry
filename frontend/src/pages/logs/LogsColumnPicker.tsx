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
//
// v0.10.1516 — liste/↑↓/×/Reset gövdesi ui/ColumnsPicker'a taşındı (/traces
// aynı atomu kullanır); burada yalnız logs'a özgü ekleme yuvası kaldı.
import { useState } from 'react';
import { Button } from '@/components/ui/Button';
import { ColumnsPicker } from '@/components/ui/ColumnsPicker';
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
  const [needle, setNeedle] = useState('');
  const list = normalizeLogColumns(columns);
  const addable = addableLogColumns(columns, BUILTIN_LOG_COLUMNS, fields, needle);
  const isDefault = list.join(',') === normalizeLogColumns(DEFAULT_LOG_COLUMNS).join(',');
  return (
    <ColumnsPicker
      items={list.map(id => ({ id, label: logColumnLabel(id), removable: isRemovableLogColumn(id) }))}
      labels={{
        trigger: tr('logs.dv.columns'), title: tr('logs.dv.columnsTitle'), shown: tr('logs.dv.columnsShown'),
        up: tr('logs.dv.colUp'), down: tr('logs.dv.colDown'), remove: tr('logs.dv.colRemove'),
        fixedNote: tr('logs.dv.colMessageFixed'), reset: tr('logs.dv.colReset'),
      }}
      onMove={(id, dir) => onChange(moveLogColumn(columns, id, dir))}
      onRemove={id => onChange(removeLogColumn(columns, id))}
      onReset={() => onChange([...DEFAULT_LOG_COLUMNS])}
      isDefault={isDefault}>
      <input
        placeholder={tr('logs.dv.colAddPlaceholder')}
        aria-label={tr('logs.dv.colAddPlaceholder')}
        value={needle}
        onChange={e => setNeedle(e.target.value)}
        onKeyDown={e => {
          if (e.key === 'Enter' && addable[0]) { e.preventDefault(); onChange(addLogColumn(columns, addable[0])); setNeedle(''); }
        }} />
      <div className="colpick-add">
        {addable.length === 0 && <span className="colpick-none">{tr('logs.dv.colNoMatch')}</span>}
        {addable.map(f => (
          <Button key={f} variant="ghost" size="xs" className="colpick-cand" title={f}
            onClick={() => onChange(addLogColumn(columns, f))}>
            + {logColumnLabel(f)}
          </Button>
        ))}
      </div>
    </ColumnsPicker>
  );
}
