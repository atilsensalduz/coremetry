import { formatAiParam, type AISubject } from '@/lib/aiSubject';
import type { ChatExplainAsk, ChatExplainMeta, ChatTurn } from '@/lib/types';
import type { ExplainEvidence } from './ExplainBody';
import { buildExplainContext } from './drawerChat';

// explainTurn — v0.10.1400 (Tek CoSRE; operatör: "Trace ve exception
// üzerinden çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi,
// CoSRE bütünselliğine uymuyor.").
//
// Trace / exception ✨ açıklaması artık CoSRE sohbetinin SIRADAN bir turu:
// görünür kullanıcı turu ("Bu trace'i açıkla · 5b8efff7…") + yapısal niyet
// (context.explainAsk {kind, id, src}). Sunucu cevabı ✨ düğmesinin çekirdeği
// ve önbellek anahtarıyla üretir (internal/api/chat_explain_turn.go); answer
// çerçevesinin `explain` künyesi balonun ExplainBody ile çizilmesini, kanıtın
// sayfaya duyurulmasını ve takip sorularının bağlamını sağlar.
//
// Saf modül (vitest node) — React yok.

/** Sohbet turu olarak açıklanan özne türleri (diğerleri çekmece özne kipinde kalır — 1401). */
export const EXPLAIN_TURN_KINDS = ['trace', 'exception'] as const;
export type ExplainTurnKind = typeof EXPLAIN_TURN_KINDS[number];

export function isExplainTurnKind(kind: string | null | undefined): kind is ExplainTurnKind {
  return kind === 'trace' || kind === 'exception';
}

/** shortExplainId — kullanıcı turunda görünen kısa kimlik (`ora:` öneki korunur). */
export function shortExplainId(id: string): string {
  const pre = id.startsWith('ora:') ? 'ora:' : '';
  const body = pre ? id.slice(4) : id;
  return body.length > 8 ? `${pre}${body.slice(0, 8)}…` : id;
}

/** explainUserText — açıklama turunun GÖRÜNEN kullanıcı metni. */
export function explainUserText(kind: ExplainTurnKind, id: string, label?: string): string {
  if (kind === 'trace') return `Bu trace'i açıkla · ${shortExplainId(id)}`;
  const l = (label ?? '').trim();
  return `${l || 'Exception'} grubunu açıkla · ${shortExplainId(id)}`;
}

/**
 * explainAskFor — özne → {görünen metin, yapısal niyet}. Yalnız trace /
 * exception (diğer türler null: çekmecenin özne kipi). Boş kimlik null.
 * v0.10.1404 — `label`: görünen metindeki grup adı (CoSRE başlangıcının
 * exception çipi "ORDERS_GET_X · orders-api"); yoksa "Exception".
 */
export function explainAskFor(subject: AISubject | null | undefined, src?: string, label?: string): { userText: string; explainAsk: ChatExplainAsk } | null {
  if (!subject || !isExplainTurnKind(subject.kind)) return null;
  const id = (subject.id ?? '').trim();
  if (!id) return null;
  return {
    userText: explainUserText(subject.kind, id, label),
    explainAsk: { kind: subject.kind, id, ...(src ? { src } : {}) },
  };
}

/** explainSubject — künye → çekmece öznesi (takip bağlamı / kodek için). */
export function explainSubject(meta: Pick<ChatExplainMeta, 'kind' | 'id'>): AISubject {
  return { kind: meta.kind, id: meta.id };
}

/** explainEvidence — künyeden ExplainBody'nin "Kanıt:" sayıları. */
export function explainEvidence(meta: ChatExplainMeta | undefined): ExplainEvidence | undefined {
  if (!meta) return undefined;
  return {
    spans: meta.evidenceSpanIds?.length ?? 0,
    traces: meta.evidenceTraceIds?.length ?? 0,
    oracle: meta.oracleRows ?? 0,
  };
}

/** Etkin açıklama: konuşmadaki SON tamamlanmış açıklama turu (+ sırası). */
export interface ActiveExplain {
  /** turun konuşmadaki sırası — ✕ ile çıkarma bu kimliğe bağlanır. */
  key: string;
  meta: ChatExplainMeta;
  text: string;
}

export function activeExplainFrom(turns: readonly ChatTurn[]): ActiveExplain | null {
  for (let i = turns.length - 1; i >= 0; i--) {
    const t = turns[i];
    if (t.role !== 'assistant' || !t.explain || t.pending || t.error) continue;
    const text = (t.text ?? '').trim();
    if (!text) continue;
    return { key: `${i}:${t.explain.kind}:${t.explain.id}`, meta: t.explain, text };
  }
  return null;
}

/**
 * explainFollowupContext — takip sorularının bağlamı (çekmece özne kipindeki
 * AIDrawerChat ile AYNI iki alan): `explain` = künye + açıklama metni + kanıt
 * kimlikleri; `subject` = `?ai=` kodeği (sunucu ham kanıtı yeniden kurar).
 */
export function explainFollowupContext(a: ActiveExplain): { explain: string; subject: string } {
  const subject = explainSubject(a.meta);
  return {
    explain: buildExplainContext({ subject, text: a.text, spanIds: a.meta.evidenceSpanIds, traceIds: a.meta.evidenceTraceIds }),
    subject: formatAiParam(subject),
  };
}

/** explainContextLabel — bağlam şeridindeki öğe ("✨ trace 5b8efff7…"). */
export function explainContextLabel(meta: Pick<ChatExplainMeta, 'kind' | 'id' | 'label'>): string {
  if (meta.kind === 'trace') return `✨ trace ${shortExplainId(meta.id)}`;
  return `✨ ${meta.label || 'exception'} ${shortExplainId(meta.id)}`;
}
