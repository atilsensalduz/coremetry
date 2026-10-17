// aiFeedbackReasons.ts — v0.10.1280 (CoSRE adım 8): 👎 hazır nedenlerinin
// TEK listesi. Tel değeri → ekran etiketi; Go aynası chstore.AIFeedbackReasons
// (sunucu küme dışını 400 ile reddeder). Hem oy rayı (AIFeedbackButtons) hem
// /ai etkileşim paneli buradan okur — iki yerde ayrı yazılmış etiket kayardı.
import type { AIFeedbackReason } from './types';

export const FEEDBACK_REASONS: ReadonlyArray<{ value: AIFeedbackReason; label: string; title: string }> = [
  { value: 'wrong', label: 'yanlış', title: 'Cevap yanlış bilgi içeriyor' },
  { value: 'incomplete', label: 'eksik', title: 'Cevap eksik — önemli bir kanıt / adım yok' },
  { value: 'irrelevant', label: 'alakasız', title: 'Cevap soruyla alakasız' },
];

/** Tel değeri → etiket; bilinmeyen/boş değer ''. */
export function feedbackReasonLabel(r: string | undefined | null): string {
  return FEEDBACK_REASONS.find(x => x.value === r)?.label ?? '';
}
