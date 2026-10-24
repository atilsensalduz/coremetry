import { useState } from 'react';
import { copyToClipboard } from '@/lib/clipboard';
import { Button } from '@/components/ui/Button';

/**
 * Tiny clipboard button. Renders a small icon next to copyable text;
 * click → write to clipboard, briefly flips to a check mark.
 *
 * v0.8.550 — the hand-rolled fallback moved to lib/clipboard. Behaviour
 * gains one thing: the shared helper also falls back when writeText
 * REJECTS (permission denied, document not focused), where this copy only
 * fell back when the API was missing and swallowed a rejection into a
 * flash-less no-op.
 */
// v0.10.1369 — `copiedTitle` (isteğe bağlı): kopyalandıktan sonraki ~1.5 sn
// title + aria-label (ör. "Kopyalandı"); verilmezse davranış bugünkü.
// v0.10.1514 (trace başlığı A) — `cell`: kimlik çipinin (ui/IdentityChip) kopyala
// HÜCRESİ. Satır-içi yerleşim yok (hücre CSS'i `.idchip__copy` ölçer) ve
// erişilebilir ad `title`ın kendisi ("Operasyon adını kopyala"): üç çip yan
// yana dururken üç tane "Copy" okumak hangisinin ne kopyaladığını söylemez.
export function CopyButton({ value, title, copiedTitle, cell }: { value: string; title?: string; copiedTitle?: string; cell?: boolean }) {
  const [copied, setCopied] = useState(false);

  const onClick = async (e: React.MouseEvent) => {
    e.stopPropagation();   // don't trigger the row click underneath
    e.preventDefault();
    if (await copyToClipboard(value)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  };

  // v0.10.924 — buton bütünlüğü Faz 2: `.copy-btn` → ghost xs Button.
  // Kopyalandı tonu evin `is-ok` değiştiricisi (ChatBubble'ın ⧉/✓'ü ile aynı):
  // `button.ghost` hover'ından SONRA bildirildiği için imleç üstündeyken de
  // yeşil kalır. Satır-içi yalnız eski `.copy-btn`in yerleşimi (margin/hiza)
  // ve glif ölçüsü (12px, line-height 1): ID/hash satırlarındaki ~25 çağrı
  // yerinde satır yüksekliği ve glif boyu değişmesin.
  if (cell) {
    const name = copied && copiedTitle ? copiedTitle : (title ?? 'Copy');
    return (
      <Button variant="ghost" size="xs" onClick={onClick} title={name} aria-label={name}
        className={copied ? 'copy-btn idchip__copy is-ok' : 'copy-btn idchip__copy'}>
        {copied ? '✓' : '⧉'}
      </Button>
    );
  }
  return (
    <Button variant="ghost" size="xs"
      onClick={onClick}
      title={copied && copiedTitle ? copiedTitle : (title ?? (copied ? 'Copied!' : 'Copy to clipboard'))}
      className={copied ? 'copy-btn is-ok' : 'copy-btn'}
      aria-label={copied && copiedTitle ? copiedTitle : 'Copy'}
      style={{ marginLeft: 4, verticalAlign: 'middle', fontSize: 12, lineHeight: 1 }}
    >
      {copied ? '✓' : '⧉'}
    </Button>
  );
}
