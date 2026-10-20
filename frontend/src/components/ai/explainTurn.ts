import { formatAiParam, parseAiParam, type AISubject } from '@/lib/aiSubject';
import type { ChatExplainAsk, ChatExplainKind, ChatExplainMeta, ChatSendWindow, ChatTurn } from '@/lib/types';
import type { ExplainEvidence } from './ExplainBody';
import { buildExplainContext, endpointAskQuestion } from './drawerChat';

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
// v0.10.1405 (Tek CoSRE adım 2) — span, problem, incident, anomaly, servis
// sağlığı, runbook ve log satırı da sohbet turu (sunucu: chat_explain_registry.go,
// düğmenin kurucusu + aynı önbellek satırı). Kimliği tek alana sığmayan türler
// (span / service-health / log) niyete `subject` kodeğini (formatAiParam) koyar.
// endpoint: ayrı açıklama yüzeyi yok — görünür soru (endpointAskQuestion) +
// düğmeye basılan pencere (ChatSendWindow), operasyon ön-getirmesi çözer.
// charts / log-spike çekmece özne kipinde kalır (1407).
//
// Saf modül (vitest node) — React yok.

/** Sohbet turu olarak açıklanan özne türleri (charts / log-spike çekmece özne kipinde — 1407). */
export const EXPLAIN_TURN_KINDS = [
  'trace', 'exception', 'span', 'problem', 'incident', 'anomaly', 'service-health', 'runbook', 'log', 'endpoint',
] as const;
export type ExplainTurnKind = typeof EXPLAIN_TURN_KINDS[number];

const TURN_KIND_SET = new Set<string>(EXPLAIN_TURN_KINDS);
/** Tek kimlikli türler: niyet `subject` taşımaz, özne {kind, id}'den kurulur. */
const SIMPLE_KINDS = new Set<string>(['trace', 'exception', 'problem', 'incident', 'anomaly', 'runbook']);

export function isExplainTurnKind(kind: string | null | undefined): kind is ExplainTurnKind {
  return !!kind && TURN_KIND_SET.has(kind);
}

/** shortExplainId — kullanıcı turunda görünen kısa kimlik (`ora:` öneki korunur). */
export function shortExplainId(id: string): string {
  const pre = id.startsWith('ora:') ? 'ora:' : '';
  const body = pre ? id.slice(4) : id;
  return body.length > 8 ? `${pre}${body.slice(0, 8)}…` : id;
}

/** explainUserText — trace / exception açıklama turunun GÖRÜNEN kullanıcı metni (v0.10.1400). */
export function explainUserText(kind: 'trace' | 'exception', id: string, label?: string): string {
  if (kind === 'trace') return `Bu trace'i açıkla · ${shortExplainId(id)}`;
  const l = (label ?? '').trim();
  return `${l || 'Exception'} grubunu açıkla · ${shortExplainId(id)}`;
}

/** explainTurnText — v0.10.1405: her türün GÖRÜNEN kullanıcı metni. */
export function explainTurnText(s: AISubject, label?: string): string {
  switch (s.kind) {
    case 'trace':
    case 'exception':      return explainUserText(s.kind, s.id, label);
    case 'span':           return `Bu span'i açıkla · ${shortExplainId(s.spanId)}`;
    case 'problem':        return `Bu problemi açıkla · ${shortExplainId(s.id)}`;
    case 'incident':       return `Bu olayı açıkla · ${shortExplainId(s.id)}`;
    case 'anomaly':        return `Bu anomaliyi açıkla · ${shortExplainId(s.id)}`;
    case 'service-health': return `${s.id} sağlığını açıkla`;
    case 'runbook':        return `Bu problem için runbook çıkar · ${shortExplainId(s.id)}`;
    case 'log':            return `Bu log satırını açıkla · ${s.id}${s.pod ? ` · ${s.pod}` : ''}`;
    case 'endpoint':       return endpointAskQuestion(s);
    default:               return '';
  }
}

/** endpointWindow — v0.10.1405: endpoint sorusunun sabit penceresi (v0.10.1367 çekmece epWin'i). */
export function endpointWindow(s: Extract<AISubject, { kind: 'endpoint' }>): ChatSendWindow {
  return { rangeS: Math.max(60, Math.round((s.toNs - s.fromNs) / 1e9)), toMs: Math.floor(s.toNs / 1e6) };
}

/** Bir açıklama turunun gönderimi: görünen metin + (varsa) yapısal niyet + (endpoint) sabit pencere. */
export interface ExplainTurnIntent {
  userText: string;
  explainAsk?: ChatExplainAsk;
  window?: ChatSendWindow;
}

/**
 * explainAskFor — özne → {görünen metin, yapısal niyet}. Göç eden türlerin
 * hepsi (EXPLAIN_TURN_KINDS); charts / log-spike null (çekmecenin özne kipi).
 * Boş kimlik null. v0.10.1404 — `label`: görünen metindeki grup adı (CoSRE
 * başlangıcının exception çipi "ORDERS_GET_X · orders-api"); yoksa "Exception".
 * v0.10.1405 — span / service-health / log niyeti `subject` kodeğini taşır;
 * endpoint niyet taşımaz, `window` taşır.
 */
