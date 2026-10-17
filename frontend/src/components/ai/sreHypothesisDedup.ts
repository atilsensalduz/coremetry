// sreHypothesisDedup — v0.10.1300 — CoSRE: hipotez doğrula çipi varken eski
// düğme gösterilmez (operatör 2026-10-17).
//
// Aynı cevapta kural-türevli "<hipotez> hipotezini doğrula" takip çipi
// (FollowupChips, v0.10.1291) ile action bloğunun SREHypothesisButton'u
// (v0.10.1288) aynı doğrulamayı iki kez sunuyordu. Çip aynı problem + tür için
// GÖRÜNÜYORSA düğme çizilmez; çipi olmayan cevaplarda (arşiv, eski sunucu, ya
// da çip şeridinin artık altında olmadığı önceki cevaplar) düğme kalır. İkisi
// de aynı useSREHypothesisVerify çağrısını kullanır. Saf (vitest node).
import type { ChatFollowup } from '@/lib/types';
import { parseSREHypothesisAction, type SREHypothesisAction } from '@/lib/pageActions';

/**
 * showSREHypothesisButton — SAF: `shownFollowups` = bu cevabın altında ŞU AN
 * çizilen takip çipleri (yoksa undefined). Çip FollowupChips ile aynı
 * çözücüden (parseSREHypothesisAction) geçer: bozuk yüklü çip çizilmez, o
 * yüzden düğmeyi de bastırmaz.
 */
export function showSREHypothesisButton(action: Pick<SREHypothesisAction, 'problemId' | 'hypothesis'>, shownFollowups: readonly ChatFollowup[] | undefined): boolean {
  if (!shownFollowups?.length) return true;
  return !shownFollowups.some(f => {
    const chip = f.action ? parseSREHypothesisAction(f.action) : null;
    return !!chip && chip.problemId === action.problemId && chip.hypothesis === action.hypothesis;
  });
}
