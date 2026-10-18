import { SafeHref } from '@/components/ui/SafeHref';
import type { AICodeContext } from '@/lib/types';
import { codeStatusLine, codeStatusOperationTail } from './codeSourceRef';

// CodeStatusNote — v0.10.1330: kod incelemesinin DURUM satırı (CopilotExplain'in
// iki kartı aynı bileşeni çizer). Metin codeStatusLine'dan; burada yalnız çizim.
export function CodeStatusNote({ code }: { code: AICodeContext | null | undefined }) {
  const s = codeStatusLine(code);
  if (!s) return null;
  if (s.kind === 'missing') {
    return (
      <div style={{ fontSize: 11, color: 'var(--warn, var(--text3))', marginBottom: 8, display: 'flex', gap: 6, alignItems: 'flex-start' }}>
        <span>⚠</span>
        <span>Kod okunamadı — <strong>kodsuz</strong> analiz.{s.detail ? ` (${s.detail})` : ''}</span>
      </div>
    );
  }
  return (
    <div style={{ fontSize: 11, color: 'var(--text3)', marginBottom: 8 }}>
      stacktrace yok → operasyon kodu üzerinden incelendi:{' '}
      {s.file && (
        <SafeHref href={s.file.url} title={s.file.title} style={{ fontFamily: 'var(--font-mono)' }}
          fallback={<span style={{ fontFamily: 'var(--font-mono)' }}>{s.file.label}</span>}>{s.file.label} ↗</SafeHref>
      )}
      {codeStatusOperationTail(s)}
    </div>
  );
}
