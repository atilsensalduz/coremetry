import { describe, it, expect } from 'vitest';
import { routingReasonLabel, routingReasonTitle, routingTierLabel, routingArchiveHref, routingSummary, routingScoreTone } from './routingReportView';

describe('routingReportView (v0.10.1464)', () => {
  it('neden etiketleri; bilinmeyen aynen', () => {
    expect(routingReasonLabel('rag_no_evidence')).toBe("doküman 'bilgi yok'");
    expect(routingReasonLabel('router_miss')).toBe("guided'a oturmadı");
    expect(routingReasonLabel('yeni')).toBe('yeni');
    expect(routingReasonTitle('reask')).toContain('10 dk');
    expect(routingReasonTitle('error')).toBeUndefined();
  });
  it('kademe · rota etiketi etkileşim paneliyle aynı', () => {
    expect(routingTierLabel({ tier: 'guided', route: 'outbound_call' })).toBe('Guided · outbound_call');
    expect(routingTierLabel({ tier: 'rag' })).toBe('Doküman (RAG)');
  });
  it('arşiv bağı yalnız konuşma kimliğiyle', () => {
    expect(routingArchiveHref({ conversationId: 'c 1' })).toBe('/cosre?chat=c%201');
    expect(routingArchiveHref({})).toBeNull();
  });
  it('özet ve tavan notu', () => {
    expect(routingSummary({ turns: 200, suspicious: 7, truncated: false })).toBe('200 turdan 7 tanesi şüpheli (%4)');
    expect(routingSummary({ turns: 0, suspicious: 0, truncated: false })).toBe('0 turdan 0 tanesi şüpheli (%0)');
    expect(routingSummary({ turns: 20000, suspicious: 10, truncated: true })).toContain('okuma tavanına ulaşıldı');
  });
  it('yalnız sapan puan renklenir', () => {
    expect(routingScoreTone(6)).toBe('err');
    expect(routingScoreTone(3)).toBe('warn');
    expect(routingScoreTone(2)).toBeUndefined();
  });
});
