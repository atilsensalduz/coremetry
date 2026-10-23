import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { decideTeamAutoApply, isSameTeam, readTeamOptOut, writeTeamOptOut, teamOptOutKey } from './logsTeam';

// v0.10.1477 — /logs "Takımım" otomatik varsayılanının karar tablosu.
afterEach(() => { vi.unstubAllGlobals(); });

const on = { team: 'orders-team', available: true, fields: ['kubernetes.labels.team-a'] };

describe('decideTeamAutoApply', () => {
  const cases: Array<[string, Parameters<typeof decideTeamAutoApply>[0], ReturnType<typeof decideTeamAutoApply>]> = [
    ['takım + açık + temiz URL → bir kez uygula', { urlHadFilters: false, optedOut: false, done: false, myTeam: on }, { kind: 'apply', team: 'orders-team' }],
    ['yükleniyor → bekle (süzgeçsiz istek yok)', { urlHadFilters: false, optedOut: false, done: false, myTeam: undefined }, { kind: 'pending' }],
    ['açık URL kazanır (yüklenirken bile beklemez)', { urlHadFilters: true, optedOut: false, done: false, myTeam: undefined }, { kind: 'none' }],
    ['açık URL kazanır', { urlHadFilters: true, optedOut: false, done: false, myTeam: on }, { kind: 'none' }],
    ['vazgeçme kalıcı', { urlHadFilters: false, optedOut: true, done: false, myTeam: on }, { kind: 'none' }],
    ['bir kez kuralı', { urlHadFilters: false, optedOut: false, done: true, myTeam: on }, { kind: 'none' }],
    ['takım yok → bugünkü', { urlHadFilters: false, optedOut: false, done: false, myTeam: { ...on, team: ' ' } }, { kind: 'none' }],
    ['alan boş / CH → bugünkü', { urlHadFilters: false, optedOut: false, done: false, myTeam: { ...on, available: false } }, { kind: 'none' }],
    ['my-team hatası → bugünkü', { urlHadFilters: false, optedOut: false, done: false, myTeam: null }, { kind: 'none' }],
  ];
  for (const [name, input, want] of cases) {
    it(name, () => { expect(decideTeamAutoApply(input)).toEqual(want); });
  }
});

describe('isSameTeam', () => {
  it('kırpılmış, harf duyarsız; boş eşleşmez', () => {
    expect(isSameTeam(' Orders-Team ', 'orders-team')).toBe(true);
    expect(isSameTeam('İŞLEM', 'işlem')).toBe(true);
    expect(isSameTeam('a', 'b')).toBe(false);
    expect(isSameTeam('', '')).toBe(false);
  });
});

describe('team opt-out (per user)', () => {
  beforeEach(() => {
    const m = new Map<string, string>();
    vi.stubGlobal('localStorage', {
      getItem: (k: string) => m.get(k) ?? null,
      setItem: (k: string, v: string) => { m.set(k, v); },
      removeItem: (k: string) => { m.delete(k); },
      clear: () => m.clear(),
    });
  });
  it('kullanıcı başına yazılır, okunur ve silinir', () => {
    expect(readTeamOptOut('u1')).toBe(false);
    writeTeamOptOut('u1', true);
    expect(readTeamOptOut('u1')).toBe(true);
    expect(readTeamOptOut('u2')).toBe(false);
    writeTeamOptOut('u1', false);
    expect(readTeamOptOut('u1')).toBe(false);
    expect(teamOptOutKey(undefined)).toContain('anon');
  });
});

describe('team opt-out — storage unavailable', () => {
  it('erişilemeyen depolama atmaz, vazgeçme yok sayılır', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => { throw new Error('denied'); },
      setItem: () => { throw new Error('denied'); },
      removeItem: () => { throw new Error('denied'); },
    });
    expect(() => writeTeamOptOut('u1', true)).not.toThrow();
    expect(readTeamOptOut('u1')).toBe(false);
  });
});
