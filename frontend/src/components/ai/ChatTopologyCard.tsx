// ChatTopologyCard — v0.10.1388 (CoSRE topoloji; operatör: "Servis topolojisini
// CoSRE'den yönlendir ya da örnek bir trace'in topolojisini çiz"). Sunucunun
// `topology` bloğu (internal/api/copilot_service_topology.go): servis komşuluğu
// (topology_edges_5m) ya da tek bir örnek trace'in servis grafiği. Çizim
// Topology sekmesinin pil grafiği (TopologyPillGraph, .topo-* tokenları);
// düğüme tık → düğümün detay sayfası (nodeDetailHref). Otomatik açılış yok.
// Hrefler lib/chatBlocks.ts topologyBlocks'ta isSafeAppPath'ten geçti.
import { useMemo } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import type { ChatTopologyPayload } from '@/lib/types';
import { TopologyPillGraph } from '@/components/TopologyPillGraph';
import { nodeDetailHref } from '@/components/topology/nodeDetailHref';
import { isSafeAppPath } from '@/lib/safeAppPath';
import { fmtRangeTR } from '@/lib/chatEvidence';
import { chatLinkTargetProps, useChatLinkNewTab } from './chatLinkTarget';
import { topologyColumns, topologyHeight, topologyPillEdges, topologyPillNodes } from './chatTopologyView';

export function ChatTopologyCard({ topo }: { topo: ChatTopologyPayload }) {
  const linkProps = chatLinkTargetProps(useChatLinkNewTab());
  const navigate = useNavigate();
  const nodes = useMemo(() => topologyPillNodes(topo), [topo]);
  const edges = useMemo(() => topologyPillEdges(topo), [topo]);
  const columns = useMemo(() => topologyColumns(topo), [topo]);
  const byId = useMemo(() => new Map(topo.nodes.map(n => [n.id, n])), [topo]);
  const onSelect = (id: string) => {
    const n = byId.get(id);
    const href = n ? nodeDetailHref(n) : null;
    if (href && isSafeAppPath(href)) navigate(href);
  };
  const trace = topo.scope === 'trace';
  const scopeLabel = trace
    ? `örnek trace${topo.operation ? ` (${topo.operation})` : ''}`
    : `${topo.hops} hop${topo.errorsOnly ? ' · yalnız hatalı' : ''}`;
  const cut = topo.truncated.nodes > 0 || topo.truncated.edges > 0;
  return (
    <section className="ev-card" aria-label={`Topoloji · ${topo.focus}`}>
      <div className="ev-head">
        Topoloji · <span className="mono">{topo.focus}</span> · {scopeLabel} · {fmtRangeTR(topo.rangeS)}
      </div>
      <div className="cm-topo-scroll">
        <TopologyPillGraph nodes={nodes} edges={edges} columns={columns} focus={topo.focus}
          onSelect={onSelect} height={topologyHeight(columns)} />
      </div>
      <div className="ev-foot">
        <Link to={topo.deepLink} {...linkProps}>Topology&apos;de aç</Link>
        {topo.traceHref && <Link to={topo.traceHref} {...linkProps}>Trace&apos;i aç</Link>}
        {cut && <span>+{topo.truncated.nodes} düğüm, +{topo.truncated.edges} kenar gösterilmedi</span>}
        {topo.note && <span>{topo.note}</span>}
      </div>
    </section>
  );
}
