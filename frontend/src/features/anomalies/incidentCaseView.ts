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
