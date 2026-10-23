import type { ChatMessage, ChatTurn } from '@/lib/types';

// chatPersist — konuşma kalıcılığının SAF yarısı (v0.9.1139, AI Faz 4.1).
//
// Sunucu tarafı saved_views(page='ai-chat') blob'u
// (internal/api/ai_conversations.go); burada yalnız "ekrandaki turlar ↔
// arşivlenen mesajlar" dönüşümü yaşıyor. Ayrı dosya çünkü bu üç
// fonksiyon useChatThread'in React'ine hiç dokunmuyor ve test edilmesi
// gereken davranışın tamamı burada.

// PERSIST_MESSAGE_CAP — A1 onayının "son 40 mesaj" tavanı. Sunucu bunu
// ZATEN uyguluyor (fitChatBlob) ve orada duran tavan gerçek duvar;
// buradaki kopya yalnız tele gereksiz byte koymamak için.
export const PERSIST_MESSAGE_CAP = 40;

// PERSIST_DEBOUNCE_MS — bir alışveriş bitip kaydetmenin tetiklenmesi
// arasındaki gecikme. İki iş yapıyor:
//   1. `answer` + `done` olayları peş peşe geliyor; tek yazım yeter;
//   2. kaydetme `send`in finally'sinde tetikleniyor ve o an son turun
//      React state'i henüz ref'e YANSIMAMIŞ olabilir. Gecikme, yüklemi
//      render sonrası okumayı garanti eder.
export const PERSIST_DEBOUNCE_MS = 600;

/**
 * persistMessages — ekrandaki turlardan arşivlenecek mesajlar.
 *
 * Düşenler: hata veren turlar (bir "sunucuya ulaşamadım" satırı
 * geçmişte hiçbir işe yaramaz), hâlâ akan (pending) tur ve boş metin.
 * Kalanlar son 40'a kırpılır.
 */
export function persistMessages(turns: ChatTurn[]): ChatMessage[] {
  const keep = turns
    .filter(t => !t.error && !t.pending && (t.text ?? '').trim() !== '')
    // v0.10.1272 — asistan cevabının kanıt çipleri de arşive gider (yalnız varsa:
    // kanıtsız mesaj bayt-bayt {role,text} kalır).
    // v0.10.1400 — açıklama turunun künyesi de (yalnız asistan, yalnız varsa).
    .map<ChatMessage>(t => ({
      role: t.role, text: t.text ?? '',
      ...(t.role === 'assistant' && t.evidence?.length ? { evidence: t.evidence } : {}),
      ...(t.role === 'assistant' && t.explain ? { explain: t.explain } : {}),
      // v0.10.1444 — turun kimliği (sunucu blok zarfını bununla bulur). Bloklar
      // ASLA istemciden gitmez (64 KB blob; sunucu zaten düşürür). Geri yüklenen
      // tur kimliğini archiveExchangeId'de taşır → yeniden kaydetmede kaybolmaz.
      ...(t.role === 'assistant' && turnArchiveId(t) ? { exchangeId: turnArchiveId(t) } : {}),
    }));
  return keep.length > PERSIST_MESSAGE_CAP ? keep.slice(-PERSIST_MESSAGE_CAP) : keep;
}

/**
 * restoreTurns — arşivden gelen mesajlar → çizilebilir turlar.
 *
 * Geri yüklenen asistan turları exchangeId TAŞIMAZ, dolayısıyla 👍/👎
 * satırı görünmez. Bu bilinçli: geri bildirim CANLI bir alışverişe
 * (ai_calls satırı) anahtarlanıyor; arşivden gelen bir cevaba oy vermek
 * ölü bir affordance olurdu (v0.9.592 dersi).
 *
 * v0.10.1444 — sunucunun doldurduğu bloklar (charts_signals / log_spike /
 * metric_panel / servis raporu / kanıt kartları) turla gelir; ChatBubble
 * canlı turdaki gibi kartı çizer. Kimlik `archiveExchangeId`de (exchangeId
 * yine boş — yukarıdaki sözleşme).
 */
export function restoreTurns(messages: ChatMessage[] | undefined): ChatTurn[] {
  if (!messages?.length) return [];
  return messages
    .filter(m => (m.role === 'user' || m.role === 'assistant') && (m.text ?? '').trim() !== '')
    .map<ChatTurn>(m => ({
      role: m.role, text: m.text ?? '',
      ...(m.role === 'assistant' && m.evidence?.length ? { evidence: m.evidence } : {}), // v0.10.1272 — geri yüklenen kanıt çipleri
      ...(m.role === 'assistant' && m.explain ? { explain: m.explain } : {}), // v0.10.1400 — açıklama yine yapısal çizilir
      ...(m.role === 'assistant' && m.exchangeId ? { archiveExchangeId: m.exchangeId } : {}), // v0.10.1444
      ...(m.role === 'assistant' && m.blocks?.length ? { blocks: m.blocks } : {}),
      ...(m.role === 'assistant' && m.blocks?.length && m.blocksAt ? { blocksAt: m.blocksAt } : {}),
      ...(m.role === 'assistant' && m.blocksDropped?.length ? { blocksDropped: m.blocksDropped } : {}),
    }));
}

/** turnArchiveId — v0.10.1444: arşive yazılacak tur kimliği (turnId ?? exchangeId ?? archiveExchangeId). */
export function turnArchiveId(t: ChatTurn): string | undefined {
  return t.turnId || t.exchangeId || t.archiveExchangeId || undefined;
}

/**
 * hasCompletedExchange — kaydetmeye DEĞER mi?
 *
 * En az bir kullanıcı sorusu VE tamamlanmış bir asistan cevabı şart:
 * iptal edilen ya da hata veren ilk tur yüzünden başlıksız bir thread
 * satırı açmak, geçmiş listesini boş kabuklarla doldururdu.
 */
export function hasCompletedExchange(turns: ChatTurn[]): boolean {
  const msgs = persistMessages(turns);
  return msgs.some(m => m.role === 'user') && msgs.some(m => m.role === 'assistant');
}
