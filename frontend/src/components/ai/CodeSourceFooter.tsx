import { SafeHref } from '@/components/ui/SafeHref';
import type { AICodeContext } from '@/lib/types';
import { codeSourceRef } from '../codeSourceRef'; // v0.10.1044 — çalışan sürüm / dal

// CodeSourceFooter — v0.10.1406 (Tek CoSRE adım 3; operatör: "Trace ve exception
// üzerinden çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi,
// CoSRE bütünselliğine uymuyor."): "Kodu da incele" cevabının KAYNAK satırı —
// hangi depo/branş (ya da çalışan sürüm) okundu, hangi dosyalar. CopilotExplain'in
// iki kartında (ilk kart + "Kod incelemesi") birebir kopyaydı; sohbet balonu da
// (ChatBubble, kod turu) aynı bileşeni çizer — tek çizim.
//
// Depo çözümü bir TAHMİN olabilir (konvansiyon) — cevabın yanlış dosyaya
// dayandığından şüphelenen operatör bunu görmeden anlayamaz. Dosya yoksa hiç
// çizilmez (okunamama nedeni CodeStatusNote'ta).
export function CodeSourceFooter({ code }: { code: AICodeContext | null | undefined }) {
  if (!code || !code.files?.length) return null;
  return (
    <div data-code-source="" style={{ marginTop: 10, fontSize: 10.5, color: 'var(--text3)', lineHeight: 1.6 }}>
      <div>
        📄 Kaynak: <strong>{code.repo}</strong>
        {codeSourceRef(code)}
        {code.source === 'pin' ? ' · katalog pini' : code.source === 'convention' ? ' · ad konvansiyonu' : ''}
      </div>
      {/* v0.9.1236 — kod GELDİĞİNDE de bir not olabilir: depo adı sunucunun
          listesine bakılarak düzeltilmiş olabilir ya da bütçe dolduğu için
          pencereler kısalmış olabilir. */}
      {code.reason && (
        <div style={{ color: 'var(--warn, var(--text3))' }}>{code.reason}</div>
      )}
      {/* v0.10.973 — tablo standardı T5: tek monospace yığını. */}
      {code.files.map(f => (
        <div key={`${f.path}:${f.fromLine}`} style={{ fontFamily: 'var(--font-mono)' }}>
          {/* v0.10.353 (operatör) — dosya+satır DevOps'ta açılır (url sunucudan, FileURL). */}
          <SafeHref href={f.url} title={`DevOps'ta aç: ${f.path}${f.line ? ` satır ${f.line}` : ''}`}
            fallback={<>{f.path}:{f.fromLine}-{f.toLine}</>}>{f.path}:{f.fromLine}-{f.toLine} ↗</SafeHref>
          {/* v0.9.1254 — modelin ">>>" ile işaretlendiği satır; içerik tarayıcıya gitmez, numara yeter. */}
          {!!f.line && <span style={{ color: 'var(--text3)' }}> · hata satırı {f.line}</span>}
        </div>
      ))}
    </div>
  );
}
