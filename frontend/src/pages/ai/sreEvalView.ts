// sreEvalView — v0.10.1301 — AI SRE Faz 5: geçmiş problemlerle doğruluk
// ölçümü. SREEvalPanel'in SAF yardımcıları (bileşen dosyası yalnız bileşen
// dışa aktarsın — react-refresh). Ölçüler Go'da (sre.scoreEval) hesaplanır;
// burada yalnız biçim + vaka satırının isabet etiketi (Go ile aynı kural).
import type { SREEvalCase, SREEvalCaseState, SREEvalDetOutcome, SREEvalDetOutcomeValue, SREEvalHit, SREEvalRatio, SREEvalRun, SREHypothesisKind } from '@/lib/types';

/** Go hypothesisTable etiketlerinin aynası (eval özetleri etiket taşımaz). */
export const HYP_KIND_LABEL: Record<SREHypothesisKind, string> = {
  deploy: 'Deploy',
  config: 'Konfigürasyon değişikliği',
  saturation: 'Doygunluk / OOM',
  dependency: 'Bağımlılık arızası',
  external: 'Dış servis kesintisi',
  db_slow: 'Veritabanı yavaşlığı',
  traffic_surge: 'Trafik sıçraması',
  code_regression: 'Kod gerilemesi / yeni exception',
};

/** incident_cases kategorileri (cosrelearn.CaseCategories) — Türkçe ad. */
export const CATEGORY_LABEL: Record<string, string> = {
  deploy: 'deploy', config: 'konfigürasyon', capacity: 'kapasite', dependency: 'bağımlılık',
  code_bug: 'kod hatası', infra: 'altyapı', data: 'veri', external: 'dış servis', noise: 'gürültü', other: 'diğer',
};

export function categoryLabel(c: string): string {
  return CATEGORY_LABEL[c] ?? c;
}

export const CASE_STATE_LABEL: Record<SREEvalCaseState, string> = {
  queued: 'kuyrukta', running: 'tekrar ediliyor', done: 'bitti', partial: 'kısmi',
  error: 'tamamlanamadı', skipped: 'atlandı', unmapped: 'eşlenmiyor',
};

/** Oran (0..1) → "%NN"; payda yoksa (null) "—". */
export function evalPct(v: number | null | undefined): string {
  return v == null || !Number.isFinite(v) ? '—' : `%${Math.round(v * 100)}`;
}

/** Kapsam yüzdesi (0..100) → "%NN". */
export function coveragePct(v: number | null | undefined): string {
  return v == null || !Number.isFinite(v) ? '—' : `%${Math.round(v)}`;
}

/** Süre (ms) → "42 sn" / "3,5 dk" / "1,2 sa". */
export function durationTR(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms) || ms < 0) return '—';
  const s = ms / 1000;
  if (s < 90) return `${Math.round(s)} sn`;
  const m = s / 60;
  if (m < 90) return `${m.toFixed(1).replace('.', ',')} dk`;
  return `${(m / 60).toFixed(1).replace('.', ',')} sa`;
}

/** Vaka puanlandı mı (Go scoreEval paydası: biten tekrar + veri + hipotez). */
export function caseScored(c: SREEvalCase): boolean {
  return (c.state === 'done' || c.state === 'partial') && !!c.coverage && c.coverage.data > 0 && (c.top?.length ?? 0) > 0;
}

export type CaseHit = 'top1' | 'top3' | 'miss';

/** Vakanın isabeti (puanlanmayan vaka null). */
export function caseHit(c: SREEvalCase): CaseHit | null {
  if (!caseScored(c)) return null;
  const top = (c.top ?? []).slice(0, 3);
  if (top[0]?.category === c.category) return 'top1';
  return top.some(h => h.category === c.category) ? 'top3' : 'miss';
}

export const HIT_LABEL: Record<CaseHit, string> = { top1: 'ilk sırada', top3: 'ilk 3\'te', miss: 'kaçtı' };

/** Vaka puanlanmadıysa neden (satırın soluk notu). */
export function caseUnscoredReason(c: SREEvalCase): string | null {
  if (caseScored(c)) return null;
  if (c.state === 'done' || c.state === 'partial') {
    return (c.top?.length ?? 0) === 0 ? 'hipotez yok' : 'veri yok — pencere saklama dışında';
  }
  return c.reason || CASE_STATE_LABEL[c.state] || c.state;
}

