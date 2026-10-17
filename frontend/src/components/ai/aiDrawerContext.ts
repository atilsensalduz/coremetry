import { createContext, useContext } from 'react';

// aiDrawerContext — v0.10.1291 (CoSRE follow-up D): "bu ağaç AI çekmecesinin
// AÇIKLAMA kipinde mi" bayrağı. Satır başı ✨ (sohbet log tablosu, trace
// listesi, Logs satırı) çekmecenin İÇİNDE gizlenir: tıklamak açık özneyi
// başka bir satırla değiştirir ve operatörün okuduğu açıklama kaybolur.
// Sağlayıcı yalnız CopilotChat'in AIDrawerBody dalı; varsayılan false
// (sohbet penceresi ve sayfalar butonu görür).
export const InAiDrawerContext = createContext(false);

export function useInAiDrawer(): boolean {
  return useContext(InAiDrawerContext);
}
