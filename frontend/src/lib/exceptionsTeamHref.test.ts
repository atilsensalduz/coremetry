import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  EXC_SORT_LAST_SEEN, EXC_SORT_PARAM, exceptionsTeamAxis, exceptionsTeamHref, readExceptionsTeamFilter,
} from './exceptionsTeamHref';
import type { ServiceMetadata } from './types';

// v0.10.1515 (operatör 2026-10-24) — CoSRE exceptions kartının takım seçicisi
// Exceptions sayfasını takım süzgeçli açar. Gidiş-dönüş: yazıcının ürettiği
// adres, sayfanın KENDİ okuyucusuyla (AnomaliesPage readExceptionsTeamFilter)
// aynı takımı geri verir; sayfa o okuyucuyu gerçekten kullanır (kaynak pini).

const params = (href: string) => new URL(href, 'http://coremetry.example.test').searchParams;

const CAT: Record<string, ServiceMetadata> = {
  'orders-api': { service: 'orders-api', ownerTeam: 'Orders-Team', sreTeam: 'platform-sre' },
  'billing-api': { service: 'billing-api', ownerTeam: 'billing', sreTeam: 'Orders-Team' },
  'batch-manager': { service: 'batch-manager', sreTeam: 'Batch SRE' },
};

describe('exceptionsTeamHref', () => {
  it('gidiş-dönüş: owner / sre ekseni, özel karakterli ad', () => {
    for (const [team, axis] of [['orders-team', 'owner'], ['platform-sre', 'sre'], ['Batch SRE & ops/1', 'sre'], ['ç takım+', 'owner']] as const) {
      const href = exceptionsTeamHref(team, axis);
      expect(href.startsWith('/problems?')).toBe(true);
      const got = readExceptionsTeamFilter(params(href));
      expect(got).toEqual(axis === 'owner' ? { owner: team, sre: '' } : { owner: '', sre: team });
      expect(params(href).get(EXC_SORT_PARAM)).toBe(EXC_SORT_LAST_SEEN);
    }
    expect(exceptionsTeamHref(' orders-team ')).toBe('/problems?owner=orders-team&s_exception-inbox-v2=lastSeen.desc');
  });

  it('okuyucu: param yoksa boş', () => {
    expect(readExceptionsTeamFilter(new URLSearchParams(''))).toEqual({ owner: '', sre: '' });
  });

  it('eksen: owner önce (iki eksende de varsa owner), yalnız SRE ise sre, katalogda yoksa owner; yazım katalogdan', () => {
    expect(exceptionsTeamAxis(CAT, 'orders-team')).toEqual({ axis: 'owner', name: 'Orders-Team' });
    expect(exceptionsTeamAxis(CAT, 'PLATFORM-SRE')).toEqual({ axis: 'sre', name: 'platform-sre' });
    expect(exceptionsTeamAxis(CAT, 'batch sre')).toEqual({ axis: 'sre', name: 'Batch SRE' });
    expect(exceptionsTeamAxis(CAT, ' unknown-team ')).toEqual({ axis: 'owner', name: 'unknown-team' });
    expect(exceptionsTeamAxis(undefined, 'x')).toEqual({ axis: 'owner', name: 'x' });
  });

  it('Exceptions sayfası takım süzgecini bu okuyucuyla okur', () => {
    const page = readFileSync(resolve(__dirname, '../features/anomalies/AnomaliesPage.tsx'), 'utf8');
    expect(page).toContain('readExceptionsTeamFilter(searchParams)');
    expect(page).toContain(`sortUrlKey: '${EXC_SORT_PARAM}'`);
  });
});
