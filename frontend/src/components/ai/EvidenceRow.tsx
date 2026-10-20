import { Button } from '@/components/ui/Button';

// Kanıt satırı — sayfadaki kutulanmış satırla AYNI görsel dil (.wf-evidence),
// böylece çekmecedeki liste ile exception'ın kutulu örnek-trace satırı aynı
// şeyi anlatır. v0.10.1033 — yalnız "Kanıt trace'leri" kullanır (span listesi
// kaldırıldı; span kanıtı waterfall'daki kutunun kendisi).
// v0.10.924 — buton bütünlüğü Faz 2: `div role=button` + elle Enter/Space →
// gerçek ghost Button (klavye yerleşik). Atom çocukları `.row` flex'ine
// sardığı için kırpma (ellipsis) id'nin kendi span'inde.
// v0.10.1400 — AIDrawerBody'den taşındı: sohbet içi açıklama turu (ChatBubble)
// da aynı satırı çizer.
export function EvidenceRow({ id, title, onClick }: { id: string; title: string; onClick: () => void }) {
  return (
    <Button variant="ghost" size="sm" className="wf-evidence mono"
      title={title}
      onClick={onClick}
      rightIcon={<span style={{ color: 'var(--text3)' }}>→</span>}
      style={{ display: 'block', width: '100%', marginBottom: 4 }}>
      <span style={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{id}</span>
    </Button>
  );
}
