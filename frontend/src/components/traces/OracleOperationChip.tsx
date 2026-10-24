import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { CopyButton } from '@/components/CopyButton'; // v0.10.1418
import { useOracleFunctionCodes } from '@/lib/queries';
import type { LogRow, SpanRow, TimeRange } from '@/lib/types';
import { functionCodeTracesHref } from '@/lib/pivotHref';
import {
  traceFunctionCodes, exactTraceOperation, pickTraceOperation, oracleOperationTitle,
  exactOperationTitle, dictionaryOperationLabel, traceEntryCodes,
} from '@/lib/oracleFunctionOps';

// OracleOperationChip — v0.10.1003 (kuyruk: "operasyon adını trace sayfasında
// göster"). Trace özet şeridinde bu trace'in Oracle operasyon adı. Tıklayınca
// aynı fonksiyon kodunun diğer trace'leri.
//
// v0.10.1329 (operatör-bildirimli) — KESİN kaynak önce (pickTraceOperation):
// (a) bu trace'in kendi Oracle hata satırları, (b) span'in operasyon
// attribute'u, (c) ancak ikisi de yoksa fonksiyon kodu sözlüğü — ve o zaman
// etiket bunun bir tahmin olduğunu söyler ("fonksiyon kodundan, N aday").
// Eskiden yalnız (c) vardı ve bankanın genelinde en çok satırlı aday "bu
// trace'in operasyonu" gibi görünüyordu (TRX trace'inde INQ adı, "+15").
//
// `oracleRows` undefined = trace'in Oracle satırları HÂLÂ yükleniyor: çip
// bekler — önce sözlük tahmini çizip sonra kesin ada atlamak, yanlış adı bir
// an göstermek demek. İstek disiplini: sözlük (kod → ad) TEK paylaşılan istek,
// 5 dk taze, YALNIZ trace bir fonksiyon kodu taşıyor VE kesin kaynak yoksa.
export function OracleOperationChip({ spans, pageRange, oracleRows }: {
  spans: SpanRow[];
  pageRange: TimeRange;
  oracleRows: LogRow[] | undefined;
}) {
  const codes = useMemo(() => traceFunctionCodes(spans), [spans]);
  const exact = useMemo(
    () => (oracleRows === undefined ? undefined : exactTraceOperation(oracleRows, spans)),
    [oracleRows, spans]);
  const q = useOracleFunctionCodes(codes.length > 0 && exact === null);
  const pick = useMemo(
    () => (oracleRows === undefined ? null : pickTraceOperation(oracleRows, spans, q.data)),
    [oracleRows, spans, q.data]);
  if (!pick) return null;

  if (pick.source === 'dictionary') {
    return (
      <>
        {pick.candidates.map(o => {
          const title = oracleOperationTitle(o);
          return (
            <span key={o.code} className="cell-hint" title={title} data-op-source="dictionary">
              {dictionaryOperationLabel(o)}{' '}
              <Link className="sec mono op-chip" aria-label={`${o.operation} — ${title}`}
                to={functionCodeTracesHref({ window: pageRange, attrKey: o.key, codes: [o.code] })}>
                {o.operation}
              </Link>
              <CopyButton value={o.operation} title="Operasyon adını kopyala" />
            </span>
          );
        })}
      </>
    );
  }

  // Kesin kaynak: tek çip. Link hedefi değişmedi — trace'in (ilk) fonksiyon
  // kodunun diğer trace'leri; trace kod taşımıyorsa ad düz metin.
  const title = exactOperationTitle(pick);
  const fc = codes[0];
  return (
    <span className="cell-hint" title={fc ? `${title} Tıkla: bu fonksiyon kodunun diğer trace'leri.` : title} data-op-source={pick.source}>
      Operasyon:{' '}
      {fc ? (
        <Link className="sec mono op-chip" aria-label={`${pick.main} — ${title}`}
          to={functionCodeTracesHref({ window: pageRange, attrKey: fc.key, codes: [fc.code] })}>
          {pick.main}
        </Link>
      ) : (
        <span className="mono">{pick.main}<span className="sr-only"> — {title}</span></span>
      )}
      {/* v0.10.1418 (operatör: "Operasyon adı kopyalanabilir olsun") — kopyala düğmesi;
          link tıklaması aynı fonksiyon kodunun trace'lerine gitmeye devam eder. */}
      <CopyButton value={pick.main} title="Operasyon adını kopyala" />
      {pick.others.length > 0 ? ` (+${pick.others.length})` : ''}
    </span>
  );
}

// TraceEntryCodes — v0.10.1497 (operatör: "function code / channel code
// bilgilerini yukarıda operasyonun yanında yazabiliriz"). Kök → giriş →
// herhangi span'deki İLK fonksiyon ve kanal kodu; etiket span'deki anahtar
// yazımıdır (uydurma ad yok). Fonksiyon kodu tıklanınca aynı kodun diğer
// trace'leri (operasyon çipiyle aynı hedef); ikisi de kopyalanabilir.
export function TraceEntryCodes({ spans, pageRange }: { spans: SpanRow[]; pageRange: TimeRange }) {
  const { functionCode: fc, channelCode: cc } = useMemo(() => traceEntryCodes(spans), [spans]);
  if (!fc && !cc) return null;
  return (
    <span className="cell-hint" data-trace-codes="">
      {fc && (
        <>
          {fc.key}:{' '}
          <Link className="sec mono op-chip" title="Bu fonksiyon kodunun diğer trace'leri"
            to={functionCodeTracesHref({ window: pageRange, attrKey: fc.key, codes: [fc.code] })}>
            {fc.code}
          </Link>
          <CopyButton value={fc.code} title="Fonksiyon kodunu kopyala" />
        </>
      )}
      {fc && cc && ' · '}
      {cc && (
        <>
          {cc.key}: <span className="mono">{cc.code}</span>
          <CopyButton value={cc.code} title="Kanal kodunu kopyala" />
        </>
      )}
    </span>
  );
}
