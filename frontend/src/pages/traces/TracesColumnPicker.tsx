// TracesColumnPicker — v0.10.1516 (operatör isteği 2026-10-24: "kolon
// sıralamasını da değiştirebilsin isteyen kullanıcılar"). /traces listesinin
// "Sütunlar ▾"ı: ui/ColumnsPicker (/logs v0.10.1502 seçicisinin genel hâli)
// üstünde ince sarmalayıcı — başlık sürükle-bırak'ın klavye eşi.
//
// Start time sabit (pinned: taşınmaz, önüne geçilmez — lib/traceColumns
// PINNED_TRACE_COL); yerleşik kolonlar taşınır ama kaldırılmaz; attribute
// kolonları × ile kaldırılır (çipteki ile aynı yol). Ekleme ColumnManager'da
// ("+ Add column") kalır. Saf sunum: her değişiklik Traces.tsx'in tek yazım
// yoluna (applyColOrder → state → URL `cols=` + localStorage + sunucu tercihi).
import { ColumnsPicker } from '@/components/ui/ColumnsPicker';
import { isBuiltinTraceCol, isDefaultTraceOrder, moveTraceColumn, PINNED_TRACE_COL, traceColumnOrder, extrasOfOrder } from '@/lib/traceColumns';
import { useT } from '@/lib/i18n';

export interface TracesColumnPickerProps {
  /** Çizilen sıra (resolveTraceOrder). */
  order: string[];
  labelOf: (id: string) => string;
  onChange: (nextOrder: string[]) => void;
  onRemove: (id: string) => void;
}

export function TracesColumnPicker({ order, labelOf, onChange, onRemove }: TracesColumnPickerProps) {
  const tr = useT();
  return (
    <ColumnsPicker
      items={order.map(id => ({
        id, label: labelOf(id),
        pinned: id === PINNED_TRACE_COL,
        removable: !isBuiltinTraceCol(id),
      }))}
      labels={{
        trigger: tr('traces.cols.trigger'), title: tr('traces.cols.title'), shown: tr('traces.cols.shown'),
        up: tr('logs.dv.colUp'), down: tr('logs.dv.colDown'), remove: tr('logs.dv.colRemove'),
        fixedNote: tr('traces.cols.builtinFixed'), pinnedNote: tr('traces.cols.pinned'),
        reset: tr('traces.cols.reset'),
      }}
      onMove={(id, dir) => onChange(moveTraceColumn(order, id, dir))}
      onRemove={onRemove}
      onReset={() => onChange(traceColumnOrder(extrasOfOrder(order)))}
      isDefault={isDefaultTraceOrder(order)} />
  );
}
