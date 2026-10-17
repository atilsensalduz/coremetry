// phaseTrail.ts — v0.10.1273 (CoSRE adım 4): serbest döngüde akış + adım satırları.
//
// İki saf yardımcı:
//   - phaseTrail: turun adım detaylarından "Metrik → Log → Trace" kırıntısı.
//     Sunucu her araç çipine isteğe bağlı `phase` basar (chat_step_phase.go);
//     ardışık aynı evre TEK halka olur (üç query_logs çağrısı "Log"), evresiz
//     adım (etiket çipi, bilinmeyen araç) ve yürütülmeyen çağrı (skipped)
//     kırıntıya girmez. Akarken her yeni çiple canlı büyür.
//   - applyChatDelta: `delta` olayının metne uygulanışı; `reset` o ana dek
//     akanı siler (tool'lu turun önsözü geri alındı).
import type { ChatStepDetail } from '@/lib/types';

export function phaseTrail(details: readonly ChatStepDetail[] | undefined): string[] {
  const out: string[] = [];
  for (const d of details ?? []) {
    const p = d.phase?.trim();
    if (!p || d.skipped) continue;
    if (out[out.length - 1] !== p) out.push(p);
  }
  return out;
}

export function applyChatDelta(text: string | undefined, e: { text: string; reset?: boolean }): string {
  return (e.reset ? '' : (text ?? '')) + e.text;
}
