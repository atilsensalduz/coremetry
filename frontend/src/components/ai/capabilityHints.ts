// capabilityHints — v0.10.1128 (operatör): CoSRE karşılamasının "neler
// yapabilirim" ipucu. Boş sohbette, "Sana nasıl yardımcı olabilirim?"in
// altında 3–4 kısa satır; her satır composer'ı ÖRNEK bir soruyla DOLDURUR
// (göndermez) — operatör ifade kalıbını görerek öğrenir.
//
// Wiki satırı YALNIZ sohbet bu kullanıcıya wiki'den cevap verebiliyorsa
// (/api/copilot/config `wiki` bayrağı; sunucudaki kapı chatWikiAvailable).
// Örnek metinler i18n kataloğunda; `|` imlecin konacağı yeri işaretler
// (servis/operasyon adını operatör yazar — prefillEndpoint ile aynı desen).
//
// Dil (prod hatası, v0.10.1130): CoSRE sohbet yüzeyi TÜRKÇE-öncelikli — karşılama
// ("Merhaba", "Sana nasıl yardımcı olabilirim?"), başlangıç çipleri, şablon
// çipi CopilotChat'te sabit Türkçe. İpuçları useT ile UI dilinde (varsayılan
// EN) çizilince karşılama karışık dilli görünüyordu. Bu yüzden ipucu satırları,
// ipucu title'ı ve wiki çipi UI dilinden BAĞIMSIZ, sohbetin diliyle (tCosre)
// çözülür. EN metinler katalogda durur — sohbet-geneli i18n gelince
// COSRE_LANG yerine useT'ye geçilir; o güne kadar burada KULLANILMAZ.

import { t, type Lang } from '@/lib/i18n';
import type { CopilotStarter, CopilotStarterGroup, CopilotStarterGroupKind } from '@/lib/types';
import type { ChatSendOpts } from './useChatThread';

/** CoSRE sohbet yüzeyinin dili — karşılama metinleriyle aynı (sabit Türkçe). */
export const COSRE_LANG: Lang = 'tr';

/** Sohbetin diliyle (UI dilinden bağımsız) katalog araması. */
export function tCosre(key: string): string {
  return t(key, COSRE_LANG);
}

// v0.10.1382 — p1 satırı: YALNIZ sunucu o grup için seçenek getirdiyse
// (/api/copilot/starters); boş grup = satır yok.
// v0.10.1402 (operatör: "Alttaki takımımın servisleri exceptionları yukarıda
// olsun. Takım seçilebilsin. Son deploy etkisini kaldırabiliriz.") — `team`
// (Takımım kartı) EN BAŞTA ve her zaman; `deploy` satırı kalktı.
export type CapabilityKind = 'team' | 'wiki' | 'service' | 'operation' | 'p1' | 'navigate';

export interface CapabilityItem {
  kind: CapabilityKind;
  /** i18n anahtarı — görünen satır. */
  labelKey: string;
  /** i18n anahtarı — composer'a dolan örnek ( `|` = imleç). */
  promptKey: string;
}

const TEAM: CapabilityItem = { kind: 'team', labelKey: 'cosre.cap.team', promptKey: 'cosre.cap.team.prompt' };
const WIKI: CapabilityItem = { kind: 'wiki', labelKey: 'cosre.cap.wiki', promptKey: 'cosre.cap.wiki.prompt' };
const SERVICE: CapabilityItem = { kind: 'service', labelKey: 'cosre.cap.service', promptKey: 'cosre.cap.service.prompt' };
const OPERATION: CapabilityItem = { kind: 'operation', labelKey: 'cosre.cap.operation', promptKey: 'cosre.cap.operation.prompt' };
const P1: CapabilityItem = { kind: 'p1', labelKey: 'cosre.cap.p1', promptKey: 'cosre.cap.p1.prompt' };
const NAVIGATE: CapabilityItem = { kind: 'navigate', labelKey: 'cosre.cap.navigate', promptKey: 'cosre.cap.navigate.prompt' };

/**
 * v0.10.1382 — yetenek satırı → sunucu grubu (wiki/navigate grupsuz).
 * v0.10.1390 — servis/operasyon satırlarının grubu yok: altlarında yalnız
 * arama kutusu (CosreCapabilities SEARCH_ROWS).
 * v0.10.1402 — deploys grubu kalktı; yalnız p1.
 */
const GROUP_OF: Partial<Record<CapabilityKind, CopilotStarterGroupKind>> = {
  p1: 'p1',
};

/** SAF — v0.10.1382: satırın sunucu grubu (öğesi yoksa undefined). */
export function groupForCapability(kind: CapabilityKind, groups: readonly CopilotStarterGroup[]): CopilotStarterGroup | undefined {
  const g = GROUP_OF[kind];
  if (!g) return undefined;
  const hit = groups.find(x => x.kind === g);
  return hit && hit.items.length > 0 ? hit : undefined;
}

