// logsQuery — v0.10.1498 (/logs Kibana-Discover yeniden tasarımı, dilim 1).
//
// Sorgu çubuğunun iki SAF kararı, DOM'suz test edilebilsin diye burada:
//   • isDraftDirty — taslak uygulanan süzgeçten farklı mı. Search ile ↻ TEK
//     düğmede birleşti (Kibana "Update" ⇄ "Refresh"): taslak = uygulanan iken
//     düğme "↻ HH:MM" (pencereyi şimdiye taşır), aksi hâlde "Search" (apply).
//     Oto-uygulanan alanlar (severity, hasTrace, pattern, team) taslağa da
//     yazıldığı için karşılaştırma tüm alanlar üzerinden güvenle yapılır.
//   • bareTraceIdOf — KQL kutusuna yapıştırılmış çıplak 32-hex trace id'si.
//     Sayfa "trace olarak süz" önerir: trace kutusu artık "+ Filter ▾"
//     popover'ının içinde, en sık yapıştırma yolu ona tek tıkla bağlanır.
import type { LogsUrlFilter } from '@/lib/logsUrl';

export function isDraftDirty(draft: LogsUrlFilter, applied: LogsUrlFilter): boolean {
  return draft.service !== applied.service
    || draft.cluster !== applied.cluster
    || draft.search !== applied.search
    || draft.severity !== applied.severity
    || draft.traceId !== applied.traceId
    || draft.spanId !== applied.spanId
    || draft.hasTrace !== applied.hasTrace
    || draft.pattern !== applied.pattern
    || draft.team !== applied.team;
}

const BARE_TRACE_ID = /^(?:0x)?([0-9a-f]{32})$/i;

/** Kutudaki metin YALNIZ bir trace id'siyse küçük harfli id, aksi hâlde ''. */
export function bareTraceIdOf(search: string): string {
  const m = BARE_TRACE_ID.exec(search.trim());
  return m ? m[1].toLowerCase() : '';
}
