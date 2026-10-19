// chatTopologyView — ChatTopologyPayload → TopologyFlowGraph girdileri. SAF, vitest'li.
//
// v0.10.1392 — operatör (prod ekran görüntüleri): "Bunu galiba eski service
// map'ten getiriyor. Onun yerine 1 hop topology getirebilir." v0.10.1388 kartı
// TopologyPillGraph'la çiziyordu (sütun kesirleri + kalabalık sütunda üst üste
// binen alt satırlar, sol kenarda kırpılan adlar). Kart artık servis sayfasının
// Topology sekmesinin grafiğini (TopologyFlowGraph, odak sütun yerleşimi)
// kompakt kipte çizer; düğüm adaptörü sekmeyle ORTAK (graphNodeToMapNode /
// graphEdgeToMapEdge), sütunlar sekmenin assignFocusColumns'u.
//
//  • 'neighborhood': odak sütun 0, çağıranlar −, bağımlılıklar + ; sunucunun
//    "+N more" düğümü (more:<sütun>) kendi sütununa (moreNodeCol).
//  • 'trace': yerleşim odağı trace'in KÖKÜ (gelen kenarı olmayan düğüm; sorulan
//    servis kökse o) — tek trace soldan sağa akar; köke yönlü yoldan ulaşılmayan
//    düğüm en sağ sütunun bir sağına.
import type { ChatTopologyPayload, ServiceMap } from '@/lib/types';
import { assignFocusColumns, graphEdgeToMapEdge, graphNodeToMapNode } from '@/components/topology/FocusedNeighborhood';
import { isMoreNode, moreNodeCol } from '@/lib/topoFocusLayout';

/** GraphNode/GraphEdge → ServiceMap (TopologyFlowGraph sözleşmesi). */
export function chatTopologyMap(p: ChatTopologyPayload): ServiceMap {
  return { sampledFrom: 0, totalSpans: 0, nodes: p.nodes.map(graphNodeToMapNode), edges: p.edges.map(graphEdgeToMapEdge) };
}

/** Trace kipinin yerleşim odağı: kök (gelen kenarı yok; sorulan servis kökse o, yoksa en çok çağrı yapan kök). */
export function chatTopologyLayoutFocus(p: ChatTopologyPayload): string {
  if (p.scope !== 'trace') return p.focus;
  const incoming = new Set(p.edges.map(e => e.target));
  const ids = p.nodes.map(n => n.id);
  if (ids.includes(p.focus) && !incoming.has(p.focus)) return p.focus;
  const outCalls = new Map<string, number>();
  for (const e of p.edges) outCalls.set(e.source, (outCalls.get(e.source) ?? 0) + e.calls);
  const roots = ids.filter(id => !incoming.has(id))
    .sort((a, b) => ((outCalls.get(b) ?? 0) - (outCalls.get(a) ?? 0)) || (a < b ? -1 : a > b ? 1 : 0));
  return roots[0] ?? (ids.includes(p.focus) ? p.focus : ids[0] ?? p.focus);
}

/** İşaretli sütunlar (id → sütun); yerleşim odağı 0. Her düğüm tam bir sütunda. */
export function chatTopologyColumns(p: ChatTopologyPayload): Map<string, number> {
  const focus = chatTopologyLayoutFocus(p);
  const hops = p.scope === 'trace' ? Math.max(1, p.nodes.length) : Math.max(1, p.hops || 1);
  const col = assignFocusColumns(p.edges, focus, hops);
  for (const n of p.nodes) if (isMoreNode(n.id)) col.set(n.id, moreNodeCol(n.id));
  const ids = new Set(p.nodes.map(n => n.id));
  for (const id of [...col.keys()]) if (id !== focus && !ids.has(id)) col.delete(id);
  const maxCol = Math.max(0, ...col.values());
  for (const id of ids) if (!col.has(id)) col.set(id, maxCol + 1);
  return col;
}
