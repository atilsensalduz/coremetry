// pageActions.ts — v0.10.542 (Faz 3.4): `action` bloğunu sayfa state'ine
// uygular, SAF karar. Aksiyon yalnız tool sonucundan gelir (sunucu
// chat_actions.go); burada ikinci kapı: kök-göreli href, AYNI pathname —
// operatör cevap geldikten sonra başka sayfaya geçtiyse düğme görünmez
// (etiket "bu sayfada" der, başka sayfaya götürmez). Uygulama mergeOpenHref
// ile URL birleşimi + replace:true navigate: yeni sekme yok, sayfa URL'yi
// kaynak-of-truth olarak yeniden okur (frontend-conventions §4).
import { mergeOpenHref } from './openHref';

export interface ApplyAction { kind: 'apply'; href: string; label: string }

export function parseAction(payload: unknown): ApplyAction | null {
  if (!payload || typeof payload !== 'object') return null;
  const p = payload as Partial<ApplyAction>;
  if (p.kind !== 'apply' || typeof p.href !== 'string' || !p.href.startsWith('/') || p.href.startsWith('//')) return null;
  return { kind: 'apply', href: p.href, label: typeof p.label === 'string' && p.label ? p.label : 'Bu sayfada uygula' };
}

function hrefPath(href: string): string {
  const q = href.indexOf('?');
  return q >= 0 ? href.slice(0, q) : href;
}

/** Düğme yalnız aksiyonun hedef sayfası açıkken görünür. */
export function actionVisible(a: ApplyAction, pathname: string): boolean {
  return hrefPath(a.href) === pathname;
}

/** Uygulanacak URL; hedef sayfa açık değilse null (düğme zaten gizli). */
export function applyActionHref(a: ApplyAction, pathname: string, search: string): string | null {
  if (!actionVisible(a, pathname)) return null;
  return mergeOpenHref(a.href, pathname, search);
}

// v0.10.1169 — "Bulguyu probleme not olarak kaydet": sunucu YALNIZ başarılı
// tool sonucundan üretir (chat_actions.go problemNoteAction); metin + jeton
// sunucudan gelir, istemci değiştiremez (jeton metni imzalar).
export interface ProblemNoteAction { kind: 'problem_note'; problemId: string; text: string; token: string; tool: string; label: string }

export function parseProblemNoteAction(payload: unknown): ProblemNoteAction | null {
  if (!payload || typeof payload !== 'object') return null;
  const p = payload as Partial<ProblemNoteAction>;
  if (p.kind !== 'problem_note' || typeof p.problemId !== 'string' || !p.problemId
    || typeof p.text !== 'string' || !p.text || typeof p.token !== 'string' || !p.token) return null;
  return {
    kind: 'problem_note', problemId: p.problemId, text: p.text, token: p.token,
    tool: typeof p.tool === 'string' ? p.tool : '',
    label: typeof p.label === 'string' && p.label ? p.label : 'Bulguyu probleme not olarak kaydet',
  };
}
