import { traceRootOpCardRef, type EndpointPivotRef, type OpCardSpanLike } from './links';

// traceOpCardPick.ts — v0.10.1503 (operatör isteği 2026-10-24).
//
// Trace sayfasının operasyon kartı çipi (v0.10.1491) kök span'in rotasına
// gidiyordu. Kökü API gateway olan trace'lerde kart gateway'in operasyonunu
// gösteriyordu — oysa soru "isteği GERÇEKTEN işleyen servis ne yapıyor".
// Çip artık gateway / proxy kökünün arkasındaki İLK İÇ servisi seçer.
//
// SAF: yalnız yüklü span listesine bakar, istek yok, ayar okumaz.
//
// Gateway kökü tespiti, sırayla:
//   (1) yapılandırılmış liste (`gatewayServices`) — bugün ayar YOK (kod
//       tabanında gateway SERVİS ayarı aranıp bulunamadı; topology.go'nun
//       `gatewayHosts`'u çıkış geçidi host'u, servis değil). Parametre
//       ileride bir ayar gelirse diye açık; boşsa hiçbir şey yapmaz.
//   (2) yapısal: kök servisin bu giriş altındaki kendi span'leri yalnız
//       giriş + yönlendiren istemci/üretici span'leri, TEK bir alt servise
//       gidiyor ve öz süre düşük (≤ %25).
//   (3) ad sezgisi — yalnız ZAYIF bağ bozucu: ad eşleşirse yapısal şart
//       gevşer (iç ara katman span'lerine izin, öz süre ≤ %50); ad TEK
//       BAŞINA gateway yapmaz. Tek alt servis şartı her zaman geçerli.
// Normal servis (kendi mantığı olan BFF: birden çok alt servis, ya da
// yüksek öz süre) → kök kalır.
//
// v0.10.1526 (operatör, prod 2026-10-24) — kendi DB span'leri (SELECT /
// INSERT / UPDATE) ve MQ üretici span'leri olan bir kök + tek HTTP istemci
// çağrısı gateway sayılıp çip alt servise gidiyordu. DB istemci span'i
// (db.system dolu) ve mesajlaşma span'i (kind=producer ya da messaging.system
// dolu) servisin KENDİ İŞİ: böyle bir span'i olan servis saf yönlendirici
// DEĞİL — yapısal ve ad yolları kökü bırakır. Gateway = giriş + TEK alt
// servise HTTP/gRPC istemci span'leri, DB/MQ yok, düşük öz süre. Yalnız
// yapılandırılmış liste (1) bunu geçersiz kılar (operatörün açık beyanı).
//
// Zincir (ingress → api-gateway → servis) en çok MAX_HOPS adım yürünür.

/** Seçicinin span'den okuduğu alanlar (SpanRow alt kümesi). */
export interface OpCardPickSpan extends OpCardSpanLike {
  spanId: string;
  parentSpanId: string;
  startTime: number; // unix ns
  endTime: number;   // unix ns
  /** SpanRow.dbSystem — dolu ise DB istemci span'i (servisin kendi işi). */
  dbSystem?: string;
}

export type OpCardPickVia = 'root' | 'config' | 'structure' | 'name';

export interface OpCardPick {
  /** Kartın açılacağı kimlik (iç servis ya da kök). */
  ref: EndpointPivotRef;
  /** Seçilen servis — çipte gösterilir. */
  service: string;
  /** Kök neden atlandı ('root' = atlanmadı). */
  via: OpCardPickVia;
  /** Atlanan EN DIŞ gateway'in kart kimliği (ikincil "gateway: <op>" bağlantısı). */
  gateway?: EndpointPivotRef;
}

export const GATEWAY_NAME_RE = /gateway|apigw|ingress|proxy|nginx|envoy|kong|apisix/i;
/** Yapısal eşik: kök servisin öz süresi giriş süresinin bu oranını aşmaz. */
export const GATEWAY_MAX_SELF_FRACTION = 0.25;
/** Ad eşleşince gevşeyen eşik (bağ bozucu). */
export const GATEWAY_NAME_MAX_SELF_FRACTION = 0.5;
const MAX_HOPS = 4;

const kindOf = (s: OpCardPickSpan) => (s.kind || '').toLowerCase();
const isEntry = (s: OpCardPickSpan) => { const k = kindOf(s); return k === 'server' || k === 'consumer'; };
const attr = (s: OpCardPickSpan, k: string) => (s.attributes?.[k] || '').trim();
/** DB istemci span'i: db.system (kolon ya da öznitelik; semconv 1.26+ db.system.name). */
export const isDbSpan = (s: OpCardPickSpan) =>
  !!((s.dbSystem || '').trim() || attr(s, 'db.system') || attr(s, 'db.system.name'));
