import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useServiceRollouts7d } from '@/lib/queries/services';
import { useServiceGitOps } from '@/lib/queries/rollouts';
import { imageRef } from '@/lib/rolloutRow';
import { runtimeLabel, newestRollout } from './detailsProps';
import { api } from '@/lib/api';
import { tsRel, rangeToSince, fmtDateTime } from '@/lib/utils';
import { serviceHref } from '@/lib/serviceHref';
import type { TimeRange } from '@/lib/types';

// DetailsPropsStrip — v0.8.370 (operator-approved mockup): the
// Dynatrace "Properties and tags" counterpart on the Details tab.
// One compact strip: technology/SDK, version + last rollout,
// clusters, pod count. Every datum comes from an EXISTING endpoint
// (runtime fingerprint is server-cached 5 min; the rest are the
// same reads the panels below make) — no new backend. Each group
// renders independently and simply drops out on error/empty, so
// the strip never blocks the tab.

const STALE = 60_000;

function Prop({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'baseline', gap: 6 }}>
      <span style={{ color: 'var(--text3)', fontSize: 10, textTransform: 'uppercase', letterSpacing: '.6px' }}>{k}</span>
      <span style={{ fontSize: 12.5, fontWeight: 600 }}>{children}</span>
    </span>
  );
}

export function DetailsPropsStrip({ service, range }: { service: string; range: TimeRange }) {

  const runtimeQ = useQuery({
    queryKey: ['svc-runtime', service],
    queryFn: () => api.serviceRuntime(service),
    enabled: !!service, staleTime: 5 * 60_000, retry: false,
  });
  // v0.9.360 — panelle AYNI 7g okuma (tek istek, tek cache girdisi).
  // Bonus: Version çipi artık 7 güne bakıyor — varsayılan 30dk pencerede
  // sürüm-değiştiren rollout neredeyse hiç olmadığı için çip hiç çıkmıyordu.
  const rolloutsQ = useServiceRollouts7d(service);
  // v0.10.1257 (operatör: "envanter ve cluster kısmına gerek yok; Java
  // versiyon, image ve son deploy yazsın") — Envs/Clusters çıktı (aşağıdaki
  // Clusters bölümü zaten aynı bilgiyi taşıyor); image + son deploy GitOps
  // sekmesiyle AYNI rollout okumasından (tek cache girdisi).
  const gitopsQ = useServiceGitOps(service);
  // v0.9.261 — was `range.preset` straight through. The preset vocabulary
  // includes '2d' / '7d' / '30d', which Go's time.ParseDuration rejects, so
  // internal/api.parseDuration fell back to this endpoint's 15-MINUTE default:
  // picking a 7-day window quietly showed a quarter hour of instances. It also
  // never handled 'custom'. rangeToSince covers both and can only emit h/m/s.
  const instancesSince = rangeToSince(range).since;
  const instancesQ = useQuery({
    queryKey: ['svc-instances-strip', service, instancesSince],
    queryFn: () => api.serviceInstances(service, instancesSince),
    enabled: !!service, staleTime: STALE, retry: false,
  });

  const rt = runtimeQ.data;
  const tech = rt?.language
    ? `${runtimeLabel(rt.language, rt.runtimeName, rt.runtimeVersion)}${rt.sdkVersion ? ' · otel ' + rt.sdkVersion : ''}`
    : null;
  const lastRollout = useMemo(() => newestRollout(gitopsQ.data?.rollouts.rows ?? []), [gitopsQ.data]);
  const image = lastRollout ? imageRef(lastRollout.image, lastRollout.imageTag) : '';
  const rollouts = rolloutsQ.data?.rollouts ?? [];
  // v0.8.405 — the header chip means "the new code shipped": key it
  // on the last VERSION-CHANGING rollout, not a restart/reschedule.
  const deploys = rollouts.filter(r => r.kind !== 'restart' && r.versionAfter);
  const last = deploys.length ? deploys[deploys.length - 1] : null;
  const version = last?.versionAfter || null;
  // v0.9.374 — "Pods" ŞU AN yaşayanlar: strip'te 28 okuyan operatör güncel
  // replika sayısı anlar; 7g penceresinde bu "hafta boyunca var olmuş her
  // pod"du (günlük deploy'lu 4-replika servis 28+ gösteriyordu). `up`
  // (2dk tazelik) zaten wire'da — sıfır yeni sorgu.
  const pods = instancesQ.data?.filter(i => i.up).length ?? 0;

  if (!tech && !version && !lastRollout && pods === 0) return null;

  const chip: React.CSSProperties = {
    border: '1px solid var(--border)', borderRadius: 3, background: 'var(--bg2)',
    padding: '0 6px', fontSize: 11, color: 'var(--text2)',
  };

  return (
    <div style={{
      display: 'flex', flexWrap: 'wrap', gap: '8px 22px', alignItems: 'center',
      border: '1px solid var(--border)', borderRadius: 6, background: 'var(--bg2)',
      padding: '9px 14px', marginBottom: 14,
    }}>
      {tech && <Prop k="Technology">{tech}</Prop>}
      {version && (
        <Prop k="Version">
          <span className="mono">{version}</span>
          {last && (
            <a href="#deploys" style={{ ...chip, marginLeft: 6, textDecoration: 'none' }}
              title="Recent rollouts panel">
              deployed {tsRel(last.timeUnixNs)}
            </a>
          )}
        </Prop>
      )}
      {image && image !== '—' && (
        <Prop k="Image">
          <span className="mono" title={image}>{image.length > 60 ? '…' + image.slice(-60) : image}</span>
        </Prop>
      )}
      {lastRollout && (
        <Prop k="Son deploy">
          <Link to={serviceHref(service, { tab: 'gitops' })} style={{ ...chip, textDecoration: 'none' }}
            title={`${fmtDateTime(new Date(lastRollout.startedAt))} — GitOps sekmesinde rollout'lar`}>
            {tsRel(lastRollout.startedAt * 1e6)}
          </Link>
        </Prop>
      )}
      {pods > 0 && <Prop k="Pods"><span className="mono">{pods}</span></Prop>}
    </div>
  );
}
