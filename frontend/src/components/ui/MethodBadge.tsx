import type { HTMLAttributes } from 'react';

// MethodBadge — HTTP method rozeti (v0.10.1425). Endpoints listesi,
// endpoint detay başlığı ve servisin HTTP Metrics tabloları aynı atomu
// okur; renk eşlemesi TEK yerde durur.
//
// Renk API araçlarının alışık eşlemesi — kategori rengi, sağlık değil:
//   GET          mavi   (.m-get    → --info / --info-bg)
//   POST         yeşil  (.m-post   → --ok   / --ok-bg)
//   PUT, PATCH   amber  (.m-put / .m-patch → --warn / --warn-bg)
//   DELETE       kırmızı(.m-delete → --err  / --err-bg)
//   diğerleri    nötr   (.m-other  → --bg3  / --text2) — HEAD, OPTIONS,
//                       CONNECT, TRACE, `_OTHER`, boş/bilinmeyen
// Yumuşak ton (zemin tonu + koyu metin, .b-ok/.b-err gibi); boyut ve
// tipografi `.badge`den, yazı `.mono`. Kural gövdeleri globals.css'te
// `.badge.m-*` (v0.10.1425 hunk'ı); `primitiveClasses` kapısı aşağıdaki
// haritanın CSS karşılığını kendiliğinden tarar.

export type MethodTone = 'get' | 'post' | 'put' | 'patch' | 'delete' | 'other';

const TONES: Record<string, MethodTone> = {
  GET: 'get',
  POST: 'post',
  PUT: 'put',
  PATCH: 'patch',
  DELETE: 'delete',
};

/** Kırpılmış, büyük harfe çevrilmiş method adı ("get " → "GET"). */
export function normalizeMethod(method: string | null | undefined): string {
  return (method ?? '').trim().toUpperCase();
}

/** Method → renk tonu; bilinmeyen/boş → `other` (nötr). */
export function methodTone(method: string | null | undefined): MethodTone {
  return TONES[normalizeMethod(method)] ?? 'other';
}

const methodClass: Record<MethodTone, string> = {
  get:    'm-get',
  post:   'm-post',
  put:    'm-put',
  patch:  'm-patch',
  delete: 'm-delete',
  other:  'm-other',
};

export interface MethodBadgeProps extends Omit<HTMLAttributes<HTMLSpanElement>, 'children'> {
  /** Ham method adı; görüntü büyük harf. */
  method: string;
}

export function MethodBadge({ method, className, ...rest }: MethodBadgeProps) {
  const label = normalizeMethod(method);
  const cls = ['badge', 'mono', methodClass[methodTone(label)], className].filter(Boolean).join(' ');
  return <span className={cls} data-method={label} {...rest}>{label}</span>;
}