/** Mesajlaşma span'i: üretici kind'ı ya da messaging.system (Kafka/MQ istemci olarak da yazılır). */
export const isMessagingSpan = (s: OpCardPickSpan) => kindOf(s) === 'producer' || !!attr(s, 'messaging.system');
/** Servisin KENDİ İŞİ sayılan span: DB ya da mesajlaşma. */
export const isOwnWorkSpan = (s: OpCardPickSpan) => isDbSpan(s) || isMessagingSpan(s);
/** Yönlendiren span: HTTP/gRPC istemci çağrısı (DB / MQ değil). */
const isForward = (s: OpCardPickSpan) => kindOf(s) === 'client' && !isOwnWorkSpan(s);
const byStart = (a: OpCardPickSpan, b: OpCardPickSpan) => a.startTime - b.startTime || (a.spanId < b.spanId ? -1 : a.spanId > b.spanId ? 1 : 0);

/** Bir giriş span'i altındaki aynı-servis span'leri + ilk başka-servis giriş span'leri. */
function hopShape(entry: OpCardPickSpan, children: Map<string, OpCardPickSpan[]>) {
  const own: OpCardPickSpan[] = [];
  const downstream: OpCardPickSpan[] = [];
  const stack = [entry];
  const seen = new Set<string>();
  while (stack.length) {
    const s = stack.pop()!;
    if (seen.has(s.spanId)) continue;
    seen.add(s.spanId);
    own.push(s);
    for (const c of children.get(s.spanId) ?? []) {
      if (c.serviceName === entry.serviceName) stack.push(c);
      else if (isEntry(c)) downstream.push(c);
    }
  }
  return { own, downstream: downstream.sort(byStart) };
}

/** Girişin öz süre oranı: doğrudan çocukların (birleşik) kapsamadığı kısım. */
export function entrySelfFraction(entry: OpCardPickSpan, kids: OpCardPickSpan[]): number {
  const dur = entry.endTime - entry.startTime;
  if (dur <= 0) return 0;
  const iv = kids
    .map(k => [Math.max(k.startTime, entry.startTime), Math.min(k.endTime, entry.endTime)] as const)
    .filter(([a, b]) => b > a)
    .sort((x, y) => x[0] - y[0]);
  let covered = 0, curA = -Infinity, curB = -Infinity;
  for (const [a, b] of iv) {
    if (a > curB) { if (curB > curA) covered += curB - curA; curA = a; curB = b; }
    else if (b > curB) curB = b;
  }
  if (curB > curA) covered += curB - curA;
  return Math.max(0, 1 - covered / dur);
}

/**
 * gatewayHop — SAF: bu giriş bir gateway/proxy adımı mı; öyleyse işi devralan
 * ilk iç giriş span'i. Değilse null.
 */
export function gatewayHop(
  entry: OpCardPickSpan, children: Map<string, OpCardPickSpan[]>, gatewayServices: readonly string[] = [],
): { next: OpCardPickSpan; via: Exclude<OpCardPickVia, 'root'> } | null {
  const { own, downstream } = hopShape(entry, children);
  if (!downstream.length) return null; // iç giriş yok → kök kalır
  const next = downstream[0];
  if (gatewayServices.includes(entry.serviceName)) return { next, via: 'config' };
  if (new Set(downstream.map(d => d.serviceName)).size !== 1) return null; // fan-out = kendi mantığı
  const rest = own.filter(s => s !== entry);
  if (rest.some(isOwnWorkSpan)) return null; // kendi DB / MQ işi = saf yönlendirici değil
  const forwardOnly = rest.every(isForward);
  const self = entrySelfFraction(entry, children.get(entry.spanId) ?? []);
  if (forwardOnly && self <= GATEWAY_MAX_SELF_FRACTION) return { next, via: 'structure' };
  const nameHit = GATEWAY_NAME_RE.test(entry.serviceName);
  const noOwnEntries = rest.every(s => !isEntry(s));
  if (nameHit && noOwnEntries && self <= GATEWAY_NAME_MAX_SELF_FRACTION) return { next, via: 'name' };
  return null;
}

/**
 * pickTraceOpCard — SAF: Trace çipinin kart kimliği. Kök gateway/proxy ise
 * arkasındaki ilk iç servisin giriş span'i (rota, yoksa span adı + entry=rpc);
 * değilse bugünkü kök kimliği (traceRootOpCardRef). İstemci/üretici kökte null.
 */
export function pickTraceOpCard(
  spans: readonly OpCardPickSpan[] | null | undefined,
  opts: { gatewayServices?: readonly string[] } = {},
): OpCardPick | null {
  if (!spans || !spans.length) return null;
  const root = spans.find(s => !s.parentSpanId) ?? spans[0];
  const rootRef = traceRootOpCardRef(root);
  if (!rootRef) return null;
  const children = new Map<string, OpCardPickSpan[]>();
  for (const s of spans) {
    if (!s.parentSpanId) continue;
    const l = children.get(s.parentSpanId);
    if (l) l.push(s); else children.set(s.parentSpanId, [s]);
  }
  let cur = root;
  let via: OpCardPickVia = 'root';
  for (let hop = 0; hop < MAX_HOPS; hop++) {
    const h = gatewayHop(cur, children, opts.gatewayServices);
    if (!h || !traceRootOpCardRef(h.next)) break;
    if (via === 'root') via = h.via;
    cur = h.next;
  }
  if (cur === root) return { ref: rootRef, service: root.serviceName, via: 'root' };
  return { ref: traceRootOpCardRef(cur)!, service: cur.serviceName, via, gateway: rootRef };
}
