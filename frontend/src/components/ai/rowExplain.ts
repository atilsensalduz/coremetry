// rowExplain — v0.10.1291: RowExplainCell'in yardımcıları (fast-refresh: bileşen dosyası yalnız bileşen ihraç eder).
import { parseAiParam, type AISubject } from '@/lib/aiSubject';
import { useInAiDrawer } from './aiDrawerContext';

/** rowSubjectOf — SAF: tel öznesi → AISubject (bozuk/boş → null). */
export function rowSubjectOf(raw: string | undefined): AISubject | null {
  return raw ? parseAiParam(raw) : null;
}

/** useRowExplain — satır ✨ sütunu çizilsin mi (çekmece dışında ve en az bir özne varken). */
export function useRowExplain(anySubject: boolean): boolean {
  return !useInAiDrawer() && anySubject;
}