export function explainAskFor(subject: AISubject | null | undefined, src?: string, label?: string): ExplainTurnIntent | null {
  if (!subject || !isExplainTurnKind(subject.kind)) return null;
  const id = (subject.id ?? '').trim();
  if (!id) return null;
  const s = { ...subject, id } as AISubject;
  const userText = explainTurnText(s, label);
  if (s.kind === 'endpoint') return { userText, window: endpointWindow(s) };
  const kind = s.kind as ChatExplainKind;
  return {
    userText,
    explainAsk: { kind, id, ...(SIMPLE_KINDS.has(kind) ? {} : { subject: formatAiParam(s) }), ...(src ? { src } : {}) },
  };
}

/** explainTurnSendOpts — niyet → send'in ikinci argümanı (yalnız dolu alanlar). */
export function explainTurnSendOpts(x: ExplainTurnIntent): { explainAsk?: ChatExplainAsk; window?: ChatSendWindow } {
  return { ...(x.explainAsk ? { explainAsk: x.explainAsk } : {}), ...(x.window ? { window: x.window } : {}) };
}

/**
 * subjectOf — künye / niyet → özne. Kodek (subject) varsa ve türü tutuyorsa
 * ondan, basit türde {kind, id}'den; kurulamıyorsa null (bozuk arşiv satırı).
 */
function subjectOf(kind: string, id: string, subject?: string): AISubject | null {
  if (subject) {
    const s = parseAiParam(subject);
    if (s && s.kind === kind) return s;
  }
  if (!id || !SIMPLE_KINDS.has(kind)) return null;
  return { kind, id } as AISubject;
}

/** explainAskSubject — v0.10.1405: niyet (çip / başlangıç öğesi) → özne. */
export function explainAskSubject(a: ChatExplainAsk): AISubject | null {
  return subjectOf(a.kind, a.id, a.subject);
}

/** explainSubject — künye → çekmece öznesi (takip bağlamı / kodek / yedek çipler için). */
export function explainSubject(meta: Pick<ChatExplainMeta, 'kind' | 'id' | 'subject'>): AISubject | null {
  return subjectOf(meta.kind, meta.id, meta.subject);
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
  /** v0.10.1405 — künyeden çözülen özne (kodek / basit tür). */
  subject: AISubject;
}

export function activeExplainFrom(turns: readonly ChatTurn[]): ActiveExplain | null {
  for (let i = turns.length - 1; i >= 0; i--) {
    const t = turns[i];
    if (t.role !== 'assistant' || !t.explain || t.pending || t.error) continue;
    const text = (t.text ?? '').trim();
    if (!text) continue;
    const subject = explainSubject(t.explain);
    if (!subject) continue;
    return { key: `${i}:${t.explain.kind}:${t.explain.id}`, meta: t.explain, text, subject };
  }
  return null;
}

/** Takip bağlamı: çekmece özne kipindeki alanlar (+ servis / pencere). */
export interface ExplainFollowupContext {
  explain: string;
  subject: string;
  service?: string;
  rangeS?: number;
  toMs?: number;
}

/**
 * explainFollowupContext — takip sorularının bağlamı (çekmece özne kipindeki
 * AIDrawerChat ile AYNI alanlar): `explain` = künye + açıklama metni + kanıt
 * kimlikleri; `subject` = `?ai=` kodeği (sunucu ham kanıtı yeniden kurar).
 * v0.10.1405 — service-health / log ayrıca servisi (guided router varsayılanı),
 * service-health açıklanan pencereyi taşır (çekmecenin service kuralı).
 */
export function explainFollowupContext(a: ActiveExplain): ExplainFollowupContext {
  const s = a.subject;
  const out: ExplainFollowupContext = {
    explain: buildExplainContext({ subject: s, text: a.text, spanIds: a.meta.evidenceSpanIds, traceIds: a.meta.evidenceTraceIds }),
    subject: formatAiParam(s),
  };
  if (s.kind === 'service-health') {
    out.service = s.id;
    out.rangeS = Math.max(60, Math.round((s.toNs - s.fromNs) / 1e9));
    out.toMs = Math.floor(s.toNs / 1e6);
  } else if (s.kind === 'log') {
    out.service = s.id;
  }
  return out;
}

const clipLabel = (l: string): string => (l.length > 40 ? `${l.slice(0, 39)}…` : l);

/** explainContextLabel — bağlam şeridindeki öğe ("✨ trace 5b8efff7…"). */
export function explainContextLabel(meta: Pick<ChatExplainMeta, 'kind' | 'id' | 'label'>): string {
  const l = clipLabel((meta.label ?? '').trim());
  switch (meta.kind) {
    case 'trace':          return `✨ trace ${shortExplainId(meta.id)}`;
    case 'exception':      return `✨ ${meta.label || 'exception'} ${shortExplainId(meta.id)}`;
    case 'span':           return `✨ span ${l || shortExplainId(meta.id)}`;
    case 'problem':        return `✨ problem ${l || shortExplainId(meta.id)}`;
    case 'incident':       return `✨ olay ${l || shortExplainId(meta.id)}`;
    case 'anomaly':        return `✨ anomali ${l || shortExplainId(meta.id)}`;
    case 'service-health': return `✨ ${meta.id} sağlığı`;
    case 'runbook':        return `✨ runbook ${shortExplainId(meta.id)}`;
    case 'log':            return `✨ log ${meta.id}`;
    default:               return `✨ ${shortExplainId(meta.id)}`;
  }
}
