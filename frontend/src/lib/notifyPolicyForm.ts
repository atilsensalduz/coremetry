// notifyPolicyForm — v0.10.1533 (Problem standardı R5). Bildirim politikası
// ekranının SAF yardımcıları: enforce'a geçiş kapısının metni (D8: 7 günlük
// gölge + "Gerçek problem" işaretleri ve onaylı vakalarda %100 recall), tutma
// kodlarının Türkçe adları ve özet saatlerinin doğrulaması.
import type { NotifyReplayReport } from './types';

/** Tutma kodu → okunur ad (Go chstore NotifyHold*; gate:Gx dahil). */
export function holdLabel(code: string): string {
  const fixed: Record<string, string> = {
    'p3:digest': 'P3 → özet', 'p3:off': 'P3 kapalı', hidden: 'kalıcı gizli', noise: '“problem değil” (süreli)',
    dedupe: 'tekrar penceresi', quiet_hours: 'sessiz saat', 'impact:error': 'etki tabanı (hata)',
    'impact:latency': 'etki tabanı (gecikme)', 'impact:traffic': 'etki tabanı (trafik)', 'gate:low': 'düşük güven',
  };
  if (fixed[code]) return fixed[code];
  const g = /^gate:(G\d)$/.exec(code);
  if (g) return `düşük güven (${g[1]})`;
  return code;
}

/** D8 kapısı: enforce'a geçiş için replay recall'u temiz mi + uyarı metni. */
export function enforceReadiness(r: NotifyReplayReport | null | undefined): { ok: boolean; text: string } {
  if (!r) return { ok: false, text: 'Replay sonucu yok — önce 14 günlük replay’i çalıştırın.' };
  const { checked, held, pass } = r.recall;
  if (checked === 0) {
    return { ok: false, text: 'Recall ölçülemedi: son günlerde “Gerçek problem” işareti ya da onaylı vaka yok. Enforce’a geçmeden önce birkaç gerçek olayı işaretleyin (D8).' };
  }
  if (!pass) {
    return { ok: false, text: `Recall %${Math.round(((checked - held) / checked) * 100)} — ${held} gerçek olay yeni bar tarafından TUTULURDU. Enforce önerilmez (D8: %100).` };
  }
  return { ok: true, text: `Recall %100 — ${checked} gerçek olayın hepsi yeni barla da bildirilirdi.` };
}

/** "HH:MM" listesini ayrıştır + doğrula (en çok 6, tekrar yok, sıralı). */
export function parseDigestTimes(raw: string): { times: string[]; error?: string } {
  const parts = raw.split(/[,\s]+/).map(s => s.trim()).filter(Boolean);
  const bad = parts.find(p => !/^([01]\d|2[0-3]):[0-5]\d$/.test(p));
  if (bad) return { times: [], error: `“${bad}” SS:DD biçiminde değil` };
  const uniq = [...new Set(parts)].sort();
  if (uniq.length > 6) return { times: [], error: 'En çok 6 özet saati' };
  return { times: uniq };
}

/** Aile başına "önce → sonra" azalma yüzdesi (gönderilen açılış başına). */
export function reductionPct(before: number, after: number): number | null {
  if (before <= 0) return null;
  return Math.round(((before - after) / before) * 100);
}
