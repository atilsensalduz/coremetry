// AddToDashboard — v0.10.1443 (sohbetten dashboard, R1). Sohbet grafiğinin
// "Dashboard'a ekle" düğmesi ve cevabın "Hepsini dashboard yap" düğmesi —
// ikisi de PinToDashboardModal'ı (panels[]) açar; tıklanmadan hiçbir şey
// yazılmaz. Paneller lib/chatPanelSpec.ts'ten (SAF eşleme).
//
// Görünürlük: panel yoksa (grafik çevrilemez — charts_signals, log_spike,
// topology, trace_list, tablolar) düğme HİÇ çizilmez. Rol: editör/admin;
// viewer düğmeyi kapalı ve gerekçesiyle görür (viewer durumu görür, asla
// boşluk değil — CLAUDE.md değişmez #7). Düğme yalnız sohbet içinde
// (ChatDashContext) çizilir: EvidenceChart sohbet dışında da kullanılıyor.
import { lazy, Suspense, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { IconButton } from '@/components/ui/IconButton';
import { useAuthRole } from '@/components/AuthProvider';
import { useT } from '@/lib/i18n';
import type { Panel } from '@/lib/types';
import { canEditDashboards, chatDashDefaultName, useChatDashOrigin } from './chatDash';

// Modal yalnız tıklamada yüklenir (sohbet paketini şişirmesin).
const PinToDashboardModal = lazy(() =>
  import('@/pages/explore/PinToDashboardModal').then(m => ({ default: m.PinToDashboardModal })));

export function AddToDashboardButton({ panels, mode = 'icon' }: {
  /** Çevrilmiş paneller (lib/chatPanelSpec); boşsa düğme çizilmez. */
  panels: readonly Panel[];
  /** icon — grafik başlığının yanında; all — cevap eylem satırında "Hepsini dashboard yap". */
  mode?: 'icon' | 'all';
}) {
  const origin = useChatDashOrigin();
  const role = useAuthRole();
  const tr = useT();
  const [open, setOpen] = useState(false);
  if (!origin || panels.length === 0) return null;
  const can = canEditDashboards(role);
  const reason = can ? undefined : tr('dash.addFromChat.viewerReason');
  const label = mode === 'all' ? tr('dash.addFromChat.all') : tr('dash.addFromChat.one');
  return (
    <>
      {mode === 'all' ? (
        <Button variant="ghost" size="sm" disabled={!can} title={reason ?? label} aria-label={label}
          data-testid="chat-dash-all" onClick={() => setOpen(true)}>
          ▦ {label} ({panels.length})
        </Button>
      ) : (
        // Devre dışı gerekçesi ipucunda + erişilebilir adda (title= mandalı: buttonUnityRatchet).
        <IconButton icon="▦" size="sm" variant="ghost" aria-label={reason ? `${label} — ${reason}` : label} disabled={!can}
          tooltip={reason ?? label} data-testid="chat-dash-add"
          onClick={() => setOpen(true)} />
      )}
      {open && (
        <Suspense fallback={null}>
          <PinToDashboardModal panels={[...panels]} onClose={() => setOpen(false)}
            origin={{ conversation: origin.conversationId ?? undefined, exchangeId: origin.exchangeId }}
            defaultName={chatDashDefaultName(origin, panels)} />
        </Suspense>
      )}
    </>
  );
}
