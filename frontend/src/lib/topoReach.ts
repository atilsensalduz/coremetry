// topoReach.ts — v0.10.1305: genel servis haritasında hover YOL vurgusu, SAF.
//
// Operatör 2026-10-17: genel haritada (/service-map, odak yok) hover yalnız
// 1-hop komşuluğu yakıyordu; çağrı zinciri okunmuyordu. Odak kipinin
// focusPath'i (lib/topoFocusLayout) sütun sözleşmesine bağlı, genel haritada
// sütun yok. reachPath: hover edilen düğümden YUKARI (çağıranlar üzerinden) ve
// AŞAĞI (çağrılanlar üzerinden) en çok `maxHops` adım, yalnız İLERİ kenarlar
// (yerleşimin döngü kırdığı geri kenarlar atlanır — yol döngüde dolanmaz).
//
// Sıra: hop seviyesi seviye seviye, her seviyede önce yukarı sonra aşağı;
// komşular ada göre sıralı → çıktı girdi sırasından bağımsız. `cap` toplam
// düğüm tavanıdır (hover dahil): yoğun graftaki bir hub haritanın yarısını
// yakmasın. Kenar yalnız İKİ ucu da yoldaysa eklenir.
//
// Maliyet: hover başına O(E) komşuluk + O(cap·deg) yürüme (500/20k grafta ~ms).

export interface ReachEdge { caller: string; callee: string }
export interface ReachPath { nodes: Set<string>; edges: Set<string> }

export const REACH_MAX_HOPS = 2;
export const REACH_CAP = 60;

export function reachPath(
  hover: string,
  edges: readonly ReachEdge[],
  backEdges: ReadonlySet<string>,
  maxHops = REACH_MAX_HOPS,
  cap = REACH_CAP,
): ReachPath {
  const nodes = new Set<string>([hover]);
  const keys = new Set<string>();
  const up = new Map<string, ReachEdge[]>();   // callee → kenarlar (çağıranlar)
  const down = new Map<string, ReachEdge[]>(); // caller → kenarlar (çağrılanlar)
  for (const e of edges) {
    if (e.caller === e.callee || backEdges.has(`${e.caller}|${e.callee}`)) continue;
    (up.get(e.callee) ?? up.set(e.callee, []).get(e.callee)!).push(e);
    (down.get(e.caller) ?? down.set(e.caller, []).get(e.caller)!).push(e);
  }
  const byCaller = (a: ReachEdge, b: ReachEdge) => (a.caller < b.caller ? -1 : a.caller > b.caller ? 1 : 0);
  const byCallee = (a: ReachEdge, b: ReachEdge) => (a.callee < b.callee ? -1 : a.callee > b.callee ? 1 : 0);
  let upF = [hover], downF = [hover];
  for (let h = 0; h < maxHops; h++) {
    for (const dir of [0, 1] as const) {
      const frontier = dir === 0 ? upF : downF;
      const next: string[] = [];
      for (const id of frontier) {
        const list = (dir === 0 ? up : down).get(id);
        if (!list) continue;
        const sorted = list.slice().sort(dir === 0 ? byCaller : byCallee);
        for (const e of sorted) {
          const nb = dir === 0 ? e.caller : e.callee;
          if (!nodes.has(nb)) {
            if (nodes.size >= cap) continue;
            nodes.add(nb);
            next.push(nb);
          }
          keys.add(`${e.caller}|${e.callee}`);
        }
      }
      if (dir === 0) upF = next; else downF = next;
    }
  }
  return { nodes, edges: keys };
}