/** Kapsam metni: "3/5 okuyucu veri döndü". */
export function coverageText(c: SREEvalCase): string | null {
  const cov = c.coverage;
  if (!cov) return null;
  const total = cov.data + cov.noData + cov.failed;
  if (total === 0) return null;
  return `${cov.data}/${total}`;
}

/** Form varsayılanı: son 90 gün, yyyy-mm-dd (UTC). */
export function defaultEvalWindow(nowMs: number): { from: string; to: string } {
  const d = (ms: number) => new Date(ms).toISOString().slice(0, 10);
  return { from: d(nowMs - 90 * 86_400_000), to: d(nowMs) };
}

/** yyyy-mm-dd → unix ms (UTC gün başı; `to` için gün SONU). null = geçersiz. */
export function dateInputMs(v: string, endOfDay = false): number | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(v)) return null;
  const ms = Date.parse(`${v}T00:00:00Z`);
  if (!Number.isFinite(ms)) return null;
  return endOfDay ? ms + 86_400_000 - 1 : ms;
}

/** Form doğrulaması — sunucuyla aynı sınırlar (limit 1–50, from < to, ≤366 gün). */
export function validateEvalForm(from: string, to: string, limit: number): { from: number; to: number; limit: number } | string {
  const f = dateInputMs(from);
  const t = dateInputMs(to, true);
  if (f == null || t == null) return 'tarih yyyy-aa-gg olmalı';
  if (t <= f) return 'başlangıç bitişten önce olmalı';
  if (t - f > 366 * 86_400_000) return 'pencere en çok 366 gün';
  if (!Number.isInteger(limit) || limit < 1 || limit > 50) return 'vaka sayısı 1–50';
  return { from: f, to: t, limit };
}

// ── v0.10.1450 — dondurulmuş küme + karşılaştırma (program 2a) ─────────────

/** Deterministik sonuç (Go evalDetOutcome) — Türkçe ad. */
export const DET_OUTCOME_LABEL: Record<SREEvalDetOutcomeValue, string> = {
  confirmed: 'doğrulandı', likely: 'olası', ambiguous: 'belirsiz', inconclusive: 'sonuçsuz', no_data: 'veri yok', error: 'hata',
};

const DET_REASON_LABEL: Record<string, string> = { low_score: 'düşük skor', low_coverage: 'düşük kapsam', no_steps: 'adım yok' };

/** "olası" / "sonuçsuz (düşük kapsam)"; sonuç yoksa "—". */
export function detOutcomeText(o: SREEvalDetOutcome | undefined): string {
  if (!o) return '—';
  const v = DET_OUTCOME_LABEL[o.value] ?? o.value;
  return o.reason ? `${v} (${DET_REASON_LABEL[o.reason] ?? o.reason})` : v;
}

export const PAIR_HIT_LABEL: Record<SREEvalHit, string> = { top1: 'ilk sırada', top3: 'ilk 3\'te', miss: 'kaçtı', unscored: 'puansız' };

/** Oran farkı (0..1 ölçeği) → yüzde puanı: "+5 puan" / "−12 puan" / "0"; null "—". */
export function evalDelta(v: number | null | undefined, scale = 100): string {
  if (v == null || !Number.isFinite(v)) return '—';
  const p = Math.sign(v) * Math.round(Math.abs(v * scale)); // simetrik yuvarlama (−12,5 → −13)
  if (p === 0) return '0';
  return `${p > 0 ? '+' : '−'}${Math.abs(p)} puan`;
}

/** Oran + pay/payda: "%67 (2/3)"; payda 0 → "— (0/0)". */
export function ratioText(r: SREEvalRatio | undefined): string {
  if (!r) return '—';
  return `${evalPct(r.v)} (${r.n}/${r.d})`;
}

/** Koşunun kipi (2c "döngü" doldurur). */
export function runMode(r: Pick<SREEvalRun, 'loop'>): 'deterministik' | 'döngü' {
  return r.loop ? 'döngü' : 'deterministik';
}

/** Manşet (bağımsız) top-1 metni; bayrak bilinmiyorsa null (eski koşu). */
export function independentTop1(r: Pick<SREEvalRun, 'score' | 'suggestionKnown'>): { pct: string; hint: string } | null {
  if (!r.suggestionKnown) return null;
  const sc = r.score;
  return {
    pct: evalPct(sc.independentTop1Acc),
    hint: `${sc.independentTop1Hits ?? 0}/${sc.independentScored ?? 0} vaka · ${sc.acceptedSuggestion ?? 0} öneri aynen kabul (manşet dışı)`,
  };
}
