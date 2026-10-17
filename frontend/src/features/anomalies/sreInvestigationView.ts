import type { SREStatus } from '@/lib/types';

// sreInvestigationView — v0.10.1282 (AI SRE Faz 0a). Panelin saf koşulları
// (bileşen dosyası yalnız bileşen dışa aktarsın — react-refresh).

/** sreRoleMissing — "SRE rolü yok" banner koşulu: kuyrukta iş var, 60 s'dir
 *  drain kalp atışı yok, drain süreç içinde değil. */
export function sreRoleMissing(s: SREStatus | undefined): boolean {
  return !!s && s.queueDepth > 0 && !s.roleSeen && s.launcher !== 'inprocess';
}

/** v0.10.1286 — AI SRE ayar ekranı (Settings → AI SRE, admin). "Kapalı"
 *  bandı operatörü API'ye değil buraya yönlendirir. */
export const SRE_SETTINGS_PATH = '/settings/ai-sre';
