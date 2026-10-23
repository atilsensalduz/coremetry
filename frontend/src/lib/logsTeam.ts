import { getRaw, setRaw, removeRaw } from './storage';
import type { LogsMyTeam } from './types';

// logsTeam — v0.10.1477 (operatör 2026-10-23: "giren kişinin takımına göre
// logs sayfası filtrelensin otomatik"). /logs "Takımım" otomatik
// varsayılanının SAF karar çekirdeği + kişisel vazgeçme bayrağı.
//
// Sözleşme:
//   - URL'de herhangi bir süzgeç parametresi varsa (paylaşılan link, CoSRE
//     çipi, kayıtlı görünüm) ASLA dokunulmaz — açık URL kazanır.
//   - Kullanıcının takımı çözüldüyse VE takım süzgeci açıksa (takım alanı
//     yapılandırılmış + ES backend) sayfa `team=<takım>`'ı BİR KEZ yazar.
//   - Kullanıcı çipi kaldırırsa vazgeçme bu tarayıcıda kullanıcı başına
//     hatırlanır (localStorage, per-viewer kolaylık; try/catch storage.ts'de)
//     — "Takımım" düğmesi bayrağı siler ve süzgeci yeniden uygular.
//   - Takım yok / özellik kapalı / my-team hata → hiçbir şey (sayfa bugünkü).

export type TeamAutoDecision =
  | { kind: 'pending' }            // my-team henüz çözülmedi — süzgeçsiz istek ATMA
  | { kind: 'apply'; team: string } // bir kez team= yaz
  | { kind: 'none' };              // bugünkü davranış

export function decideTeamAutoApply(i: {
  /** Sayfa açılırken URL'de süzgeç durumu vardı (logsUrlHasFilterState). */
  urlHadFilters: boolean;
  /** Kullanıcı daha önce Takımım çipini kaldırdı. */
  optedOut: boolean;
  /** Bu bağlamada karar zaten verildi (bir kez kuralı). */
  done: boolean;
  /** undefined = yükleniyor, null = hata, aksi hâlde yanıt. */
  myTeam: LogsMyTeam | null | undefined;
}): TeamAutoDecision {
  if (i.done || i.urlHadFilters || i.optedOut) return { kind: 'none' };
  if (i.myTeam === undefined) return { kind: 'pending' };
  const team = (i.myTeam?.team ?? '').trim();
  if (!i.myTeam || !i.myTeam.available || !team) return { kind: 'none' };
  return { kind: 'apply', team };
}

/** Aynı takım mı (çip etiketi "Takımım" mı "Takım" mı) — kırpılmış, harf duyarsız. */
export function isSameTeam(a: string, b: string): boolean {
  const x = a.trim().toLocaleLowerCase('tr');
  return x !== '' && x === b.trim().toLocaleLowerCase('tr');
}

export function teamOptOutKey(userId: string | undefined): string {
  return `coremetry.logs.myTeamOptOut.${userId || 'anon'}`;
}

export function readTeamOptOut(userId: string | undefined): boolean {
  return getRaw(teamOptOutKey(userId)) === '1';
}

export function writeTeamOptOut(userId: string | undefined, optedOut: boolean): void {
  if (optedOut) setRaw(teamOptOutKey(userId), '1');
  else removeRaw(teamOptOutKey(userId));
}
