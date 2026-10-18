// OperationCard — CoSRE operasyon tespiti Faz 3: sohbette operasyon adı
// yazılınca sunucunun ön-getirdiği paket (chat_operation_prefetch.go):
// metrik satırı (önceki eşit pencereye fark), hatalı trace listesi (satır =
// trace sayfası), trace'lere bağlı Oracle hata kodları, süzgeçli "Traces'te
// aç". Hatalı trace'ler tablo değil liste (≤10 satır, tablo mandalı).
import { Link } from 'react-router-dom';
import type { ChatOperationPayload } from '@/lib/types';
import { traceHref } from '@/lib/traceHref';
import { chatLinkTargetProps, useChatLinkNewTab } from './chatLinkTarget';
import {
  deltaLabel, errRate, fmtMs, opAlternativesLine, opEndpointExceptionRows, opEndpointExceptionsNote, opEndpointHref,
  opEndpointLabel, opExceptionRows, opExceptionsNote, worse,
} from './operationCardView';

export function OperationCard({ op }: { op: ChatOperationPayload }) {
  const linkProps = chatLinkTargetProps(useChatLinkNewTab());
  const errTraces = (op.traces ?? []).filter(t => t.error).slice(0, 10);
  const oracle = op.oracleErrors ?? [];
  const c = op.current, p = op.previous;
  const excRows = opExceptionRows(op); // v0.10.1331 — Exceptions grupları
  const excNote = opExceptionsNote(op);
  // v0.10.1343 — endpoint hedefi: başlık, /endpoint linki, endpoint exception'ları, alternatif servisler
  const epLabel = opEndpointLabel(op);
  const epHref = opEndpointHref(op);
  const epRows = opEndpointExceptionRows(op);
  const epNote = opEndpointExceptionsNote(op);
  const altLine = opAlternativesLine(op);
  return (
    <div className="cm-trace-list" style={{ marginTop: 6 }}>
      {epLabel ? (
        <div style={{ fontSize: 12, marginBottom: 6 }}>
          <b className="mono">{epLabel}</b>
          {altLine && <div className="cell-hint">{altLine}</div>}
        </div>
      ) : (
        <div style={{ fontSize: 12, marginBottom: 6 }}>
          <b className="mono">{op.operation}</b>
          {(op.services ?? []).length > 0 && <span style={{ color: 'var(--text3)' }}> · {(op.services ?? []).join(', ')}</span>}
          {(op.functionCodes ?? []).length > 0 && <span style={{ color: 'var(--text3)' }}> · kod {(op.functionCodes ?? []).join(', ')}</span>}
        </div>
      )}
      {c.measured ? (
        <div style={{ display: 'flex', gap: 16, fontSize: 12, marginBottom: 6, flexWrap: 'wrap' }}>
          <span>Çağrı <b>{c.calls}</b> <span style={{ color: 'var(--text3)' }}>{deltaLabel(c.calls, p.calls)}</span></span>
          <span className={worse('err', errRate(c), errRate(p)) ? 'cell-err' : undefined}>
            Hata oranı <b>%{(errRate(c) * 100).toFixed(2)}</b> <span style={{ color: 'var(--text3)' }}>(önce %{(errRate(p) * 100).toFixed(2)})</span>
          </span>
          <span className={worse('p99', c.p99Ms, p.p99Ms) ? 'cell-warn' : undefined}>
            p99 <b>{fmtMs(c.p99Ms)}</b> <span style={{ color: 'var(--text3)' }}>{deltaLabel(c.p99Ms, p.p99Ms)}</span>
          </span>
        </div>
      ) : (
        <div className="cell-hint" style={{ marginBottom: 6 }}>Metrik ölçülemedi (bu kaynak için ön-özet yok)</div>
      )}
      {errTraces.length > 0 && (
        <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 2, fontSize: 12 }}>
          {errTraces.map(t => (
            <li key={t.traceId} style={{ display: 'flex', gap: 8 }}>
              <Link className="mono" to={traceHref(t.traceId)} title={t.traceId} {...linkProps}>{t.traceId.slice(0, 16)}…</Link>
              <span>{t.service}</span>
              <span style={{ marginLeft: 'auto', fontVariantNumeric: 'tabular-nums' }}>{fmtMs(t.durationMs)}</span>
            </li>
          ))}
        </ul>
      )}
      {oracle.length > 0 && (
        <div style={{ marginTop: 6, display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          {oracle.map(o => (
            <span key={o.code} className="badge b-err" title={o.sample}>{o.code || '—'} ×{o.count}</span>
          ))}
        </div>
      )}
      {(excRows.length > 0 || excNote) && (
        <div style={{ marginTop: 8 }}>
          <div style={{ fontSize: 12, fontWeight: 600, marginBottom: 4 }}>Teknik hatalar</div>
          {excRows.length > 0 && (
            <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 2, fontSize: 12 }}>
              {excRows.map(r => (
                <li key={r.key} style={{ display: 'flex', gap: 8, alignItems: 'baseline' }} title={r.title}>
                  <span className="badge b-err mono">{r.code}</span>
                  {r.priority && <span className="cell-hint">{r.priority}</span>}
                  <span className={r.burst ? 'cell-err' : undefined}>{r.count}</span>
                  {r.lastSeen && <span className="cell-hint">{r.lastSeen}</span>}
                  {r.href && <Link to={r.href} style={{ marginLeft: 'auto' }} {...linkProps}>Exceptions →</Link>}
                </li>
              ))}
            </ul>
          )}
          {excNote && <div className="cell-hint">{excNote}</div>}
        </div>
      )}
      {(epRows.length > 0 || epNote) && (
        <div style={{ marginTop: 8 }}>
          <div style={{ fontSize: 12, fontWeight: 600, marginBottom: 4 }}>Endpoint teknik hataları</div>
          {epRows.length > 0 && (
            <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 2, fontSize: 12 }}>
              {epRows.map(r => (
                <li key={r.key} style={{ display: 'flex', gap: 8, alignItems: 'baseline' }} title={r.title}>
                  <span className="badge b-err mono">{r.code}</span>
                  <span>{r.count}</span>
                  {r.lastSeen && <span className="cell-hint">{r.lastSeen}</span>}
                  {r.href && <Link to={r.href} style={{ marginLeft: 'auto' }} {...linkProps}>Exceptions →</Link>}
                </li>
              ))}
            </ul>
          )}
          {epNote && <div className="cell-hint">{epNote}</div>}
        </div>
      )}
      <div className="cm-trace-list__more">
        <Link to={op.tracesHref} {...linkProps}>Traces'te aç →</Link>
        {epHref && <> · <Link to={epHref} {...linkProps}>Endpoint sayfası →</Link></>}
        <span className="cell-hint"> · {(op.traces ?? []).length} trace, {errTraces.length} hatalı gösterildi</span>
      </div>
    </div>
  );
}
