// incidentCaseView — CoSRE öğrenme Faz 3: vaka kartının saf yardımcıları.
import type { IncidentCase } from '@/lib/types';

const CATEGORY_LABEL: Record<string, string> = {
  deploy: 'Deploy', config: 'Yapılandırma', capacity: 'Kapasite', dependency: 'Bağımlılık',
  code_bug: 'Kod hatası', infra: 'Altyapı', data: 'Veri', external: 'Dış sistem',
  noise: 'Gürültü (gerçek olay değil)', other: 'Diğer',
};

export function categoryLabel(k: string): string {
  return CATEGORY_LABEL[k] ?? k;
}

export function correctnessLabel(v: number): string {
  if (v === 1) return 'doğruydu';
  if (v === 0) return 'yanlıştı';
  return 'değerlendirilemedi';
}

const pct = (v: number) => `%${(v * 100).toFixed(v * 100 < 10 ? 1 : 0)}`;
const ms = (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(1)} s` : `${Math.round(v)} ms`);

/** Ölçülen sinyaller düz cümle; ölçülmeyen (0/boş) satır yazılmaz. */
export function caseSignalLines(c: IncidentCase): string[] {
  const out: string[] = [];
  const codes = c.errorCodes ?? [];
  if (codes.length) out.push(`Hata kodları: ${codes.join(', ')}`);
  const fn = c.functionCodes ?? [];
  if (fn.length) out.push(`Hata veren function code: ${fn.join(', ')}`);
  const ch = c.channelCodes ?? [];
  if (ch.length) out.push(`Kanal: ${ch.join(', ')}`);
  if (c.errRateBefore > 0 || c.errRateDuring > 0) out.push(`Hata oranı ${pct(c.errRateBefore)} → ${pct(c.errRateDuring)}`);
  if (c.p99BeforeMs > 0 || c.p99DuringMs > 0) out.push(`p99 ${ms(c.p99BeforeMs)} → ${ms(c.p99DuringMs)}`);
  out.push(c.deployInWindow ? `Pencerede deploy: ${c.deployVersion || 'evet'}` : 'Pencerede deploy yok');
  return out;
}

// ── v0.10.1475 — onay formu (Problem kartı + /ai bekleyen liste ORTAK) ──

/** Go aynası: chstore.SameCause — boşluk/harf farkı dışında aynı metin
 *  (onaylayan CoSRE önerisini değiştirmeden kabul etti). Eval'in
 *  AcceptedSuggestion bayrağı ve sunucunun cosre_correct=1 kararı bunu kullanır. */
export function sameCause(a: string, b: string): boolean {
  const norm = (s: string) => s.toLowerCase().split(/\s+/).filter(Boolean).join(' ');
  const na = norm(a);
  return na !== '' && na === norm(b);
}

/** Formun ilk kategorisi (öneri DEĞİL — model kategori önermiyor): pencerede
 *  deploy varsa deploy, yoksa diğer. Yalnız seçim; hiçbir şey yazılmaz. */
export function defaultCaseCategory(c: Pick<IncidentCase, 'deployInWindow'>): string {
  return c.deployInWindow ? 'deploy' : 'other';
}

/** Formun altındaki canlı not: onay manşet "bağımsız" ölçüye sayılacak mı. */
export function independenceNote(suggested: string, cause: string): { independent: boolean; text: string } {
  if (!suggested.trim()) return { independent: true, text: 'CoSRE öneri vermemişti — onay bağımsız vaka sayılır' };
  if (sameCause(suggested, cause)) {
    return { independent: false, text: 'Öneri aynen kabul ediliyor — doğruluk manşetine (bağımsız vakalar) sayılmaz; yanlışsa düzeltin' };
  }
  return { independent: true, text: 'Öneri düzeltildi — bağımsız vaka olarak manşet ölçüye sayılır' };
}

/** Vakanın kaynak sayfası (Problem çekmecesi / exception grubu). */
export function caseHref(c: Pick<IncidentCase, 'source' | 'sourceId'>): string {
  return c.source === 'exception'
    ? `/problems?exception=${encodeURIComponent(c.sourceId)}`
    : `/problems?problem=${encodeURIComponent(c.sourceId)}`;
}
