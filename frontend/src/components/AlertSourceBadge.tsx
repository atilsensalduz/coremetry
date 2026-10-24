import { Badge } from '@/components/ui/Badge';
import type { AlertSourceInfo } from '@/lib/types';
import { alertSourceLabel, alertSourceReason } from '@/lib/alertSource';

// AlertSourceBadge — v0.10.1489: kural ölçüsünün kaynağı (önizleme, taban
// önerisi, problem grafiği). Değerlendirici AYNI kaynağı kullanır; span'e
// düşüldüyse gerekçe rozetin yanında yazar (gizli tooltip değil — operatör
// eşiği hangi veriye göre seçtiğini görmeli). Renk nötr: kaynak bir durum değil.
// 2026-10-24 — VM hiç yapılandırılmamışsa gerekçe yazılmaz (alertSourceReason).
export function AlertSourceBadge({ source }: { source: AlertSourceInfo }) {
  const reason = alertSourceReason(source);
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 11, color: 'var(--text3)', minWidth: 0 }}>
      <Badge tone="neutral" title={source.evidence}>{alertSourceLabel(source)}</Badge>
      {reason && <span>VM kullanılmadı: {reason}</span>}
    </span>
  );
}
