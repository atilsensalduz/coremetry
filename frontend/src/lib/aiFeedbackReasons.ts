// aiFeedbackReasons.ts — v0.10.1280 (CoSRE adım 8): 👎 hazır nedenlerinin
// TEK listesi. Tel değeri → ekran etiketi.
// v0.10.1393 — oy rayı kaldırıldı (yeni oy yazılmaz); liste yalnız /ai
// etkileşim panelinin GEÇMİŞ oyları okuması için kalır.
import type { AIFeedbackReason } from './types';

const FEEDBACK_REASONS: ReadonlyArray<{ value: AIFeedbackReason; label: string; title: string }> = [
  { value: 'wrong', label: 'yanlış', title: 'Cevap yanlış bilgi içeriyor' },
  { value: 'incomplete', label: 'eksik', title: 'Cevap eksik — önemli bir kanıt / adım yok' },
  { value: 'irrelevant', label: 'alakasız', title: 'Cevap soruyla alakasız' },
];

/** Tel değeri → etiket; bilinmeyen/boş değer ''. */
export function feedbackReasonLabel(r: string | undefined | null): string {
  return FEEDBACK_REASONS.find(x => x.value === r)?.label ?? '';
}
