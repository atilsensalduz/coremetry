// routingReportView — v0.10.1464: /ai "Şüpheli yönlendirmeler" panelinin saf
// yardımcıları. Neden adları Go'daki ai_routing_report.go reason* sabitleri;
// kademe etiketi ve arşiv bağı CoSRE etkileşimleri paneliyle aynı üretimden.

import type { RoutingReport } from '@/lib/types';
import { exchangeTierLabel, exchangeConversationHref } from './exchangeView';

const REASON_LABEL: Record<string, string> = {
  error: 'hata',
  empty: 'boş cevap',
  rag_no_evidence: "doküman 'bilgi yok'",
  loop_no_evidence: "döngü 'veri bulunamadı'",
  router_miss: "guided'a oturmadı",
  reask: 'hemen yeniden soruldu',
  no_evidence: 'kanıtsız cevap',
  aborted: 'yarıda bırakıldı',
};

const REASON_TITLE: Record<string, string> = {
  rag_no_evidence: 'Doküman (RAG) kademesi "bilgi yok" dedi ve yine de cevap verdi (v0.10.1446 sınıfı)',
  loop_no_evidence: 'Serbest araç döngüsü veri bulamadı (v0.10.1447 sınıfı)',
  router_miss: 'Telemetri şekilli soru guided rotasına oturmadı: serbest döngü, doküman, genel cevap ya da konu dışı',
  reask: 'Aynı konuşmada 10 dk içinde benzer soru ya da tekrar dene / yeniden üret / düzenle',
};

export function routingReasonLabel(kind: string): string {
  return REASON_LABEL[kind] ?? kind;
}

export function routingReasonTitle(kind: string): string | undefined {
  return REASON_TITLE[kind];
}

/** Kademe · rota (CoSRE etkileşimleri paneliyle aynı etiket). */
export function routingTierLabel(r: { tier: string; route?: string }): string {
  return exchangeTierLabel({ tier: r.tier, route: r.route ?? '' });
}

/** "arşivi aç" bağı (konuşma kimliği yoksa null). */
export function routingArchiveHref(r: { conversationId?: string }): string | null {
  return exchangeConversationHref({ conversationId: r.conversationId ?? '' });
}

/** Panelin özet satırı: "1 240 turdan 37'si şüpheli (%3)". */
export function routingSummary(rep: Pick<RoutingReport, 'turns' | 'suspicious' | 'truncated'>): string {
  const pct = rep.turns > 0 ? Math.round((rep.suspicious / rep.turns) * 100) : 0;
  const base = `${rep.turns.toLocaleString('tr-TR')} turdan ${rep.suspicious.toLocaleString('tr-TR')} tanesi şüpheli (%${pct})`;
  return rep.truncated ? `${base} — okuma tavanına ulaşıldı, yalnız en yeni turlar` : base;
}

/** Yalnız sapan değer renklenir: yüksek puan kırmızı, orta sarı. */
export function routingScoreTone(score: number): 'err' | 'warn' | undefined {
  if (score >= 5) return 'err';
  if (score >= 3) return 'warn';
  return undefined;
}
