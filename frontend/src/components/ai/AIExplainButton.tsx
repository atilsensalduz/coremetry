import { useState, type ReactNode } from 'react';
import { Button, type ButtonProps } from '@/components/ui/Button';
import { IconSparkles } from '@/components/icons';
import { formatAiParam, type AISubject } from '@/lib/aiSubject';
import { useAiSubject } from './useAiSubject';
import { useCopilotEnabled } from './useCopilotEnabled';

// AIExplainButton — TEK "✨ Explain" affordance'ı (v0.9.477, onaylı
// AI-drawer mockup'ı). Eskiden her yüzey kendi satır-içi açıklama panelini
// gömüyor, cevap butonun ALTINDA açılıyordu: aynı içerik sekiz farklı
// genişlikte, sayfayı iterek. Artık buton yalnız adrese `?ai=<kind>:<id>`
// yazar; CoSRE sohbeti (CopilotChat) onu tek seferlik niyet olarak tüketip
// açıklamayı bir sohbet turu olarak gönderir (v0.10.1400–1407).
//
// Korunanlar:
//   • copilot kapalıysa buton HİÇ görünmez (eski self-hide davranışı),
//   • ilk kullanıma dek nabız atar (v0.9.409, operatör isteği),
//   • Button atomu + variant="accent" (tek tasarım dili).

// v0.10.1342 (K3 dilim B) — ağırlık basamağı bir ARAMA TABLOSU, `strong ?
// 'primary' : …` üçlüsü değil. `strong` bir durum anahtarı değil, çağrı
// yerinde beyan edilen vurgu; buttonStateRatchet koşullu-birincil deseninin
// sıfırda kalmasını çiviliyor. Görsel sonuç aynı: strong = sayfanın tek dolu
// birincili (md), normal = accent tint (sm).
const EMPHASIS_LOOK = {
  normal: { variant: 'accent',  size: 'sm' },
  strong: { variant: 'primary', size: 'md' },
} as const satisfies Record<'normal' | 'strong', { variant: ButtonProps['variant']; size: NonNullable<ButtonProps['size']> }>;

export function AIExplainButton({ subject, label, size, emphasis = 'normal', title, className }: {
  subject: AISubject;
  label?: ReactNode;
  // 'xs' (v0.9.1033): kart başlığındaki mini ✨ — ikon-only etiketle
  // kullanılır, satır yüksekliğini büyütmez.
  size?: 'xs' | 'sm' | 'md';
  // emphasis (v0.9.1166, operatör: "Explain trace butonu daha belirgin
  // olsun"). Ağırlık ARTIK ÇAĞRI YERİNDE beyan edilir, çünkü tek bir
  // varsayılan iki farklı işi taşıyamıyordu: kart başlığındaki ✨ satır
  // yüksekliğini büyütmemeli, sayfanın TEK ana eylemi ise göze çarpmalı.
  // Serbest `variant` prop'u AÇMIYORUZ — her yüzey kendi ağırlığını
  // seçerse atomun varlık nedeni (tek affordance görünümü) biter; iki
  // isimli basamak denetlenebilir kalır.
  //   normal → accent + sm (tüm mevcut çağrılar; davranış değişmedi)
  //   strong → primary + md (dolu aksan; grubun tek birincili olmalı)
  emphasis?: 'normal' | 'strong';
  title?: string;
  className?: string;
}) {
  const enabled = useCopilotEnabled();
  const [ai, setAi] = useAiSubject();
  // Nabız "bu mount'ta hiç kullanılmadı" demek; çekmece bu özneyle
  // açıldıysa da susar (operatör zaten bulmuş).
  const [used, setUsed] = useState(false);
  if (enabled !== true) return null;

  const key = formatAiParam(subject);
  const open = ai !== null && formatAiParam(ai) === key;
  const quiet = used || open;

  const look = EMPHASIS_LOOK[emphasis];
  // v0.10.1400 (Tek CoSRE) — trace / exception: `?ai=` tek seferlik niyet;
  // CopilotChat onu tüketip açıklamayı SOHBET TURU olarak gönderir (çekmece
  // özne kipi yok; adres anında temizlenir, ikinci tık yeni tur = önbellek
  // isabeti).
  // v0.10.1405 (Tek CoSRE adım 2; operatör: "Trace ve exception üzerinden
  // çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE
  // bütünselliğine uymuyor.") — tık HER ZAMAN özneyi yazar: aç/kapa
  // geçişi ve aria-expanded kalktı (düğme bir çekmeceyi açıp kapatmıyor,
  // sohbete bir tur gönderiyor). Adres replace:true (useAiSubject).
  // v0.10.1407 — charts / log-spike da: HER tür bir sohbet turu (özne kipi silindi).
  return (
    <Button variant={look.variant}
      size={size ?? look.size}
      title={title ?? 'CoSRE sohbetinde açıkla'}
      className={[quiet ? undefined : 'ai-attn', className].filter(Boolean).join(' ') || undefined}
      onClick={() => { setUsed(true); setAi(subject); }}>
      {label ?? <><IconSparkles /> <span>AI explain</span></>}
    </Button>
  );
}
