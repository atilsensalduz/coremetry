// rowExplain — v0.10.1291: RowExplainCell'in yardımcıları (fast-refresh: bileşen dosyası yalnız bileşen ihraç eder).
// v0.10.1407 (Tek CoSRE adım 4) — useRowExplain kalktı: çekmecenin açıklama
// kipi (aiDrawerContext) silindi; satır ✨'i artık açık bir özneyi
// DEĞİŞTİRMEZ, sohbete bir tur gönderir — sohbet içi tablolarda da görünür.
import { parseAiParam, type AISubject } from '@/lib/aiSubject';

/** rowSubjectOf — SAF: tel öznesi → AISubject (bozuk/boş → null). */
export function rowSubjectOf(raw: string | undefined): AISubject | null {
  return raw ? parseAiParam(raw) : null;
}
