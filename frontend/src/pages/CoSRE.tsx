import { useEffect } from 'react';
import { CopilotChat } from '@/components/CopilotChat';
import { setDocumentTitleOverride } from '@/lib/branding';

// CoSRE — v0.10.1125: `/cosre` bağımsız sohbet sayfası (yer imine eklenebilir,
// tek başına açılabilir). Kabuk dalı AppShell'de (lib/cosrePage.ts): sidebar
// ve uygulama kromu yok. Sohbetin KENDİSİ çekmeceyle aynı bileşen —
// CopilotChat `variant="page"` (çatal yok; başlık + gövde tek yerde kurulur).
//
// Sekme başlığı "CoSRE"; favicon zaten /favicon.svg (CoSRE markası da o).
// v0.10.1340 — başlık lib/branding.ts'in TAM geçersiz kılmasıyla yazılır:
// markalama fetch'i sayfa açıldıktan SONRA çözülse de (applyBranding) "CoSRE"
// kalır — eski `document.title = 'CoSRE'` + brandTitle bağımlılığı varsayılan
// markada yeniden koşmuyor, sekme "Coremetry"ye dönüyordu. Çıkışta geçersiz
// kılma kalkar → sayfa/marka bileşimi (Topbar'lı sayfa kendi başlığını yazar).
export default function CoSRE() {
  useEffect(() => {
    setDocumentTitleOverride('CoSRE');
    return () => setDocumentTitleOverride(null);
  }, []);
  return <CopilotChat variant="page" />;
}
