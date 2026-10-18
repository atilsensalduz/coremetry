// TourProvider.tsx — v0.10.1334 (rehberli tur, R1). AppShell'in tam-kromlu
// dalında <ShortcutsHelp/>'in yanında BİR KEZ mount edilir (kiosk-çıplak ve
// cosre-çıplak dallar onu hiç çizmez; public sayfalar erken döner).
//
// Akış:
//   1. Oto-başlatma adayıysa (sekmede henüz başlamadı + bu tarayıcıda
//      görülmedi + engelli kabuk değil) GET /api/tour/state — aksi hâlde
//      istek bile atılmaz.
//   2. shouldAutoStart (autoStart.ts) → sessionStorage damgası → begin().
//   3. begin(): karşılama karoları bir kez çözülür (queries.ts, mevcut
//      sınırlı uçlar) → TourCtx → engine START. Bağlam turun ortasında
//      değişmez.
//   4. Bitti/Atlandı → localStorage flaş bekçisi + PUT /api/tour/state
//      HEMEN (beklemeden; düşerse bekçi yine korur).
//   5. "Turu yeniden başlat" (restart.ts) → kapılardan bağımsız begin().

import { useCallback, useEffect, useRef } from 'react';
import { useLocation } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { useAuth } from '@/components/AuthProvider';
import { StatTile } from '@/components/ui';
import { useCopilotConfig } from '@/components/ai/useCopilotEnabled';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import { fmtNum } from '@/lib/utils';
import { getRaw, setRaw, getSessionRaw, setSessionRaw, STORAGE_KEYS, tourSeenKey } from '@/lib/storage';
import type { TourStateResponse } from '@/lib/types';
import { TOUR_STEPS, TOUR_TOTAL, TOUR_VERSION, formatStepOf } from './steps';
import { buildTourCtx } from './ctx';
import { hasPrev, indicator, isLastOpen, persistedOutcome, type IndicatorSeg, type TourState } from './engine';
import { isTourBlockedLocation, shouldAutoStart } from './autoStart';
import { loadTourTiles, tourKeys, useTourStateQuery, useTourTiles } from './queries';
import { onTourRestart } from './restart';
import { useTour } from './useTour';
import { TourOverlay } from './TourOverlay';

