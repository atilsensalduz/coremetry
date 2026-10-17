// detailsProps.ts — v0.10.1257: Details özellik şeridinin saf yardımcıları.
import type { WorkloadRollout } from '@/lib/types';

/** v0.10.1257 — "Java 17.0.9" gibi okunur çalışma zamanı etiketi (SAF). */
export function runtimeLabel(language: string, runtimeName?: string, runtimeVersion?: string): string {
  const lang = language === 'java' ? 'Java' : language;
  if (!runtimeVersion) return lang;
  const v = runtimeVersion.split('+')[0];
  return `${lang} ${v}${runtimeName && language !== 'java' ? ` (${runtimeName})` : ''}`;
}

/** v0.10.1257 — en son başlayan rollout (SAF). */
export function newestRollout(rows: WorkloadRollout[]): WorkloadRollout | null {
  return rows.reduce<WorkloadRollout | null>((a, r) => (!a || r.startedAt > a.startedAt ? r : a), null);
}

