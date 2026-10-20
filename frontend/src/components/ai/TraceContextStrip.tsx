import { useMemo } from 'react';
import { useT } from '@/lib/i18n';
import { formatTraceWindow, type TraceAiContext } from '@/lib/traceAiContext';

// TraceContextStrip — v0.10.944 (CoSRE Faz A): CoSRE sohbetinin "Bağlam"
// şeridi (etkin trace açıklama turu). Sohbetin HANGİ trace/span/servis/ortam/
// cluster/namespace ve pencereye kapsandığını gösterir; operatör takip
// sorusunun hangi env'e bakacağını bilsin diye.
//
// Veri sayfanın yayınladığı CANLI bağlamdan (lib/traceAiContext deposu).
// Bilinmeyen alan hiç çizilmez ("—" bile değil): şerit iddia değil, bilinenin
// listesi. Öğeler mevcut `.chip` anatomisi; kap `.ai-ctx` (globals.css).
export function TraceContextStrip({ traceId, ctx }: {
  traceId: string;
  ctx: TraceAiContext | null;
}) {
  const tr = useT();
  const win = useMemo(() => (ctx ? formatTraceWindow(ctx.fromNs, ctx.toNs) : null), [ctx]);
  const clusterNs = [ctx?.cluster, ctx?.namespace].filter(Boolean).join(' / ');
  return (
    <div className="ai-ctx" role="group" aria-label={tr('ai.ctx.aria')}>
      <span className="ai-ctx__cap">{tr('ai.ctx.label')}</span>
      <span className="chip" title={traceId}>
        <span className="k">{tr('ai.ctx.trace')}</span><b className="mono">{traceId.slice(0, 8)}</b>
      </span>
      {ctx?.spanId && (
        <span className="chip" title={ctx.spanName ? `${ctx.spanName} · ${ctx.spanId}` : ctx.spanId}>
          <span className="k">{tr('ai.ctx.span')}</span>
          <b className={ctx.spanName ? undefined : 'mono'}>{ctx.spanName || ctx.spanId.slice(0, 8)}</b>
        </span>
      )}
      {ctx?.service && (
        <span className="chip" title={ctx.version ? `${ctx.service} · ${ctx.version}` : ctx.service}>
          <span className="k">{tr('ai.ctx.service')}</span><b>{ctx.service}</b>
        </span>
      )}
      {ctx?.env && (
        <span className="chip" title={ctx.env}>
          <span className="k">{tr('ai.ctx.env')}</span><b>{ctx.env}</b>
        </span>
      )}
      {clusterNs && (
        <span className="chip" title={clusterNs}>
          <span className="k">{tr('ai.ctx.clusterNs')}</span><b>{clusterNs}</b>
        </span>
      )}
      {win && (
        <span className="chip" title={`${win.text} (${win.tzLabel})`}>
          <span className="k">{tr('ai.ctx.window')}</span><b className="mono">{win.text}</b>
          <span className="k">{win.tzLabel}</span>
        </span>
      )}
    </div>
  );
}
