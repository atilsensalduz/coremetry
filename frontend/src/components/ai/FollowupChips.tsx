// FollowupChips — v0.10.1291 (CoSRE follow-up D): cevap altındaki takip
// çipleri; CoSRE penceresi (CopilotChat) ve AI çekmecesi sohbeti
// (AIDrawerBody) AYNI bileşeni çizer.
//
// Öncelik: sunucunun kural-türevli `followups`'ı (araç sonuçlarından,
// internal/api/chat_followup_chips.go) → guided `suggestions` → yüzeyin statik
// listesi. Metin çipi tıklanınca metin yeni mesaj, `scope`/`command` yapısal
// kapsam olarak gider (ChatSendOpts.parsed — chatScope.ts). Aksiyon çipi
// (hipotezi doğrula) mesaj GÖNDERMEZ: mevcut verify çağrısı (editor+;
// sunucu viewer'a çipi hiç üretmez, uç ayrıca 403 döner).
//
// Öğrenme sinyali `followup_click` (cosrelearn, ağırlık 0.05): yalnız SUNUCU
// çiplerinde (followups / suggestions) — statik liste cevaba bağlı değildir.
// Sinyal yalnız ölçümdür; çalışma zamanı kararına okunmaz.
import { Chip } from '@/components/ui';
import { api } from '@/lib/api';
import type { ChatFollowup } from '@/lib/types';
import type { ChatSendOpts } from './useChatThread';
import { followupSendOpts } from './followupSend';
import { useSREHypothesisVerify } from './sreHypothesisVerify';
import { parseSREHypothesisAction, type SREHypothesisAction } from '@/lib/pageActions';
import { useNavigate } from 'react-router-dom';
import { safeAppPath } from '@/lib/safeAppPath';
import { useChatLinkNewTab } from './chatLinkTarget';

export interface FollowupChipsProps {
  followups?: ChatFollowup[];
  suggestions?: string[];
  fallback: string[];
  /** son cevabın exchangeId'si (sinyal); yoksa sinyal gönderilmez */
  exchangeId?: string;
  disabled?: boolean;
  onAsk: (text: string, opts?: ChatSendOpts) => void;
}

function ActionChip({ action, disabled, onClicked }: { action: SREHypothesisAction; disabled?: boolean; onClicked: () => void }) {
  const { state, run } = useSREHypothesisVerify(action);
  return (
    <Chip pill disabled={disabled || state !== 'idle'} title="Doğrulama koşusunu kuyruğa al (problem sayfasındaki AI SRE kartı)"
      onClick={() => { onClicked(); void run(); }}>
      🔎 {state === 'done' ? 'Doğrulama kuyrukta' : action.label}
    </Chip>
  );
}

// LinkChip — v0.10.1331: link çipi ("<KOD> grubunu açıkla" → grubun Exceptions
// sayfası + AI açıklaması). Mesaj göndermez; yol isSafeAppPath'ten geçmiştir.
// /cosre (kromsuz) sayfada diğer sohbet linkleri gibi yeni sekmede açılır.
function LinkChip({ href, text, disabled, onClicked }: { href: string; text: string; disabled?: boolean; onClicked: () => void }) {
  const navigate = useNavigate();
  const newTab = useChatLinkNewTab();
  return (
    <Chip pill disabled={disabled} title={href}
      onClick={() => { onClicked(); if (newTab) window.open(href, '_blank', 'noopener,noreferrer'); else navigate(href); }}>
      ↗ {text}
    </Chip>
  );
}

export function FollowupChips({ followups, suggestions, fallback, exchangeId, disabled, onAsk }: FollowupChipsProps) {
  const signal = (detail: string) => { if (exchangeId) api.aiSignal(exchangeId, 'followup_click', detail); };
  if (followups?.length) {
    return (
      <>
        {followups.map(f => {
          if (f.action) {
            // Aksiyon bloğuyla AYNI doğrulama (kapalı tür kümesi); bozuk yük → çip yok.
            const a = parseSREHypothesisAction(f.action);
            return a ? <ActionChip key={`a:${f.text}`} action={a} disabled={disabled} onClicked={() => signal(f.kind)} /> : null;
          }
          if (f.href !== undefined) {
            // v0.10.1331 — link çipi; güvensiz yol → çip yok (metin olarak da gönderilmez).
            const h = safeAppPath(f.href);
            return h ? <LinkChip key={`l:${f.text}`} href={h} text={f.text} disabled={disabled} onClicked={() => signal(f.kind)} /> : null;
          }
          // v0.10.1383 — kısa etiket (label) görünür, tam cümle (text) gönderilir + title'da.
          return (
            <Chip key={f.text} pill disabled={disabled} title={f.label ? f.text : undefined}
              onClick={() => { signal(f.kind); onAsk(f.text, followupSendOpts(f)); }}>
              {f.explainAsk ? '✨' : '↳'} {f.label || f.text}{/* v0.10.1400 — açıklama çipi sohbet içi tur */}
            </Chip>
          );
        })}
      </>
    );
  }
  const server = !!suggestions?.length;
  return (
    <>
      {(server ? suggestions ?? [] : fallback).map(q => (
        <Chip key={q} pill disabled={disabled} onClick={() => { if (server) signal('guided'); onAsk(q); }}>↳ {q}</Chip>
      ))}
    </>
  );
}
