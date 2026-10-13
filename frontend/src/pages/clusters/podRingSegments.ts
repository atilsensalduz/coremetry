// podRingSegments — PodRing'in saf çekirdeği (v0.10.1161): halka dilimleri + merkez etiketi.

export interface PodRingSegment { key: 'running' | 'pending' | 'failed'; n: number; color: string }

/** SAF: halka dilimleri. Toplamı aşan / negatif girdiler kırpılır. */
export function podRingSegments(total: number, running: number, failing: number, phaseKnown: boolean): PodRingSegment[] {
  if (!phaseKnown || total <= 0) return [];
  const run = Math.min(Math.max(running, 0), total);
  const fail = Math.min(Math.max(failing, 0), total - run);
  const segs: PodRingSegment[] = [
    { key: 'running', n: run, color: 'var(--text3)' },
    { key: 'pending', n: total - run - fail, color: 'var(--warn)' },
    { key: 'failed', n: fail, color: 'var(--err)' },
  ];
  return segs.filter(s => s.n > 0);
}

/** SAF: merkez etiketi — dört haneden itibaren kısaltılır (halkaya sığsın). */
export function podRingLabel(total: number): string {
  if (total < 1000) return String(total);
  const k = total / 1000;
  return `${k >= 10 ? Math.round(k) : Math.round(k * 10) / 10}k`;
}
