import { useRef, useState } from 'react';
import { Button } from '@/components/ui';
import { Field } from '@/components/ui/Field';
import { Popover } from '@/components/ui/Popover';
import { NOISE_REASONS } from '@/lib/problemVerdict';
import type { NoiseReason } from '@/lib/types';

// NoiseReasonButton — v0.10.1531 (Problem standardı R3, D5; mockup "Problem
// değil ▾"). "Problem değil" artık gerekçeyle yazılır: tıklayınca küçük bir
// diyalog gerekçe (kapalı küme, Go chstore.NoiseReasons), isteğe bağlı not
// ("Diğer"de zorunlu) ve — yalnız yöneticiye — "kalıcı gizle" sorar.
//
// Gerekçe neden zorunlu bir adım: false positive programında dedektör başına
// isabet ölçümünün etiketi bu. "Eşik fazla hassas" o dedektörün ayarına,
// "yanlış atıf" özne çözümüne işaret eder; gerekçesiz işaret yalnız "gürültü"
// der, neyin düzeltileceğini söylemez.
//
// Etkinin metni diyalogda: görünümden gizler; bildirim susturması ZAMAN
// SINIRLI (sunucu noiseMuteHours, varsayılan 7 gün) ve öncelik yükselir / etki
// 2 katına çıkarsa bildirim yine gelir. Kalıcı gizleme bu sınırı kaldırır.
export interface NoiseReasonChoice {
  reason: NoiseReason;
  note: string;
  permanent: boolean;
}

export function NoiseReasonButton({ label, onConfirm, active, disabled, loading, isAdmin, size = 'sm', ariaLabel }: {
  label: string;
  onConfirm: (c: NoiseReasonChoice) => void;
  active?: boolean;
  disabled?: boolean;
  loading?: boolean;
  isAdmin: boolean;
  size?: 'sm' | 'md';
  ariaLabel?: string;
}) {
  const anchorRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState<NoiseReason | ''>('');
  const [note, setNote] = useState('');
  const [permanent, setPermanent] = useState(false);
  const noteNeeded = reason === 'other' && note.trim() === '';
  const confirm = () => {
    if (!reason || noteNeeded) return;
    onConfirm({ reason, note: note.trim(), permanent: isAdmin && permanent });
    setOpen(false);
  };
  return (
    <>
      <Button ref={anchorRef} size={size} variant="secondary" active={active}
        aria-haspopup="dialog" aria-expanded={open} aria-label={ariaLabel}
        loading={loading} disabled={disabled} onClick={() => setOpen(o => !o)}>
        {label} ▾
      </Button>
      <Popover anchorRef={anchorRef} open={open} onClose={() => setOpen(false)} kind="dialog"
        ariaLabel="Problem değil — gerekçe" width={300}>
        <div className="nr-pop" onClick={e => e.stopPropagation()}>
          <fieldset className="nr-reasons">
            <legend className="field-label">Neden problem değil?</legend>
            {NOISE_REASONS.map(r => (
              <label key={r.id} className="nr-reason">
                <input type="radio" name="noise-reason" value={r.id}
                  checked={reason === r.id} onChange={() => setReason(r.id)} />
                <span><b>{r.label}</b> <span className="field-hint">— {r.hint}</span></span>
              </label>
            ))}
          </fieldset>
          <Field label={reason === 'other' ? 'Not (zorunlu)' : 'Not (isteğe bağlı)'} value={note} maxLength={280}
            onChange={e => setNote(e.target.value)} error={noteNeeded ? '“Diğer” için kısa bir not yazın' : undefined} />
          {isAdmin && (
            <label className="nr-reason">
              <input type="checkbox" checked={permanent} onChange={e => setPermanent(e.target.checked)} />
              <span><b>Kalıcı gizle</b> <span className="field-hint">— bildirim süresiz susar (yalnız yönetici)</span></span>
            </label>
          )}
          <div className="field-hint">
            Satırı listeden gizler. Bildirim {permanent && isAdmin ? 'süresiz' : '7 gün'} susar
            {permanent && isAdmin ? '.' : '; öncelik yükselir ya da etki 2 katına çıkarsa yine gelir.'} Geri alınabilir.
          </div>
          <Button variant="primary" size="sm" disabled={!reason || noteNeeded} onClick={confirm}>
            Problem değil olarak işaretle
          </Button>
        </div>
      </Popover>
    </>
  );
}
