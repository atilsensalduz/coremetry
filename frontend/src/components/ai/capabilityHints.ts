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

// v0.10.1382 — p1 / deploy satırları: YALNIZ sunucu o grup için seçenek
// getirdiyse (/api/copilot/starters); boş grup = satır yok.
export type CapabilityKind = 'wiki' | 'service' | 'operation' | 'p1' | 'deploy' | 'navigate';

export interface CapabilityItem {
  kind: CapabilityKind;
  /** i18n anahtarı — görünen satır. */
  labelKey: string;
  /** i18n anahtarı — composer'a dolan örnek ( `|` = imleç). */
  promptKey: string;
}

const WIKI: CapabilityItem = { kind: 'wiki', labelKey: 'cosre.cap.wiki', promptKey: 'cosre.cap.wiki.prompt' };
const SERVICE: CapabilityItem = { kind: 'service', labelKey: 'cosre.cap.service', promptKey: 'cosre.cap.service.prompt' };
const OPERATION: CapabilityItem = { kind: 'operation', labelKey: 'cosre.cap.operation', promptKey: 'cosre.cap.operation.prompt' };
const P1: CapabilityItem = { kind: 'p1', labelKey: 'cosre.cap.p1', promptKey: 'cosre.cap.p1.prompt' };
const DEPLOY: CapabilityItem = { kind: 'deploy', labelKey: 'cosre.cap.deploy', promptKey: 'cosre.cap.deploy.prompt' };
const NAVIGATE: CapabilityItem = { kind: 'navigate', labelKey: 'cosre.cap.navigate', promptKey: 'cosre.cap.navigate.prompt' };

/** v0.10.1382 — yetenek satırı → sunucu grubu (wiki/navigate grupsuz). */
const GROUP_OF: Partial<Record<CapabilityKind, CopilotStarterGroupKind>> = {
  service: 'services', operation: 'operations', p1: 'p1', deploy: 'deploys',
};

/** SAF — v0.10.1382: satırın sunucu grubu (öğesi yoksa undefined). */
export function groupForCapability(kind: CapabilityKind, groups: readonly CopilotStarterGroup[]): CopilotStarterGroup | undefined {
  const g = GROUP_OF[kind];
  if (!g) return undefined;
  const hit = groups.find(x => x.kind === g);
  return hit && hit.items.length > 0 ? hit : undefined;
}

/**
 * SAF — görünen satırlar; wiki satırı yalnız `wiki` true iken ve en başta.
 * v0.10.1382 — p1 / deploy satırları yalnız o grupta öğe varken (operasyon
 * satırından sonra, navigate'ten önce).
 */
export function capabilityItems(wiki: boolean, groups: readonly CopilotStarterGroup[] = []): CapabilityItem[] {
  const out: CapabilityItem[] = wiki ? [WIKI] : [];
  out.push(SERVICE, OPERATION);
  if (groupForCapability('p1', groups)) out.push(P1);
  if (groupForCapability('deploy', groups)) out.push(DEPLOY);
  out.push(NAVIGATE);
  return out;
}

/** v0.10.1382 — yerel (son/sabitlenmiş) servis çipi tavanı. */
export const LOCAL_SERVICE_CHIPS_MAX = 2;

/** SAF — servis sorusu (yerel çip ve "başka bir servis…" seçimi; service_health'e yönlenir). */
export function serviceHealthQuestion(name: string): string {
  return `${name} sağlığı nasıl?`;
}

/**
 * SAF — v0.10.1382: tarayıcının sabitlenmiş + son ziyaret edilen servisleri
 * (lib/recentServices.ts) servis satırına DÜZ çip olarak eklenir. Sunucuya
 * gitmez (yalnız bu tarayıcının kolaylığı); sunucu çipinin zaten andığı servis
 * tekrar edilmez; sabitlenmiş önce, ad biçimi uygunsuzsa atlanır.
 */
export function localServiceChips(
  serverItems: readonly CopilotStarter[],
  pinned: readonly string[],
  recent: readonly string[],
  max = LOCAL_SERVICE_CHIPS_MAX,
): CopilotStarter[] {
  const out: CopilotStarter[] = [];
  const seen = new Set<string>();
  for (const raw of [...pinned, ...recent]) {
    if (out.length >= max) break;
    const name = raw.trim();
    if (!name || /[\s:]/.test(name) || seen.has(name)) continue;
    seen.add(name);
    if (serverItems.some(it => it.question.startsWith(name + ' '))) continue;
    out.push({ chip: name, question: serviceHealthQuestion(name), kind: 'service_health' });
  }
  return out;
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
