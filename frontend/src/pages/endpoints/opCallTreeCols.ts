import type { DataTableColumn } from '@/lib/dataTable';
import type { OpCallEdge, OpWindowEdge } from '@/lib/types';

// opCallTreeCols — v0.10.1491 (operasyon kartı R3). "Kod ve çağrılar"
// bölümünün çağrı ağacı tablosunun SAF yarısı: kolonlar, hedef etiketi,
// tür glifi, hata rozeti sınıfı, pencere-geneli dipnot satırı. Bileşen
// (OperationCardSection.tsx) yalnız çizer; kararlar burada ve testli.
//
// SIRALAMA. Sunucu satırları AĞAÇ sırasında verir (üst, sonra çocukları
// çağrıya göre); varsayılan sıralama YOK (initialSort verilmez) ki ağaç
// okunsun. Başlığa tıklayan düz bir sıralama alır; girinti derinliği yine
// gösterir.

/** Kolonlar — etiketler çeviriden (useT), kararlar sabit. */
export function opCallTreeCols(tr: (k: string) => string): DataTableColumn<OpCallEdge>[] {
  return [
    { id: 'target', label: tr('endpoint.opcard.colTarget'), sortValue: r => r.target, naturalDir: 'asc', flex: true, minWidth: 180, priority: 1 },
    { id: 'op', label: tr('endpoint.opcard.colOp'), sortValue: r => r.op, naturalDir: 'asc', width: 240, minWidth: 140, priority: 1 },
    { id: 'protocol', label: tr('endpoint.opcard.colProtocol'), sortValue: r => r.protocol, naturalDir: 'asc', width: 84, minWidth: 64, priority: 3 },
    { id: 'calls', label: tr('endpoint.opcard.colCalls'), sortValue: r => r.calls, numeric: true, width: 76, minWidth: 60, priority: 2 },
    { id: 'errPct', label: tr('endpoint.opcard.colErr'), sortValue: r => r.errPct, numeric: true, width: 76, minWidth: 60, priority: 2 },
    { id: 'p95Ms', label: 'P95', sortValue: r => r.p95Ms, numeric: true, width: 84, minWidth: 64, priority: 2 },
    { id: 'samples', label: tr('endpoint.opcard.colSamples'), width: 150, minWidth: 110, priority: 3 },
  ];
}

/** targetLabel — SAF: `db:oracle` → `oracle`, `queue:kafka:t` → `kafka · t`, `ext:h` → `h`. */
export function targetLabel(target: string): string {
  if (target.startsWith('db:')) return target.slice(3);
  if (target.startsWith('queue:')) {
    const [sys, ...rest] = target.slice(6).split(':');
    return rest.length ? `${sys} · ${rest.join(':')}` : sys;
  }
  if (target.startsWith('ext:')) return target.slice(4);
  return target;
}

/** kindGlyph — SAF: satır türünün işareti (renk değil; renk yalnız sapmada). */
export function kindGlyph(kind: string): string {
  switch (kind) {
    case 'db': return '⛁';
    case 'messaging': return '✉';
    case 'external': return '↗';
    default: return '⇄';
  }
}

/** errBadgeClass — SAF: CallersSection ile aynı eşikler; sağlıklı nötr. */
export function errBadgeClass(errPct: number): string {
  return errPct >= 5 ? 'b-err' : errPct >= 1 ? 'b-warn' : 'b-gray';
}

/** indentPx — SAF: derinlik girintisi (derinlik 1 = 0). */
export function indentPx(depth: number): number {
  return Math.max(0, Math.min(depth, 3) - 1) * 16;
}

/** windowEdgesLine — SAF: MV dipnotu, en çok 5 kenar ("svc · op: N"). */
export function windowEdgesLine(edges: OpWindowEdge[], fmt: (n: number) => string): string {
  return edges.slice(0, 5)
    .map(e => `${e.childService}${e.childOp ? ` · ${e.childOp}` : ''}: ${fmt(e.calls)}`)
    .join(' — ');
}