export function TourProvider() {
  const { user } = useAuth();
  const { pathname, search } = useLocation();
  const qc = useQueryClient();
  const t = useT();
  const copilot = useCopilotConfig(true);
  const hereRef = useRef({ pathname, search });
  hereRef.current = { pathname, search };
  const copilotRef = useRef(false);
  copilotRef.current = !!copilot?.enabled;

  const uid = user?.id ?? '';
  const blocked = isTourBlockedLocation(pathname, search);
  const sessionAutoStarted = getSessionRaw(STORAGE_KEYS.tourAutoStarted) === '1';
  const localSeen = !!uid && getRaw(tourSeenKey(uid)) === '1';
  const candidate = !!uid && !blocked && !sessionAutoStarted && !localSeen;
  const stateQ = useTourStateQuery(candidate);

  const onDone = useCallback((s: TourState) => {
    const status = persistedOutcome(s);
    if (!status || !uid) return;
    setRaw(tourSeenKey(uid), '1');
    void api.putTourState({ status, version: TOUR_VERSION })
      .then(r => {
        qc.setQueryData<TourStateResponse | null>(tourKeys.state, prev =>
          prev ? { ...prev, status, version: TOUR_VERSION, at: r?.at ?? prev.at } : prev);
      })
      .catch(() => { /* PUT düşerse localStorage bekçisi bu tarayıcıda korur; sunucu satırı yazılmamış kalır */ });
  }, [qc, uid]);

  const tour = useTour({ onDone });
  const { state, start } = tour;

  const startingRef = useRef(false);
  const begin = useCallback(async () => {
    if (startingRef.current || state.status === 'running') return;
    startingRef.current = true;
    try {
      const tiles = await loadTourTiles(qc);
      // Konum ve copilot bayrağı AWAIT'TEN SONRA okunur: karo beklenirken
      // kullanıcı gezindiyse returnTo bayat kalıp LOCATION_LEFT tetiklemesin.
      start(buildTourCtx(tiles, hereRef.current, copilotRef.current));
    } finally {
      startingRef.current = false;
    }
  }, [qc, start, state.status]);

  // Oto-başlatma.
  const data = stateQ.data;
  useEffect(() => {
    if (!data || !candidate) return;
    const go = shouldAutoStart({
      enabled: data.enabled, status: data.status, newUser: data.newUser,
      sessionAutoStarted, localSeen, pathname, search,
    });
    if (!go) return;
    setSessionRaw(STORAGE_KEYS.tourAutoStarted, '1');
    void begin();
  }, [data, candidate, sessionAutoStarted, localSeen, pathname, search, begin]);

  // Menüden yeniden başlatma.
  useEffect(() => onTourRestart(() => {
    if (isTourBlockedLocation(hereRef.current.pathname, hereRef.current.search)) return;
    void begin();
  }), [begin]);

  const running = state.status === 'running';
  const tilesQ = useTourTiles(running && TOUR_STEPS[state.index]?.id === 'welcome');
  if (!running || blocked) return null;
  const step = TOUR_STEPS[state.index];
  if (!step) return null;

  const notes: string[] = [];
  const ctx = state.ctx;
  if (step.id === 'welcome' && ctx && ctx.serviceCount === 0) notes.push(t('tour.note.no_services'));
  if (step.id === 'welcome' && ctx && ctx.serviceCount < 0) notes.push(t('tour.welcome.unavailable'));
  if (state.timedOut[step.id]) notes.push(t('tour.note.timeout'));
  else if (tour.target.status === 'waiting') notes.push(t('tour.chrome.loading'));

  const segTitle = (seg: IndicatorSeg) => {
    const st = TOUR_STEPS.find(x => x.id === seg.id);
    const name = st ? t(st.titleKey) : seg.id;
    return seg.reason ? `${name} — ${t(`tour.note.${seg.reason}`)}` : name;
  };

  const tiles = tilesQ.data;
  const showTiles = step.id === 'welcome' && !!tiles && tiles.totalServices > 0;

  return (
    <TourOverlay
      stepKey={step.id}
      stepLabel={formatStepOf(t('tour.chrome.stepOf'), state.index + 1, TOUR_TOTAL)}
      progressLabel={t('tour.chrome.progress')}
      segments={indicator(state)}
      segmentTitle={segTitle}
      title={t(step.titleKey)}
      body={t(step.bodyKey)}
      notes={notes}
      rect={tour.target.status === 'found' ? tour.target.rect : null}
      placement={step.placement}
      dialogLabel={t('tour.chrome.dialog')}
      labels={{ back: t('tour.chrome.back'), next: t('tour.chrome.next'), finish: t('tour.chrome.finish'), skip: t('tour.chrome.skip') }}
      canBack={hasPrev(state)}
      isLast={isLastOpen(state)}
      onBack={tour.back}
      onNext={isLastOpen(state) ? tour.finish : tour.next}
      onSkip={tour.skip}>
      {showTiles && tiles && (
        <div className="tour-card__tiles">
          <StatTile label={t('tour.welcome.tile.services')}>{fmtNum(tiles.totalServices)}</StatTile>
          <StatTile label={t(tiles.spansTopOnly ? 'tour.welcome.tile.spansTop' : 'tour.welcome.tile.spans')}>{fmtNum(tiles.spans)}</StatTile>
          <StatTile label={t('tour.welcome.tile.busiest')}>
            <span className="dt-trunc" title={tiles.busiest?.name}>{tiles.busiest?.name ?? '—'}</span>
          </StatTile>
          <StatTile label={t('tour.welcome.tile.envs')}>{fmtNum(tiles.environments)}</StatTile>
        </div>
      )}
    </TourOverlay>
  );
}
