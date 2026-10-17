// cosreRules.ts — v0.10.1289 (CoSRE kurallar / hafıza): onay kartı ve kural
// panelinin SAF yardımcıları (tavan aynası, tek satır, kapsam etiketi, hata
// metni, süzgeç). Bileşen dosyaları yalnız bileşen dışa aktarsın diye ayrı.
import type { CosreRule, CosreRuleScope } from '@/lib/types';

// Sunucu tavanlarının aynası (internal/ai/cosrerules MinTextRunes/MaxTextRunes).
export const RULE_MIN = 8;
export const RULE_MAX = 200;

/** Tek satır: satır sonu / sekme boşluğa (sunucu da NormalizeText'le aynısını yapar). */
export function singleLine(s: string): string {
  return s.replace(/[\r\n\t]+/g, ' ');
}

export function ruleTextError(text: string): string | undefined {
  const n = Array.from(text.trim()).length;
  if (n < RULE_MIN) return `En az ${RULE_MIN} karakter`;
  if (n > RULE_MAX) return `En çok ${RULE_MAX} karakter`;
  return undefined;
}

export function ruleScopeLabel(sc: CosreRuleScope): string {
  if (sc.kind === 'service') return `servis ${sc.value ?? ''}`;
  if (sc.kind === 'team') return `takım ${sc.value ?? ''}`;
  return 'genel';
}

export function ruleTs(r: CosreRule): number {
  return Math.max(r.updatedAt ?? 0, r.createdAt);
}

/** 409 / HTTP hatasından okunur ileti (request "HTTP 409: {json}" fırlatır). */
export function ruleErrorText(e: unknown): { conflict: boolean; text: string } {
  const msg = e instanceof Error ? e.message : String(e);
  const m = /^HTTP (\d+): ([\s\S]*)$/.exec(msg);
  if (!m) return { conflict: false, text: msg };
  let text = m[2];
  try {
    const j = JSON.parse(m[2]) as { error?: string };
    if (j.error) text = j.error;
  } catch { /* düz metin */ }
  return { conflict: m[1] === '409', text };
}

// Türkçe katlama: "JENKINS" (tr → "jenkıns") ile "Jenkins" eşleşsin.
const fold = (s: string) => s.toLocaleLowerCase('tr').replace(/ı/g, 'i');

export function filterRules(rules: CosreRule[], q: string): CosreRule[] {
  const t = fold(q.trim());
  if (!t) return rules;
  return rules.filter(r => fold(`${r.text} ${ruleScopeLabel(r.scope)} ${r.author} ${r.authorEmail ?? ''}`).includes(t));
}
