import { Badge } from '@/components/ui/Badge';
import type { AlertSourceInfo } from '@/lib/types';
import { alertSourceLabel } from '@/lib/alertSource';

// AlertSourceBadge — v0.10.1489: kural ölçüsünün kaynağı (önizleme, taban
// önerisi, problem grafiği). Değerlendirici AYNI kaynağı kullanır; span'e
// düşüldüyse gerekçe rozetin yanında yazar (gizli tooltip değil — operatör
// eşiği hangi veriye göre seçtiğini görmeli). Renk nötr: kaynak bir durum değil.
export function AlertSourceBadge({ source }: { source: AlertSourceInfo }) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 11, color: 'var(--text3)', minWidth: 0 }}>
      <Badge tone="neutral" title={source.evidence}>{alertSourceLabel(source)}</Badge>
      {!source.vm && source.reason && <span>VM kullanılmadı: {source.reason}</span>}
    </span>
  );
}
