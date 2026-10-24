import { useT } from '@/lib/i18n';
import { fmtNs, tsLong } from '@/lib/utils';

// TraceMeasures — v0.10.1514 (trace başlığı A, operatör onayı 2026-10-24).
// Başlığın satır 1 sağındaki ölçü kümesi: SÜRE (ilk span başlangıcı → son
// span bitişi) · KRİTİK YOL (kök span duvar süresi; lib/criticalPath — iç içe
// süreler TOPLANMAZ) · SPAN (+ servis sayısı) · BAŞLANGIÇ. Hepsi sayfanın
// zaten elindeki span listesinden türer; ek istek yok. Büyük mono sayı +
// küçük birim, üstte küçük harfli etiket (<dl>: dt/dd çiftleri).
// ≤1280px'te başlığın altına iner, sola dayanır (globals.css trace-hdr bloğu).

/** "24.97ms" → ["24.97", "ms"]; sayı+birim biçiminde değilse birim boş. */
export function splitValueUnit(s: string): [string, string] {
  const m = /^(-?[\d.,]+)\s*([^\d\s].*)$/.exec(s);
  return m ? [m[1], m[2]] : [s, ''];
}

function Num({ text }: { text: string }) {
  const [n, u] = splitValueUnit(text);
  return <>{n}{u && <span className="trace-measures__unit"> {u}</span>}</>;
}

export function TraceMeasures({ totalNs, criticalNs, criticalSpans, spanCount, svcCount, startNs }: {
  totalNs: number;
  /** Kritik yolun kök duvar süresi; yol yoksa null. */
  criticalNs: number | null;
  criticalSpans: number;
  spanCount: number;
  svcCount: number;
  startNs: number | undefined;
}) {
  const tr = useT();
  const svcLabel = tr(svcCount === 1 ? 'trace.hdr.serviceOne' : 'trace.hdr.serviceMany').replace('{n}', String(svcCount));
  return (
    <dl className="trace-measures" aria-label={tr('trace.hdr.measures')} data-testid="trace-measures">
      <div className="trace-measures__item">
        <dt>{tr('trace.hdr.duration')}</dt>
        <dd className="trace-summary__dur" title="Trace toplam süresi: ilk span başlangıcından son span bitişine"><Num text={fmtNs(totalNs)} /></dd>
      </div>
      <div className="trace-measures__item">
        <dt>{tr('trace.hdr.critical')}</dt>
        {criticalNs != null && criticalSpans > 0 ? (
          <dd title={`Kritik yol: kökten yaprağa ${criticalSpans} span; kök span duvar süresi ${fmtNs(criticalNs)}. İç içe span süreleri toplanmaz. Odaklamak için alttaki "Critical path focus" düğmesi.`}>
            <Num text={fmtNs(criticalNs)} />
          </dd>
        ) : <dd className="trace-measures__none">—</dd>}
      </div>
      <div className="trace-measures__item">
        <dt>{tr('trace.hdr.spans')}</dt>
        <dd title={`${spanCount} span · ${svcLabel}`}>{spanCount}<span className="trace-measures__unit"> · {svcLabel}</span></dd>
      </div>
      <div className="trace-measures__item">
        <dt>{tr('trace.hdr.start')}</dt>
        <dd className="trace-measures__start" title="Trace başlangıcı (kök span)">{startNs ? tsLong(startNs) : '—'}</dd>
      </div>
    </dl>
  );
}
