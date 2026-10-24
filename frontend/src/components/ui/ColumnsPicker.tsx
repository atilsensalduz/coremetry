// ColumnsPicker — v0.10.1516. "Sütunlar ▾": tablo sütunlarını sırala (↑ ↓),
// kaldır (×), sıfırla; ekleme alanı çağıranın `children` yuvası.
//
// /logs'un v0.10.1502 LogsColumnPicker'ından GENELLEŞTİRİLDİ (operatör isteği
// 2026-10-24: /traces'te de sütun sırası). İkinci bir seçici yazılmadı:
// LogsColumnPicker artık bu atomun üstünde ince bir sarmalayıcı (ekleme =
// yüklenmiş alan listesi), /traces ise ekleme yuvası olmadan kullanır
// (attribute ekleme ColumnManager'da kalır).
//
// Saf sunum: her değişiklik çağıranın tek yazım yoluna (`onMove` /
// `onRemove` / `onReset`) gider; burada URL / localStorage / sunucu tercihi
// YAZILMAZ. Klavye yolu: tetik Enter/Boşluk ile açılır, Tab satırların ↑/↓/×
// düğmelerinde gezer — başlık sürükle-bırak'ın (DataTableHead, opt-in
// `reorderable`) erişilebilir eşi.
//
// Sabit (`pinned`) satır taşınmaz ve hiçbir satır onun önüne geçemez:
// üstündeki satır sabitse ↑ devre dışı (/traces: Start time en solda).
import { useRef, useState, type ReactNode } from 'react';
import { Button } from './Button';
import { IconButton } from './IconButton';
import { Popover } from './Popover';

export interface ColumnsPickerItem {
  id: string;
  label: string;
  /** Taşınmaz (ve önüne geçilmez). */
  pinned?: boolean;
  /** × gösterilir; false → `fixedNote` işareti. */
  removable?: boolean;
}

export interface ColumnsPickerLabels {
  trigger: string;
  title: string;
  shown: string;
  up: string;
  down: string;
  remove: string;
  /** Kaldırılamayan satırın işaret açıklaması. */
  fixedNote: string;
  /** Sabit satırın işaret açıklaması (yoksa fixedNote). */
  pinnedNote?: string;
  reset: string;
}

export interface ColumnsPickerProps {
  items: ColumnsPickerItem[];
  labels: ColumnsPickerLabels;
  onMove: (id: string, dir: -1 | 1) => void;
  onRemove?: (id: string) => void;
  onReset: () => void;
  /** Varsayılandayken Reset devre dışı. */
  isDefault: boolean;
  /** Ekleme alanı (listenin altı, Reset'in üstü). */
  children?: ReactNode;
  width?: number;
}

export function ColumnsPicker({ items, labels, onMove, onRemove, onReset, isDefault, children, width = 320 }: ColumnsPickerProps) {
  const anchorRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button ref={anchorRef} variant="secondary" size="sm" aria-haspopup="dialog" aria-expanded={open}
        title={labels.title}
        onClick={() => setOpen(o => !o)}>
        {labels.trigger} ▾
      </Button>
      <Popover anchorRef={anchorRef} open={open} onClose={() => setOpen(false)} kind="dialog"
        ariaLabel={labels.title} width={width}>
        <div className="colpick-head">{labels.shown}</div>
        <ol className="colpick-list" aria-label={labels.shown}>
          {items.map((it, i) => {
            const upBlocked = it.pinned || i === 0 || !!items[i - 1]?.pinned;
            const downBlocked = it.pinned || i === items.length - 1;
            return (
              <li key={it.id} className="colpick-row">
                <span className="colpick-name" title={it.id}>{it.label}</span>
                <IconButton size="xs" variant="bare" icon="↑" aria-label={`${labels.up}: ${it.label}`}
                  disabled={upBlocked} onClick={() => onMove(it.id, -1)} />
                <IconButton size="xs" variant="bare" icon="↓" aria-label={`${labels.down}: ${it.label}`}
                  disabled={downBlocked} onClick={() => onMove(it.id, 1)} />
                {it.removable && onRemove ? (
                  <IconButton size="xs" variant="bare" icon="×" aria-label={`${labels.remove}: ${it.label}`}
                    onClick={() => onRemove(it.id)} />
                ) : (
                  <span className="colpick-fixed"
                    title={it.pinned ? (labels.pinnedNote ?? labels.fixedNote) : labels.fixedNote}
                    aria-label={it.pinned ? (labels.pinnedNote ?? labels.fixedNote) : labels.fixedNote}>—</span>
                )}
              </li>
            );
          })}
        </ol>
        {children}
        <Button variant="secondary" size="sm" disabled={isDefault} onClick={onReset}>
          {labels.reset}
        </Button>
      </Popover>
    </>
  );
}
