// serviceReportCardView — v0.10.1337: ServiceReportCard'ın saf yardımcıları.
import type { ChatServiceReportDrill } from '@/lib/types';

/** Hüküm rozeti: renk yalnız sapan hükümde (Sağlıklı nötr). */
export function verdictBadgeClass(v: string): string {
  if (v === 'Sorunlu') return 'badge b-err';
  if (v === 'Dikkat') return 'badge b-warn';
  return 'badge b-gray';
}

/** Kırılımın log durumu — "depo yok" / "okunamadı" / "log yok" AYRI cümleler. */
export function drillLogsNote(d: ChatServiceReportDrill): string | null {
  switch (d.logsState) {
    case 'no_store': return 'log deposu yapılandırılmamış (depo yok)';
    case 'error': return 'loglar okunamadı — "log yok" demek değil';
    case 'empty': return 'bu trace\'e bağlı log kaydı yok';
    case 'ok': return null;
  }
  return 'loglar okunmadı';
}
