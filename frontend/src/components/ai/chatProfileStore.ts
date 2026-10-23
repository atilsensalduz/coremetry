import { useCallback, useEffect, useMemo, useState } from 'react';
import type { CopilotProfileOption } from '@/lib/types';

// chatProfileStore — v0.10.1138: sohbet model profili seçiminin kullanıcı
// başına kalıcılığı (localStorage, try/catch — özel pencerede/engelli
// depolamada sessizce oturum belleğine düşer) + aynı sekmedeki iki yüzeyin
// (CoSRE penceresi / ✨ Explain çekmecesi sohbeti) eşzamanı.
//
// Seçim yalnız ÇAĞIRANIN rolüne açık profiller içinde geçerlidir
// (/api/copilot/config listesi zaten süzülü gelir); listede olmayan kayıtlı
// seçim (silinmiş profil, rol daraltıldı) sessizce varsayılana ('') döner.
// Sunucu yine de kapıdır: 400/403 gelirse useChatThread onProfileRejected ile
// seçimi buradan temizler.

//
// v0.10.1448 (operatör-bildirimli: "Settings'te varsayılan modeli değiştirdiğimde
// kullanıcının tarayıcısında önceki model kalıyor, CoSRE hâlâ ona gidiyor.")
// Seçim artık o anki VARSAYILANLA birlikte yazılır ({id, def}); config'in
// varsayılanı değişince kayıtlı seçim BAYAT sayılır ve '' (yeni varsayılan)
// olur — kullanıcı yeniden seçerse yeni varsayılana göre damgalanır. Eski
// düz-dize kayıtların damgası yok → bir kez varsayılana döner (operatör
// isteği: herkes varsayılana dönsün).

const KEY_PREFIX = 'cosre.chatProfile.';
const mem = new Map<string, ChatProfileChoice>();
const listeners = new Set<(user: string, id: string) => void>();

/** Kayıtlı seçim: id ('' = varsayılan) + seçim anındaki varsayılan profil
 *  (def; eski kayıtlarda yok). */
export interface ChatProfileChoice { id: string; def?: string }

export function chatProfileKey(user: string | undefined | null): string {
  return KEY_PREFIX + (user || 'anon');
}

/** parseChatProfileChoice — SAF: JSON {id, def} ya da eski düz id dizesi. */
export function parseChatProfileChoice(raw: string | null | undefined): ChatProfileChoice {
  if (!raw) return { id: '' };
  if (raw[0] === '{') {
    try {
      const o = JSON.parse(raw) as { id?: unknown; def?: unknown };
      const id = typeof o.id === 'string' ? o.id : '';
      return typeof o.def === 'string' ? { id, def: o.def } : { id };
    } catch { /* bozuk kayıt → varsayılan */ return { id: '' }; }
  }
  return { id: raw };
}

export function readChatProfileChoice(user: string | undefined | null): ChatProfileChoice {
  const k = chatProfileKey(user);
  try {
    const v = window.localStorage.getItem(k);
    if (v !== null) return parseChatProfileChoice(v);
  } catch { /* depolama kapalı */ }
  return mem.get(k) ?? { id: '' };
}

export function readChatProfile(user: string | undefined | null): string {
  return readChatProfileChoice(user).id;
}

export function writeChatProfile(user: string | undefined | null, id: string, def?: string): void {
  const k = chatProfileKey(user);
  const choice: ChatProfileChoice = id && def !== undefined ? { id, def } : { id }; // '' hep damgasız
  mem.set(k, choice);
  try {
    if (id) window.localStorage.setItem(k, JSON.stringify(choice));
    else window.localStorage.removeItem(k);
  } catch { /* depolama kapalı — bellek kopyası yeter */ }
  for (const l of listeners) l(user || 'anon', id);
}

/** resolveChatProfile — SAF: kayıtlı seçim izinli listede değilse '' (varsayılan).
 *  v0.10.1448 — seçim anındaki varsayılan (def) config'in varsayılanından
 *  (defaultProfile) farklıysa da '': admin varsayılanı değiştirmiş, kullanıcı
 *  yeni varsayılana döner. Damgasız (eski) kayıt da bayattır. */
