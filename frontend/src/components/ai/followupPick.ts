// followupPick — v0.10.1515 (operatör 2026-10-24: "Alttaki chipler çok çıkıyor,
// gerek yok servis servis yazmasına. Sadece 'sağlığı analiz et' ve
// 'exception'ları' — bir servisin diyerek genel başlıklarda servis seçmesi
// sağlanabilir."). Takım cevabının SEÇİCİ çipinin saf yardımcıları (vitest
// node): sunucu (internal/api/chat_followup_chips.go followupPick) cümle
// şablonunu "{service}" yer tutucusuyla ve aday servisleri gönderir; çip tıkı
// satır içi servis seçicisini açar, seçim şablonu doldurup o cümleyi kapsam +
// seçilen servisle gönderir.
import type { ChatFollowup } from '@/lib/types';
import type { ChatSendOpts } from './useChatThread';

/** Şablondaki servis yer tutucusu (Go pickServicePlaceholder ile aynı). */
export const PICK_SERVICE_PLACEHOLDER = '{service}';

/** SAF — geçerli seçici yükü mü (tanınmayan tür / yer tutucusuz şablon → çip yok). */
export function isServicePick(f: ChatFollowup): boolean {
  const p = f.pick;
  return !!p && p.kind === 'service' && typeof p.template === 'string' && p.template.includes(PICK_SERVICE_PLACEHOLDER);
}

/** SAF — seçimle gönderilen cümle (Go fillPickTemplate ile aynı). */
export function fillPickTemplate(template: string, service: string): string {
  return template.split(PICK_SERVICE_PLACEHOLDER).join(service.trim());
}

/** SAF — gönderim seçenekleri: çipin kapsamı (env) + seçilen servis. */
export function pickSendOpts(f: ChatFollowup, service: string): ChatSendOpts {
  return { parsed: { scope: { ...(f.scope ?? {}), services: [service.trim()] } } };
}
