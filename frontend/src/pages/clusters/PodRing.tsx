// PodRing — pod sayısı halka içinde (v0.10.1161, operatör: "OpenShift'te
// olduğu gibi daire içinde"). PhaseDonut'un satır / karo boyu kardeşi: merkez
// = toplam pod, halka = faz dağılımı. Palet PhaseDonut ile aynı (v0.10.922
// K5): Running nötr, yalnız sapan faz renkli. Faz bilinmiyorsa (KSM yok)
// halka boş iz olarak kalır — "hepsi sağlıklı" iddia edilmez.
// Kök öğe <span>: StatTile düğmesinin içine de girer (düğmeye div konamaz).

import { podRingSegments, podRingLabel } from './podRingSegments';

export function PodRing({ total, running, failing, phaseKnown, size = 30 }: {
  total: number;
  running: number;
  failing: number;
  phaseKnown: boolean;
  size?: number;
}) {
  const stroke = Math.max(3, Math.round(size / 9));
  const r = (size - stroke) / 2;
  const circ = 2 * Math.PI * r;
  const segs = podRingSegments(total, running, failing, phaseKnown);
  const label = podRingLabel(total);
  const tip = phaseKnown ? `${running} / ${total} pod Running` : `${total} pod — faz bilinmiyor`;
  const offsets = segs.map((_, i) => segs.slice(0, i).reduce((a, s) => a + (s.n / total) * circ, 0));
  return (
    <span role="img" aria-label={tip} title={tip}
      style={{ position: 'relative', display: 'inline-flex', width: size, height: size, flexShrink: 0, verticalAlign: 'middle' }}>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} style={{ transform: 'rotate(-90deg)' }} aria-hidden="true">
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--border)" strokeWidth={stroke} />
        {segs.map((s, i) => {
          const len = (s.n / total) * circ;
          return (
            <circle key={s.key} cx={size / 2} cy={size / 2} r={r} fill="none" stroke={s.color} strokeWidth={stroke}
              strokeDasharray={`${len} ${circ - len}`} strokeDashoffset={-offsets[i]} />
          );
        })}
      </svg>
      <span className="mono" style={{
        position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center',
        fontSize: Math.round(size * (label.length <= 2 ? 0.38 : 0.3)), fontWeight: 700, lineHeight: 1, color: 'var(--text)',
      }}>{label}</span>
    </span>
  );
}
