// pendingCasesView — v0.10.1475 — /ai "Onay bekleyen vakalar" panelinin SAF
// yardımcıları (bileşen dosyası yalnız bileşen — react-refresh kuralı).
import type { IncidentCase, PendingCaseCounts } from '@/lib/types';

/** ISO zaman → ns (tsCompact/tsLong ns ister); geçersiz → 0 ("—"). */
export function isoToNs(iso: string): number {
  const t = Date.parse(iso);
  return Number.isFinite(t) && t > 0 ? t * 1e6 : 0;
}

/** Sayaç başlığı: değerlendirme yalnız PROBLEM vakalarını sayar. */
export function pendingCounterText(c: PendingCaseCounts): string {
  return `onaylı: ${c.confirmedProblem} / taslak: ${c.draftProblem}`;
}

/** Değerlendirme eşiği ipucu — eşik yalnız bilgi, hiçbir şeyi kilitlemez. */
export function evalReadinessHint(c: PendingCaseCounts, min: number): { ready: boolean; text: string } {
  const have = c.confirmedProblem;
  if (have >= min) {
    return { ready: true, text: `Değerlendirme için yeterli (${have} ≥ ${min} onaylı problem vakası) — /ai › AI SRE doğruluğu'ndan koşturulabilir` };
  }
  return { ready: false, text: `AI SRE doğruluk ölçümü en az ${min} onaylı problem vakası ister — ${min - have} vaka daha` };
}

/** Sıradaki açılacak satır: onaylanan/atlanan satırın ardından gelen ilk
 *  atlanmamış satır (yoksa null). Toplu inceleme akışı: Onayla → sıradaki. */
export function nextCaseId(rows: Pick<IncidentCase, 'caseId'>[], current: string, skip: ReadonlySet<string>): string | null {
  const i = rows.findIndex(r => r.caseId === current);
  for (let j = i + 1; j < rows.length; j++) {
    if (!skip.has(rows[j].caseId)) return rows[j].caseId;
  }
  return null;
}

const SOURCE_LABEL: Record<string, string> = { rca_verdict: 'RCA hükmü', hypothesis: 'hipotez', ai_summary: 'AI özeti' };

/** Öneri kaynağı etiketi (cosrelearn.SuggestedCause sırası). */
export function suggestionSourceLabel(s?: string): string {
  if (!s) return '—';
  return SOURCE_LABEL[s] ?? s;
}
