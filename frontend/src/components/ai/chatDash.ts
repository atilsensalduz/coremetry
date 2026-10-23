// chatDash — v0.10.1443 (sohbetten dashboard, R1): sohbet bağlamı (konuşma
// kimliği CopilotChat'ten, soru + exchange ChatBubble'dan) ve SAF yardımcılar.
// AddToDashboard.tsx'ten ayrı: hızlı-yenileme sınırı (dosya yalnız bileşen
// dışa açsın) ve testlerin saf yarıya bileşensiz erişimi.
import { createContext, useContext } from 'react';
import type { Panel } from '@/lib/types';
import type { ChatPanelOrigin } from '@/lib/chatPanelSpec';

export interface ChatDashOrigin extends ChatPanelOrigin {
  exchangeId?: string;
}

/** Sohbet bağlamı: konuşma kimliği (CopilotChat) + soru/exchange (ChatBubble). */
export const ChatDashContext = createContext<ChatDashOrigin | null>(null);

export function useChatDashOrigin(): ChatDashOrigin | null {
  return useContext(ChatDashContext);
}

/** canEditDashboards — SAF: panoya yazma rolü (api.go editorRoles aynası). */
export function canEditDashboards(role: string | undefined): boolean {
  return role === 'admin' || role === 'editor';
}

/** chatDashDefaultName — SAF: yeni panonun önerilen adı (soru, kırpık). */
export function chatDashDefaultName(origin: ChatDashOrigin | null, panels: readonly Panel[]): string {
  const q = origin?.question?.replace(/\s+/g, ' ').trim();
  if (q) return Array.from(q).length > 60 ? `${Array.from(q).slice(0, 59).join('')}…` : q;
  return panels[0]?.title ?? 'CoSRE';
}