/**
 * SAF — görünen satırlar; wiki satırı yalnız `wiki` true iken.
 * v0.10.1382 — p1 satırı yalnız o grupta öğe varken (operasyon satırından
 * sonra, navigate'ten önce).
 * v0.10.1402 — Takımım kartı her zaman EN BAŞTA (wiki'den de önce); deploy
 * satırı kalktı.
 */
export function capabilityItems(wiki: boolean, groups: readonly CopilotStarterGroup[] = []): CapabilityItem[] {
  const out: CapabilityItem[] = [TEAM];
  if (wiki) out.push(WIKI);
  out.push(SERVICE, OPERATION);
  if (groupForCapability('p1', groups)) out.push(P1);
  out.push(NAVIGATE);
  return out;
}

/**
 * v0.10.1402 — Takımım kartının üç eylem çipi (eskiden kartların altındaki
 * gevşek çip satırındaki "Takımımın …" çipleri; oradan kaldırıldı). Etiket
 * i18n anahtarı (tCosre), gönderilen cümle teamCardQuestion.
 */
export type TeamAsk = 'services' | 'exceptions' | 'topErrors';
export const TEAM_ASKS: readonly { kind: TeamAsk; labelKey: string }[] = [
  { kind: 'services', labelKey: 'cosre.team.ask.services' },
  { kind: 'exceptions', labelKey: 'cosre.team.ask.exceptions' },
  { kind: 'topErrors', labelKey: 'cosre.team.ask.topErrors' },
];

/**
 * SAF — v0.10.1402: Takımım çipinin gönderdiği cümle. Takım seçiliyse adı
 * geçen takım (team_services / team_exceptions / team_top_errors), değilse
 * iyelik (my_services / my_exceptions / my_top_errors — sunucu takımı
 * kimlikten çözer). İkisi de LLM'siz yönlenir: copilot_team_card_test.go
 * TestTeamCardQuestionsRoute bu kalıpları birebir pinler.
 */
export function teamCardQuestion(kind: TeamAsk, team: string): string {
  const t = team.trim();
  const who = t ? `${t} takımının` : 'Takımımın';
  switch (kind) {
    case 'services': return `${who} servisleri nasıl?`;
    case 'exceptions': return `${who} exception'ları?`;
    case 'topErrors': return `${who} en hatalı servisleri?`;
  }
}

/**
 * SAF — v0.10.1402 (operatör: "Takımı da seçilebilir yapabiliriz, oradan
 * devam eder."): takım seçicide bir takım seçmek bu cümleyi HEMEN gönderir →
 * team_services (v0.10.1397 takım özeti: servis tablosu + son exception
 * grupları + "Özellikle kontrol etmek istediğin bir servis var mı?" + servis
 * çipleri); sohbet oradan devam eder.
 */
export function teamPickQuestion(team: string): string {
  return teamCardQuestion('services', team);
}

/**
 * v0.10.1402 — son seçilen takım, izleyici başına (yalnız kolaylık; yoksa
 * sunucunun çözdüğü takım, o da yoksa boş = "Takımımın …"). Depolama
 * erişilemezse (gizli pencere, engelli site verisi) sessizce yok sayılır.
 */
export const COSRE_TEAM_STORAGE_KEY = 'coremetry.cosre.team';

export function readStoredTeam(): string | null {
  try {
    const v = localStorage.getItem(COSRE_TEAM_STORAGE_KEY);
    return v && v.trim() ? v.trim() : null;
  } catch {
    return null;
  }
}

export function writeStoredTeam(team: string): void {
  try {
    const t = team.trim();
    if (t) localStorage.setItem(COSRE_TEAM_STORAGE_KEY, t);
    else localStorage.removeItem(COSRE_TEAM_STORAGE_KEY);
  } catch {
    // kolaylık — depolanamazsa seçim yalnız bu oturumda kalır
  }
}

/**
 * SAF — servis arama kutusunun seçimi → gönderilen cümle (service_health'e
 * LLM'siz yönlenir; copilot_starters_test TestStarterQuestionsRoute).
 */
export function serviceHealthQuestion(name: string): string {
  return `${name} sağlığı nasıl?`;
}

/**
 * SAF — v0.10.1390: operasyon arama kutusunun seçimi → gönderilen cümle.
 * Sohbet ön-getirmesinde kataloğa BİREBİR tek eşleşir (aynı test pinler);
 * şablon satırının (cosre.cap.operation.prompt) doldurduğu kalıpla aynı.
 */
export function operationErrorQuestion(op: string): string {
  return `${op} operasyonu neden hata veriyor?`;
}

/** SAF — v0.10.1382: çipin yapısal kapsamı → send ikinci argümanı (followupSendOpts deseni). */
export function starterSendOpts(st: CopilotStarter): ChatSendOpts | undefined {
  return st.scope ? { parsed: { scope: st.scope } } : undefined;
}

/** SAF — `|` imleç işaretini söker; işaret yoksa imleç metnin sonunda. */
export function splitCaret(s: string): { text: string; caret: number } {
  const i = s.indexOf('|');
  if (i < 0) return { text: s, caret: s.length };
  return { text: s.slice(0, i) + s.slice(i + 1), caret: i };
}
