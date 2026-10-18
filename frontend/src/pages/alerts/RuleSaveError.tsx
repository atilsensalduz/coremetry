import { appLinkKind } from '@/lib/safeAppPath';

// RuleSaveError — v0.10.1341: /alerts kural formunun kaydetme hatası.
//
// Sunucu kuralı 400 ile reddedebilir (runbookUrl kapısı, hedef/bildirim
// doğrulaması, ölü log sorgusu). Eskiden `save` reddi yakalamıyordu: form
// açık kalıyor, hata yalnız konsola düşüyordu. Hata formun footer'ının
// üstünde satır içi gösterilir, form açık kalır.
//
// Alerts.tsx'e dokunuş bilinçli olarak küçük (dosya başka bir dilimin):
// hook (useRuleSaveError.ts) + iki bileşen burada, sayfa yalnız çağırıyor.

export function RuleSaveError({ error }: { error: string | null }) {
  if (!error) return null;
  return (
    <div role="alert" className="field-error" data-rule-save-error
      style={{ marginTop: 10 }}>
      {error}
    </div>
  );
}

/** Runbook alanının altında ön-kontrol ipucu: sunucunun (safelink) kabul
 *  etmeyeceği bir değer kaydetmeden önce işaretlenir. Boş ya da geçerli → yok. */
export function RunbookUrlHint({ url }: { url?: string }) {
  const v = (url ?? '').trim();
  if (!v || appLinkKind(v)) return null;
  return (
    <div className="field-error" data-runbook-hint>
      Must be an absolute http(s) URL or an app path such as /runbook?id=…
    </div>
  );
}
