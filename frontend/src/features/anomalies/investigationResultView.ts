import type { Tone } from '@/components/ui';
import type { ChatQueryEvidence, SREInvestigation, SREInvestigationFinding, SREInvestigationStep } from '@/lib/types';

// investigationResultView — v0.10.1287 (AI SRE Faz 1b). InvestigationResult'ın
// saf yardımcıları (bileşen dosyası yalnız bileşen dışa aktarsın —
// react-refresh). Adım adları Go internal/sre/plan.go sabitlerinin aynası.

/** Boru hattı sırası (dalga 1 → dalga 2 → anlatım); tablo bu sırayla, sıralanmaz. */
export const STEP_ORDER = ['sweep', 'deploys', 'deps', 'pivot', 'log_spike', 'events', 'node', 'summary'] as const;

export const STEP_LABEL: Record<string, string> = {
  sweep: 'Servis taraması',
  deploys: 'Deploy',
  deps: 'Bağımlılık',
  pivot: 'Trace pivotu',
  log_spike: 'Log artışı',
  events: 'Olaylar', // v0.10.1288 — alarmlar + aşağı akış db/ext problemleri + incident
  node: 'Node baskısı', // v0.10.1288 — tembel; yalnız doygunluk hipotezi isterse
  summary: 'AI özeti',
};

/** orderedSteps — adımlar boru hattı sırasıyla (bilinmeyen sonda), en çok 8. */
export function orderedSteps(steps: readonly SREInvestigationStep[]): SREInvestigationStep[] {
  const rank = (n: string) => {
    const i = (STEP_ORDER as readonly string[]).indexOf(n);
    return i < 0 ? STEP_ORDER.length : i;
  };
  return [...steps].sort((a, b) => rank(a.name) - rank(b.name)).slice(0, STEP_ORDER.length);
}

/** Adım durumu: yalnız sapan durum renk alır (tablo standardı — ok düz metin). */
export function stepStateView(state: string): { label: string; tone: Tone | null } {
  switch (state) {
    case 'ok': return { label: 'tamam', tone: null };
    case 'partial': return { label: 'kısmi', tone: 'warning' };
    case 'error': return { label: 'okunamadı', tone: 'danger' };
    case 'skipped': return { label: 'atlandı', tone: null };
    default: return { label: state, tone: null };
  }
}

/** Adım süresi: 950 ms / 1,2 sn. */
export function fmtStepDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return '—';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(1).replace('.', ',')} sn`;
}

/** findingEvidence — bulgunun kanıt künyeleri: `evidences[i]` → bulguyu üreten
 *  adımın evidence[i]'si (adım yoksa sweep). Tekrarsız, bulunamayan düşer. */
export function findingEvidence(inv: SREInvestigation, f: SREInvestigationFinding): ChatQueryEvidence[] {
  const step = inv.steps.find(s => s.name === (f.step || 'sweep'));
  const evs = step?.evidence ?? [];
  const out: ChatQueryEvidence[] = [];
  const seen = new Set<number>();
  for (const ref of f.evidenceRefs ?? []) {
    const m = /^evidences\[(\d+)\]$/.exec(ref);
    if (!m) continue;
    const i = Number(m[1]);
    if (seen.has(i) || !evs[i]) continue;
    seen.add(i);
    out.push(evs[i]);
  }
  return out;
}

/** Bulgu türünün Türkçe adı (Go sre kindTR aynası). */
export function findingKindLabel(kind: string): string {
  switch (kind) {
    case 'operation': return 'operasyon';
    case 'function': return 'fonksiyon';
    case 'dependency': return 'bağımlılık';
    case 'k8s': return 'Kubernetes';
    case 'jvm': return 'JVM';
    case 'node': return 'node';
    default: return kind;
  }
}

/** askInChatQuestion — "Sohbette sor": problem kapsamı (@problem:) + kayıtlı
 *  araştırmayı (get_problem `investigation`) kullanma talimatı. */
export function askInChatQuestion(inv: SREInvestigation): string {
  return `@problem:${inv.problemId} için kayıtlı AI SRE araştırmasını değerlendir — bulgular neyi gösteriyor, sonraki adım ne olmalı?`;
}
