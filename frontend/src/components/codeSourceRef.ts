import type { AICodeContext } from '@/lib/types';

// codeSourceRef.ts — v0.10.1044 (operatör: "Kod, dalın ucundan değil çalışan
// sürümden okunsun"). "Kaynak:" satırının ref parçası — kodun HANGİ ref'ten
// okunduğu dürüstçe: çalışan sürümün commit'i mi, dalın ucu mu. İki çizim
// yeri (CopilotExplain) aynı metni bu tek fonksiyondan alır.
export function codeSourceRef(code: Pick<AICodeContext, 'version' | 'branch'>): string {
  if (code.version) return ` · ${code.version} (çalışan sürüm)`;
  if (code.branch) return ` · ${code.branch} (dal)`;
  return '';
}

// codeStatusLine — v0.10.1330 (operatör: stacktrace'siz trace'te "Kod
// incelemesi" kodsuz kalıyordu). Cevabın başındaki kod DURUM satırı, iki çizim
// yeri (CopilotExplain ilk kart + kod kartı) için TEK yerden:
//   - dosya yok → "⚠ Kod okunamadı — kodsuz analiz. (detay)"; operasyon yolu
//     koştuysa detay operasyonu ve neden okunamadığını söyler;
//   - kod ana operasyondan okunduysa → "stacktrace yok → operasyon kodu
//     üzerinden incelendi: <Dosya.java#L123 ↗> (operasyon: X, +N …)";
//   - stack penceresi geldiyse null (Kaynak satırı yeter).
//
// v0.10.1349 (operatör-bildirimli: HATASIZ trace'te Code Search açıkken de "Kod
// okunamadı … (bu kayıtta stacktrace yok)") — kesin operasyon yokken kod
// FUNCTION_CODE sözlüğünün ADAY operasyonundan okunabilir: satır "kesin değil"
// der (candidate); hiç ad yoksa / adaylar depoda yoksa bunu açıkça söyler.
export type CodeStatusLine =
  | { kind: 'missing'; detail: string }
  | { kind: 'operation'; operation: string; others: number; candidate?: boolean; file?: { label: string; url?: string; title: string } };

const NO_STACK = 'bu kayıtta stacktrace yok';

export function codeStatusLine(code: AICodeContext | null | undefined): CodeStatusLine | null {
  if (!code) return null;
  const files = code.files ?? [];
  const op = code.operation ?? '';
  const candidate = code.operationSource === 'function-code';
  if (!files.length) {
    if (code.source === 'operation' && code.operationLookup === 'none') {
      return { kind: 'missing', detail: `${NO_STACK}; trace'in operasyon adı bulunamadı` };
    }
    const cands = code.operationCandidates ?? [];
    if (candidate && cands.length && code.operationLookup === 'failed') {
      // Sunucu gerekçesi zaten "bu kayıtta stacktrace yok; …adayları…" ise tekrar edilmez.
      const why = code.reason && !code.reason.startsWith(NO_STACK) ? `: ${code.reason}` : '';
      return { kind: 'missing', detail: `${NO_STACK}; fonksiyon kodu adayları ${cands.join(', ')} için depoda kod bulunamadı${why}` };
    }
    if (op && code.operationLookup === 'off') {
      return { kind: 'missing', detail: 'bu kayıtta stacktrace yok — operasyon kodu araması için Code Search kapalı' };
    }
    if (op && code.operationLookup === 'failed') {
      const why = code.reason ? `: ${code.reason}` : '';
      return { kind: 'missing', detail: `bu kayıtta stacktrace yok; operasyon ${op} için depoda kod bulunamadı${why}` };
    }
    return { kind: 'missing', detail: code.reason ?? '' };
  }
  if (!op || code.source !== 'operation') return null;
  const entry = files.find(f => f.kind === 'operation') ?? files[0];
  const base = entry.path.split('/').pop() ?? entry.path;
  return {
    kind: 'operation',
    operation: op,
    others: code.otherOperations?.length ?? 0,
    ...(candidate ? { candidate: true } : {}),
    file: { label: `${base}#L${entry.fromLine}`, url: entry.url, title: `DevOps'ta aç: ${entry.path} satır ${entry.fromLine}` },
  };
}

/** codeStatusLead — satırın başı: kesin operasyon ya da (v0.10.1349) fonksiyon kodu ADAYI. */
export function codeStatusLead(s: { candidate?: boolean }): string {
  return s.candidate
    ? 'stacktrace yok → fonksiyon kodu ADAYININ kodu üzerinden incelendi (kesin değil):'
    : 'stacktrace yok → operasyon kodu üzerinden incelendi:';
}

/** codeStatusOperationTail — "(operasyon: X, +N diğer operasyon okunmadı)"; aday: "(operasyon adayı: X, +N aday okunmadı)". */
export function codeStatusOperationTail(s: { operation: string; others: number; candidate?: boolean }): string {
  if (s.candidate) return ` (operasyon adayı: ${s.operation}${s.others > 0 ? `, +${s.others} aday okunmadı` : ''})`;
  return ` (operasyon: ${s.operation}${s.others > 0 ? `, +${s.others} diğer operasyon okunmadı` : ''})`;
}
