import { useState } from 'react';
import { api } from '@/lib/api';
import type { AIFeedbackReason } from '@/lib/types';
import { FEEDBACK_REASONS } from '@/lib/aiFeedbackReasons';
import { Button } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { IconButton } from '@/components/ui/IconButton';

// AIFeedbackButtons — tek-atış ✨ Explain cevaplarının 👍/👎 rayı
// (v0.9.1119 backend'i, Faz 0.3b).
//
// NEDEN PAYLAŞILAN BİLEŞEN: aynı üç satırlık iyimser-güncelleme +
// geri-alma mantığı depoda ZATEN üç kez yazılmıştı (ChatBubble.rateTurn,
// RCAVerdictPanel.rateVerdict, AIAnalysisPanel.rateAnalysis). Dördüncü
// ile sekizinci kopyayı yazmak yerine tek atom: sekiz yüzey aynı
// davranışı paylaşır, sözleşme tek yerde ayrışmadan durur.
//
// SÖZLEŞME (üçü de v0.9.592'nin dersi):
//  1. Kimlik yoksa AFFORDANCE HİÇ ÇİZİLMEZ. Cevabını kaydedemeyeceğimiz
//     bir soruyu sormak — "yararlı mıydı?" deyip hiçbir yere yazmamak —
//     bu rayın var olma sebebi olan ÖLÜ affordance hatasının ta kendisi.
//     `exchangeId` omitempty: sunucu-cache'li (explain-charts) ya da eski
//     sürümden gelen bir gövdede alan HİÇ gelmez.
//  2. İyimser güncelleme + hata hâlinde GERİ ALMA. Başarısız bir POST'tan
//     sonra seçili kalmak, "kaydedildi" yalanının daha sessiz biçimi.
//  3. Aynı oya ikinci tık NO-OP; ters oya tık YENİDEN POST eder (sunucu
//     exchangeId'ye göre değiştirir, çift satır yazmaz).
//
// Seçili durum `IconButton active` ile: `aria-pressed` + `.active` tint.
// v0.9.891'de opacity+borderColor ile ELLE çizilen seçili durum tam da
// bu yüzden emekli edilmişti — durum yalnız GÖRÜLMEMELİ, DUYURULMALI da.
//
// Oy, exchangeId'ye BAĞLI tutuluyor: "Yeniden sor" taze bir kimlik
// üretir ve eski cevabın oyu yeni cevabın üstünde kalamaz.
//
// v0.10.1280 (CoSRE adım 8) — `callId`: cevabı üreten ai_calls satırı
// (sohbetin answer.callId'si). Verilirse HER POST onu taşır → oy somut
// çağrıya bağlanır; verilmezse gövde bayt bayt eski (exchange düzeyinde oy;
// ✨ Explain yüzeyleri ve eski sunucu). 👎 panelinde hazır neden çipleri
// (yanlış / eksik / alakasız): tık neden + oyu birlikte gönderir, seçili
// çipe ikinci tık nedeni temizler (''); yorum kutusu aynen yerinde.
export function AIFeedbackButtons({ exchangeId, callId }: { exchangeId?: string; callId?: string }) {
  const [rated, setRated] = useState<{ xid: string; verdict: 1 | -1 } | null>(null);
  // Türetilmiş: kimlik değiştiğinde sıfırlama efekti GEREKMEZ (ve
  // efektle sıfırlamak bir render geciktirir — eski oy bir kare boyunca
  // yeni cevabın altında görünürdü).
  const verdict = rated && rated.xid === exchangeId ? rated.verdict : null;
  // v0.9.1193 (Faz 5.1) — 👎'nin YORUMU. Kimliğe bağlı tutulur (oyla aynı
  // gerekçe: "Yeniden sor" taze kimlik üretir, eski cevabın yorumu yeni
  // cevabın altına taşamaz). `sent` gönderilen metni tutar — kutu
  // "kaydedildi" der ve yeniden düzenlemeye izin verir.
  const [note, setNote] = useState<{ xid: string; text: string; sent: string | null }>({ xid: '', text: '', sent: null });
  const noteText = note.xid === exchangeId ? note.text : '';
  const noteSent = note.xid === exchangeId ? note.sent : null;
  // v0.10.1280 — seçili hazır neden; kimliğe bağlı (oy/yorumla aynı gerekçe).
  const [picked, setPicked] = useState<{ xid: string; reason: AIFeedbackReason } | null>(null);
  const reason = picked && picked.xid === exchangeId ? picked.reason : null;

  if (!exchangeId) return null;
  // callId yalnız VARSA gövdeye girer: alanı hiç göndermemek sunucuda
  // saklanan bağı korur (preserve), eski gövdeyle bayt bayt aynı kalır.
  const call = callId ? { callId } : {};

  const rate = (v: 1 | -1) => {
    if (verdict === v) return; // aynı oya ikinci tık: no-op
    const prior = verdict;
    setRated({ xid: exchangeId, verdict: v });
    // Yorum alanı BİLEREK gönderilmiyor: alan yoksa sunucu saklananı
    // korur (preserve sözleşmesi). 👎→👍 flip'i yazılmış yorumu silmez.
    api.postAIFeedback({ exchangeId, verdict: v, ...call }).catch(() => {
      setRated(prior ? { xid: exchangeId, verdict: prior } : null);
    });
  };

  const sendNote = () => {
    const text = noteText.trim();
    if (!text || verdict !== -1) return;
    // Yorum, oyla BİRLİKTE tam gövde olarak gider (tam-satır replace):
    // verdict -1 zaten seçili, tekrar göndermek sunucuda değişiklik değil.
    api.postAIFeedback({ exchangeId, verdict: -1, comment: text, ...call })
      .then(() => setNote({ xid: exchangeId, text, sent: text }))
      .catch(() => setNote({ xid: exchangeId, text, sent: null }));
  };

  // v0.10.1280 — hazır neden: seçili çipe ikinci tık temizler (''). İyimser +
  // hata hâlinde önceki seçime dönüş (oy rayının sözleşmesi).
  const pickReason = (r: AIFeedbackReason) => {
    if (verdict !== -1) return;
    const prior = picked;
    const next = reason === r ? null : r;
    setPicked(next ? { xid: exchangeId, reason: next } : null);
    api.postAIFeedback({ exchangeId, verdict: -1, reason: next ?? '', ...call })
      .catch(() => setPicked(prior));
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 8 }}>
      <div style={{ display: 'inline-flex', gap: 2, alignItems: 'center' }}>
        <IconButton variant="ghost" size="sm" active={verdict === 1}
          onClick={() => rate(1)} tooltip="Faydalı"
          aria-label="Cevabı faydalı işaretle" icon="👍" />
        <IconButton variant="ghost" size="sm" active={verdict === -1}
          onClick={() => rate(-1)} tooltip="Faydasız"
          aria-label="Cevabı faydasız işaretle" icon="👎" />
      </div>
      {/* Kutu yalnız 👎 SEÇİLİYKEN: 👍'ye neden sormak anket olurdu; 👎'nin
          nedeni ise /ai madenciliğinin asıl sinyali. Opsiyonel — kutuyu boş
          bırakmak oyu geçersiz kılmaz. */}
      {verdict === -1 && (
        <div role="group" aria-label="Faydasız oyunun hazır nedeni"
          style={{ display: 'inline-flex', gap: 4, alignItems: 'center', flexWrap: 'wrap' }}>
          {FEEDBACK_REASONS.map(r => (
            <Chip key={r.value} size="xs" pill active={reason === r.value}
              title={r.title} onClick={() => pickReason(r.value)}>
              {r.label}
            </Chip>
          ))}
        </div>
      )}
      {verdict === -1 && (
        <div style={{ display: 'flex', gap: 6, alignItems: 'flex-start', maxWidth: 420 }}>
          <textarea
            value={noteText}
            onChange={e => setNote({ xid: exchangeId, text: e.target.value, sent: noteSent })}
            placeholder="Neden faydasızdı? (opsiyonel — /ai negatif paneline düşer)"
            rows={2}
            maxLength={2000}
            aria-label="Faydasız oyunun nedeni"
            style={{ flex: 1, fontSize: 11.5, resize: 'vertical' }} />
          <Button variant="secondary" size="sm"
            disabled={!noteText.trim() || noteSent === noteText.trim()}
            onClick={sendNote}>
            {noteSent === noteText.trim() && noteSent !== null ? 'Kaydedildi' : 'Gönder'}
          </Button>
        </div>
      )}
    </div>
  );
}