export function resolveChatProfile(stored: string, profiles: CopilotProfileOption[], defaultProfile?: string, storedDef?: string): string {
  if (!stored || profiles.length < 2) return '';
  if (defaultProfile !== undefined && storedDef !== defaultProfile) return '';
  return profiles.some(p => p.id === stored) ? stored : '';
}

/** activeProfileModel — SAF: rozetteki model adı. Seçim yoksa varsayılan
 *  profilin modeli, o da yoksa config'in genel `model`i. */
export function activeProfileModel(profile: string, profiles: CopilotProfileOption[], defaultProfile: string | undefined, fallback: string | undefined): string {
  const id = profile || defaultProfile || '';
  const p = profiles.find(x => x.id === id);
  return p?.model || fallback || '';
}

// 2026-10-10 — kompakt model hapı etiketi (ModelPicker).
const LABEL_MAX = 12;
// Boyut parçası: 31b, 7B, 1.5b, 270m, 8x7b.
const SIZE_RE = /^\d+(?:\.\d+)?[bmk]$|^\d+x\d+(?:\.\d+)?b$/i;

function clipLabel(s: string): string {
  return s.length > LABEL_MAX ? `${s.slice(0, LABEL_MAX - 1)}…` : s;
}

/** shortModelName — SAF: satıcı öneki (`org/`) atılır; ilk '-' (ya da ':')
 *  öncesi parça + varsa boyut parçası; kuantizasyon/sonek gürültüsü (it, qat,
 *  w4a16, ct, instruct…) düşer. En çok 12 karakter, aşarsa '…'.
 *  "gemma4-31b-it-qat-w4a16-ct" → "gemma4 31b". */
export function shortModelName(model: string): string {
  const base = model.trim().split('/').pop() ?? '';
  const [head = '', ...rest] = base.split(/[-:]/);
  if (!head) return clipLabel(base);
  const size = rest.find(t => SIZE_RE.test(t));
  return clipLabel(size ? `${head} ${size}` : head);
}

/** compactModelLabel — SAF: hap etiketi = profilin görünen adı, yoksa kısaltılmış model adı. */
export function compactModelLabel(profileLabel: string | undefined, model: string): string {
  const name = profileLabel?.trim();
  return name ? clipLabel(name) : shortModelName(model);
}

export interface ChatProfileState {
  /** '' = sunucu varsayılanı / yüzey eşlemesi */
  profile: string;
  setProfile: (id: string) => void;
  /** Rozet: o an cevabı üretecek model. */
  activeModel: string;
}

export function useChatProfile(user: string | undefined | null, profiles: CopilotProfileOption[], defaultProfile: string | undefined, fallbackModel: string | undefined): ChatProfileState {
  const [stored, setStored] = useState<ChatProfileChoice>(() => readChatProfileChoice(user));
  useEffect(() => { setStored(readChatProfileChoice(user)); }, [user]);
  useEffect(() => {
    const me = user || 'anon';
    const l = (u: string) => { if (u === me) setStored(readChatProfileChoice(user)); };
    listeners.add(l);
    return () => { listeners.delete(l); };
  }, [user]);
  const profile = useMemo(() => resolveChatProfile(stored.id, profiles, defaultProfile, stored.def), [stored, profiles, defaultProfile]);
  // v0.10.1448 — bayat kayıt (varsayılan değişti / profil gitti) depodan da
  // silinir; yalnız seçici görünürken (≥2 profil), config gelmeden değil.
  useEffect(() => {
    if (stored.id && profile === '' && profiles.length >= 2) writeChatProfile(user, '');
  }, [stored, profile, profiles.length, user]);
  // Seçim o anki varsayılanla damgalanır; '' (varsayılan) damgasız silinir.
  const setProfile = useCallback((id: string) => writeChatProfile(user, id, id ? defaultProfile : undefined), [user, defaultProfile]);
  const activeModel = activeProfileModel(profile, profiles, defaultProfile, fallbackModel);
  return { profile, setProfile, activeModel };
}
