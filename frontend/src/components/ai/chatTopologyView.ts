// chatTopologyView — v0.10.1388 (CoSRE topoloji bloğu): ChatTopologyPayload →
// TopologyPillGraph girdileri (düğüm / kenar / sütun). SAF, vitest'li.
//
// Sütunlar: 'neighborhood' kipinde Topology sekmesinin odak yerleşimi
// (assignFocusColumns: çağıranlar solda, bağımlılıklar sağda); 'trace' kipinde
// köklerden (gelen kenarı olmayan düğümler) BFS derinliği — tek trace soldan
// sağa akar. Renk: hata oranı ≥%5 kırmızı, ≥%1 sarı (sunucu metniyle aynı eşik).
import type { ChatTopologyPayload, GraphEdge, GraphNode } from '@/lib/types';
import type { PillEdge, PillLevel, PillNode } from '@/components/TopologyPillGraph';
import { assignFocusColumns } from '@/components/topology/FocusedNeighborhood';
import { fmtNum } from '@/lib/utils';

export const TOPO_WARN_PCT = 1;
export const TOPO_ERR_PCT = 5;

export function topoLevel(errorRate: number): PillLevel {
  if (errorRate >= TOPO_ERR_PCT) return 'red';
  if (errorRate >= TOPO_WARN_PCT) return 'amber';
  return 'green';
}

function fmtMs(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`;
  if (ms < 10) return `${ms.toFixed(1)} ms`;
  return `${ms.toFixed(0)} ms`;
}

/** Düğümün p99'u: gelen kenarların en büyüğü, yoksa giden kenarların. */
function nodeP99(id: string, edges: GraphEdge[]): number {
  let inMax = 0, outMax = 0;
  for (const e of edges) {
    if (e.target === id) inMax = Math.max(inMax, e.p99Ms);
    if (e.source === id) outMax = Math.max(outMax, e.p99Ms);
  }
  return inMax > 0 ? inMax : outMax;
}

const KIND_TR: Record<string, string> = { database: 'veritabanı', queue: 'kuyruk', external: 'dış' };

export function topologyPillNodes(p: ChatTopologyPayload): PillNode[] {
  return p.nodes.map((n: GraphNode) => {
    const p99 = nodeP99(n.id, p.edges);
    const kind = KIND_TR[n.kind];
    return {
      id: n.id,
      name: n.name,
      level: topoLevel(n.errorRate),
      sub: `${fmtNum(n.calls)} çağrı · p99 ${fmtMs(p99)}`,
      title: `${n.name}${kind ? ` (${kind})` : ''} — hata %${n.errorRate.toFixed(2)}`,
    };
  });
}

export function topologyPillEdges(p: ChatTopologyPayload): PillEdge[] {
  return p.edges.map(e => ({
    from: e.source,
    to: e.target,
    level: e.errorRate >= TOPO_ERR_PCT ? 'err' : e.errorRate >= TOPO_WARN_PCT ? 'warn' : undefined,
  }));
}

/** Sütunlar (soldan sağa düğüm id'leri); her düğüm tam bir sütunda. */
export function topologyColumns(p: ChatTopologyPayload): string[][] {
  const ids = p.nodes.map(n => n.id);
  const col = new Map<string, number>();
  if (p.scope === 'trace') {
    const incoming = new Set(p.edges.map(e => e.target));
    let frontier = ids.filter(id => !incoming.has(id));
    if (frontier.length === 0 && ids.length > 0) frontier = [p.focus && ids.includes(p.focus) ? p.focus : ids[0]];
    frontier.forEach(id => col.set(id, 0));
    for (let d = 1; frontier.length > 0 && d <= ids.length; d++) {
      const next: string[] = [];
      for (const id of frontier) {
        for (const e of p.edges) {
          if (e.source === id && !col.has(e.target)) { col.set(e.target, d); next.push(e.target); }
        }
      }
      frontier = next;
    }
  } else {
    const hops = Math.max(1, p.hops || 1);
    for (const [id, c] of assignFocusColumns(p.edges, p.focus, hops)) col.set(id, c);
  }
  // Ulaşılmayan düğüm (ör. odağın çağıranının diğer bağımlılığı) en sağ sütuna.
  const maxCol = Math.max(0, ...[...col.values()]);
  for (const id of ids) if (!col.has(id)) col.set(id, maxCol + (p.scope === 'trace' ? 1 : 0));
  const keys = [...new Set(ids.map(id => col.get(id)!))].sort((a, b) => a - b);
  return keys.map(k => ids.filter(id => col.get(id) === k));
}

/** Grafiğin yüksekliği: en kalabalık sütun × satır payı (160..420 px). */
export function topologyHeight(columns: string[][]): number {
  const rows = Math.max(1, ...columns.map(c => c.length));
  return Math.min(420, Math.max(160, rows * 58 + 40));
}
