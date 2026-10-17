import type { Tone } from '@/components/ui';
import type { SRERecommendation, SREInvestigation } from '@/lib/types';

// recommendationView — v0.10.1293 — AI SRE Faz 4: yalnız öneri (runbook
// eşleme + geri alma metni). ProblemInvestigationRecommendations'ın saf
// yardımcıları (bileşen dosyası yalnız bileşen dışa aktarsın — react-refresh).
// Go internal/sre/recommend.go sabitlerinin aynası. Öneri YALNIZ METİNDİR:
// hiçbir düğme sistemde bir şeyi uygulamaz.

/** Ajanın her kartta taşıdığı rozet metni (Go RecPending). */
export const REC_PENDING = 'öneri — uygulanmadı' as const;

/** Kartta gösterilen öneriler (sunucu sırası: hipotez önerileri → runbook'lar). */
export function shownRecommendations(inv: SREInvestigation): SRERecommendation[] {
  return inv.recommendations ?? [];
}

/** Operatör işaretinin etiketi (yoksa null). */
export function recHumanLabel(r: SRERecommendation): { text: string; tone: Tone } | null {
  const who = r.ackBy ? ` · ${r.ackBy}` : '';
  if (r.state === 'ack') return { text: `operatör uyguladığını işaretledi${who}`, tone: 'success' };
  if (r.state === 'dismissed') return { text: `operatör eledi${who}`, tone: 'neutral' };
  return null;
}

/** Runbook bağlantısı uygulama içi mi (react-router ile git) yoksa dış mı. */
export function runbookTarget(r: SRERecommendation): { kind: 'internal' | 'external'; href: string } | null {
  const u = r.runbook?.url;
  if (!u) return null;
  if (u.startsWith('/api/')) return null; // hiçbir zaman bir uca bağlanma (Go zaten üretmez)
  return u.startsWith('/') ? { kind: 'internal', href: u } : { kind: 'external', href: u };
}

/** Uygulandı / Ele yalnız bitmiş kayıtta ve işaretlenmemiş öneride. */
export function canAckRecommendation(inv: SREInvestigation, r: SRERecommendation): boolean {
  return (inv.state === 'done' || inv.state === 'partial') && r.state === REC_PENDING;
}
