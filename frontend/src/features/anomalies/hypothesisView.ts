import type { Tone } from '@/components/ui';
import type {
  ChatQueryEvidence, SREHypothesis, SREHypothesisBand, SREHypothesisCheck, SREInvestigation, SRETimelineKind,
} from '@/lib/types';

// hypothesisView — v0.10.1288 (AI SRE Faz 2). ProblemInvestigationCards'ın
// saf yardımcıları (bileşen dosyası yalnız bileşen dışa aktarsın —
// react-refresh). Go internal/sre hypotheses.go / timeline.go sabitlerinin
// aynası. Skor Go'da hesaplanır; burada yalnız çizilir.

/** Kartta gösterilen en çok hipotez (Go HypothesesShown). */
export const HYPOTHESES_SHOWN = 3;

/** Bant rozeti: olasılık bir uyarı değil — yüksek dikkat çeker, düşük soluk. */
export function bandTone(b: SREHypothesisBand): Tone {
  switch (b) {
    case 'yüksek': return 'warning';
    case 'orta': return 'info';
    default: return 'neutral';
  }
}

/** Kontrol sonucunun görünümü (+1 doğrular · −1 çürütür · 0 bilinmiyor). */
export function outcomeView(o: SREHypothesisCheck['outcome']): { glyph: string; label: string } {
  if (o > 0) return { glyph: '✓', label: 'doğrular' };
  if (o < 0) return { glyph: '✗', label: 'çürütür' };
  return { glyph: '?', label: 'bilinmiyor' };
}

/** Skor iki ondalık (Go aynı yuvarlamayı yapar). */
export function fmtScore(s: number): string {
  return Number.isFinite(s) ? s.toFixed(2) : '—';
}

/** Gösterilecek kartlar: skor sırası korunur (sunucu sıralı), ilk 3. */
export function shownHypotheses(inv: SREInvestigation): SREHypothesis[] {
  return (inv.hypotheses ?? []).slice(0, HYPOTHESES_SHOWN);
}

/** checkEvidence — kontrolün künyesi: "<adım>.evidences[i]" → adımın i'nci
 *  kanıtı; yalnız "<adım>" ya da bulunamayan künye → yok. */
export function checkEvidence(inv: SREInvestigation, ref: string | undefined): ChatQueryEvidence[] {
  if (!ref) return [];
  const m = /^([a-z_]+)\.evidences\[(\d+)\]$/.exec(ref);
  if (!m) return [];
  const ev = inv.steps.find(s => s.name === m[1])?.evidence?.[Number(m[2])];
  return ev ? [ev] : [];
}

/** İşaret metni ("elendi" / "onaylandı" + kim). */
export function humanLabel(h: SREHypothesis): string | null {
  const hs = h.humanState;
  if (!hs) return null;
  const who = hs.by ? ` · ${hs.by}` : '';
  return (hs.state === 'dismissed' ? 'operatör eledi' : 'operatör onayladı') + who;
}

/** Doğrula / Ele / Onayla yalnız bitmiş kayıtta (kuyruk / koşu işareti ezerdi). */
export function canAct(inv: SREInvestigation): boolean {
  return inv.state === 'done' || inv.state === 'partial';
}

export const TIMELINE_KIND: Record<SRETimelineKind, string> = {
  problem_start: 'Başlangıç',
  deploy: 'Deploy',
  spike_start: 'Log artışı',
  first_error: 'İlk hata',
  alert: 'Alarm',
  alert_resolved: 'Alarm kapandı',
  incident_attach: 'Incident',
};

/** Kök-neden paneline kaydırma hedefi (ProblemDetail sarmalayıcısı). */
export const ROOT_CAUSE_ANCHOR = 'problem-root-cause';
