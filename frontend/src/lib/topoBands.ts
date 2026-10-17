// topoBands.ts — topoloji namespace şeridi ÜYELİĞİ, SAF.
//
// Yerleşim çekirdeği (topoBfsLayout) jenerik kalır: yalnız `bandOf(düğüm)`
// alır. Hangi düğümün hangi şeride girdiği burada hesaplanır.
//
// v0.10.1261/1265 — servis şeridi (hull kuralı): kind'lı bağımlılık ve katlı
// süper-düğüm serbest; namespace'i grafta ≥2 servisle temsil edilen servis
// (ya da odaklı servisin namespace'i, tek üyeyle de) o şeride girer.
//
// v0.10.1268 — operatör 2026-10-17: tek namespace'in bağımlılıkları o
// namespace şeridinde. Bir bağımlılık (db/queue/external — `kind`'lı düğüm)
// TÜM çağıranları aynı adlı şeritteyse o şeride, kendi katman sütununa
// oturur. ≥2 şeritten çağrılan, ya da en az bir çağıranı grupsuz (ns'siz
// servis, katlı süper-düğüm, başka bir bağımlılık) olan bağımlılık grupsuz
// şeritte kalır. Şerit başlığındaki "N svc" sayısı yalnız SERVİSLERİ sayar
// (`bandServiceCounts`) — bir db "3 svc"yi şişirmez.

import { isNsNode } from './topoFold';

export interface BandNode { service: string; kind?: string }
export interface BandEdge { caller: string; callee: string }

/** servis → namespace şeridi (hull kuralı + odak istisnası). */
export function serviceBands(
  nodes: readonly BandNode[],
  nsOf: (service: string) => string | undefined,
  focusNs?: string,
): Map<string, string> {
  const m = new Map<string, string>();
  const counts = new Map<string, number>();
  for (const node of nodes) {
    if (node.kind || isNsNode(node.service)) continue;
    const ns = nsOf(node.service);
    if (!ns) continue;
    m.set(node.service, ns);
    counts.set(ns, (counts.get(ns) ?? 0) + 1);
  }
  for (const [svc, ns] of m) if ((counts.get(ns) ?? 0) < 2 && ns !== focusNs) m.delete(svc);
  return m;
}

/** bağımlılık → şerit: yalnız tüm çağıranları TEK adlı şeritteyse. */
export function dependencyBands(
  nodes: readonly BandNode[],
  edges: readonly BandEdge[],
  svcBand: ReadonlyMap<string, string>,
): Map<string, string> {
  const deps = new Set<string>();
  for (const n of nodes) if (n.kind && !isNsNode(n.service)) deps.add(n.service);
  // undefined = henüz çağıran yok; null = karışık/grupsuz (kalıcı)
  const acc = new Map<string, string | null>();
  for (const e of edges) {
    if (!deps.has(e.callee) || e.caller === e.callee) continue;
    const prev = acc.get(e.callee);
    if (prev === null) continue;
    const b = svcBand.get(e.caller) ?? null;
    acc.set(e.callee, prev === undefined || prev === b ? b : null);
  }
  const m = new Map<string, string>();
  for (const [dep, b] of acc) if (b !== null) m.set(dep, b);
  return m;
}

/** şerit → SERVİS sayısı (başlık etiketi; bağımlılıklar sayılmaz). */
export function bandServiceCounts(svcBand: ReadonlyMap<string, string>): Map<string, number> {
  const c = new Map<string, number>();
  for (const ns of svcBand.values()) c.set(ns, (c.get(ns) ?? 0) + 1);
  return c;
}
