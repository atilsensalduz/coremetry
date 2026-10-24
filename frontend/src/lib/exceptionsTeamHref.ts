// exceptionsTeamHref — v0.10.1515 (operatör 2026-10-24: CoSRE karşılamasındaki
// "Takımının son exception'larına bakabilirim" kartında takım seçmek
// Exceptions sayfasını o takımla süzülü açsın). Exceptions sayfasının
// (/problems, AnomaliesPage) takım süzgeci URL'de iki eksenli: ?owner=
// ("All owner teams") ve ?sre= ("All SRE teams"). Okuyucu
// (readExceptionsTeamFilter) ve yazıcı (exceptionsTeamHref) BURADA, tek yerde —
// sayfa okuyucuyu kullanır, gidiş-dönüş testi ikisini birbirine bağlar
// (exceptionsTeamHref.test.ts). Sunucudaki eşi: internal/api/
// copilot_team_exceptions.go exceptionsPageTeamHref (aynı param'lar, aynı
// "son görülme, yeni önce" sırası, aynı eksen kuralı teamExceptionsAxis).
import type { ServiceMetadata } from './types';

export type ExceptionsTeamAxis = 'owner' | 'sre';

/** Exceptions tablosunun sıralama URL anahtarı (AnomaliesPage sortUrlKey) ve "son görülme" değeri. */
export const EXC_SORT_PARAM = 's_exception-inbox-v2';
export const EXC_SORT_LAST_SEEN = 'lastSeen.desc';

/** SAF — Exceptions sayfasının takım süzgecini URL'den okur (sayfanın okuyucusu). */
export function readExceptionsTeamFilter(sp: URLSearchParams): { owner: string; sre: string } {
  return { owner: sp.get('owner') || '', sre: sp.get('sre') || '' };
}

/**
 * SAF — takımın ekseni ve katalogdaki yazımı. Kural sunucunun
 * teamExceptionsAxis'i: takım en az bir serviste owner ise owner (iki eksende
 * de varsa owner — sayfanın varsayılanı), yalnız SRE ise sre, katalogda hiç
 * yoksa owner. Eşleşme büyük/küçük harf duyarsız (teamOptionsCI ve sunucu
 * EqualFold ile aynı); dönen ad katalogdaki yazım, böylece sayfanın açılır
 * listesi seçili görünür.
 */
export function exceptionsTeamAxis(
  catalog: Record<string, ServiceMetadata> | undefined, team: string,
): { axis: ExceptionsTeamAxis; name: string } {
  const want = team.trim();
  const key = want.toLowerCase();
  let sreName = '';
  for (const md of Object.values(catalog ?? {})) {
    const o = (md.ownerTeam ?? '').trim();
    if (o && o.toLowerCase() === key) return { axis: 'owner', name: o };
    const s = (md.sreTeam ?? '').trim();
    if (!sreName && s && s.toLowerCase() === key) sreName = s;
  }
  return sreName ? { axis: 'sre', name: sreName } : { axis: 'owner', name: want };
}

/** SAF — takım süzgeçli, son görülmeye göre sıralı Exceptions adresi. */
export function exceptionsTeamHref(team: string, axis: ExceptionsTeamAxis = 'owner'): string {
  const q = new URLSearchParams();
  q.set(axis, team.trim());
  q.set(EXC_SORT_PARAM, EXC_SORT_LAST_SEEN);
  return `/problems?${q.toString()}`;
}
