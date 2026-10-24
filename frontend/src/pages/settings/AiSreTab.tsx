// AiSreTab — v0.10.1286 (AI SRE ayar ekranı). system_settings["sre_agent"]
// (v0.10.1282) için admin formu + canlı durum. Settings alanı yalnız admin'e
// açık (Settings.tsx kapısı); GET/PUT /api/settings/sre de admin, PUT
// audit'li (settings.sre_agent.update).
//
// Form ↔ tel çevirisi ve Go NormalizeSettings sınırlarının aynası saf
// modülde (sreSettingsForm.ts): aralık dışı değer alanın altında hata olarak
// görünür ve PUT gitmez — sunucu sessizce tavana kırpardı.
import { useId, useMemo, useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Spinner } from '@/components/Spinner';
import { Button, Field } from '@/components/ui';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries';
import { agoTR, tsLong } from '@/lib/utils';
import type { SRESettings } from '@/lib/types';
import { AiSreStatusPanel } from './AiSreStatusPanel';
import { ContextPackPanel } from './ContextPackPanel'; // 2026-10-24
import { useAuthRole } from '@/components/AuthProvider';
import {
  SRE_NUM_BOUNDS, SRE_PRIORITIES, sreDefaultForm, sreFormToWire, sreSavedSummaryTR,
  sreToForm, togglePriority, validateSREForm, type SREForm, type SRENumKey,
} from './sreSettingsForm';
import { ConfigStatusBanner, FlashBox, SectionTitle, SettingsLoadError, SettingsSection, humanize, useSettingsLoad } from './shared';

const PRIORITY_HINT: Record<string, string> = {
  P1: 'şimdi',
  P2: 'bugün',
  P3: 'uygun zamanda',
};

export function AiSreTab() {
  const [form, setForm] = useState<SREForm>(sreDefaultForm);
  const [meta, setMeta] = useState<Pick<SRESettings, 'updatedBy' | 'updatedAt'>>({});
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const qc = useQueryClient();
  const prioHintId = useId();
  const role = useAuthRole();

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.sreSettings(),
    s => { setForm(sreToForm(s)); setMeta({ updatedBy: s.updatedBy, updatedAt: s.updatedAt }); },
  );
  const errors = useMemo(() => validateSREForm(form), [form]);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    const r = sreFormToWire(form);
    if (!r.ok) {
      setFlash({ kind: 'err', text: `${Object.keys(r.errors).length} alan geçersiz — kaydedilmedi.` });
      return;
    }
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putSRESettings(r.wire);
      setForm(sreToForm(saved));
      setMeta({ updatedBy: saved.updatedBy, updatedAt: saved.updatedAt });
      setFlash({ kind: 'ok', text: sreSavedSummaryTR(saved) });
      await qc.invalidateQueries({ queryKey: keys.sre.all });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const setNum = (k: SRENumKey) => (e: { target: { value: string } }) =>
    setForm(f => ({ ...f, nums: { ...f.nums, [k]: e.target.value } }));

  const header = (
    <>
      <h2 className="sso-title">AI SRE</h2>
      <p className="sso-note">
        Açık problemleri deterministik okumalarla araştıran ajan. Yalnız öneri üretir —
        problem, öncelik ya da kök neden tablolarına yazmaz. Kuyruğu
        COREMETRY_MODE=sre (ya da all) rolü tüketir.
      </p>
    </>
  );

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner label="Ayar okunuyor…" />;

  return (
    <div className="settings-pane">
      {header}
      <AiSreStatusPanel />
      <ConfigStatusBanner label={form.enabled ? 'AÇIK' : 'KAPALI'}>
        {!form.enabled
          ? 'Kapalı — üretici kuyruğa yazmaz, drain yeni iş almaz, "Araştır" düğmesi gizlenir.'
          : form.priorities.length === 0
            ? 'Açık — otomatik kuyruk kapalı; yalnız elle "Araştır".'
            : `Açık — ${form.priorities.join(', ')} problemleri otomatik araştırılır.`}
      </ConfigStatusBanner>
      <form onSubmit={save} noValidate>
        <SettingsSection>
          <label className="sre-check">
            <input type="checkbox" checked={form.enabled} disabled={busy}
              onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))} />
            <span>
              <span>AI SRE ajanını etkinleştir</span>
              <span className="field-hint">Varsayılan: açık. Kapatmak mevcut kayıtları silmez.</span>
            </span>
          </label>

          <SectionTitle>Otomatik kuyruk</SectionTitle>
          <fieldset className="sso-fieldset" aria-describedby={prioHintId}>
            <legend className="field-label">Otomatik araştırılan öncelikler</legend>
            {SRE_PRIORITIES.map(p => (
              <label key={p} className="sre-check is-inline">
                <input type="checkbox" checked={form.priorities.includes(p)} disabled={busy}
                  onChange={() => setForm(f => ({ ...f, priorities: togglePriority(f.priorities, p) }))} />
                <span>{p} <span className="field-hint">({PRIORITY_HINT[p]})</span></span>
              </label>
            ))}
            <div id={prioHintId} className="field-hint">
              Varsayılan: P1, P2. Hiçbiri seçili değilse otomatik kuyruk kapalı — elle "Araştır" yine çalışır.
            </div>
          </fieldset>

          <SectionTitle>Tavanlar</SectionTitle>
          <div className="grid-2 sre-grid">
            {SRE_NUM_BOUNDS.map(b => (
              <Field key={b.key} label={b.label} inputMode="numeric" value={form.nums[b.key]}
                onChange={setNum(b.key)} disabled={busy} placeholder={String(b.def)}
                error={errors[b.key]}
                hint={`${b.hint} · varsayılan ${b.def} · ${b.min}–${b.max}`} />
            ))}
          </div>

          <div className="sre-actions">
            <Button type="submit" variant="primary" loading={busy}>Kaydet</Button>
            {meta.updatedAt ? (
              <span className="field-hint" title={tsLong(meta.updatedAt)}>
                Son değişiklik{meta.updatedBy ? ` ${meta.updatedBy}` : ''} · {agoTR(meta.updatedAt)}
              </span>
            ) : (
              <span className="field-hint">Henüz kaydedilmedi — varsayılanlar geçerli.</span>
            )}
          </div>
          {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
        </SettingsSection>
      </form>
      {/* Ayrı anahtar + ayrı uç (sre_agent.contextpack) — kendi formu. Rol
          bilinmiyorsa (AuthProvider dışı) Settings'in admin kapısı geçerli. */}
      <ContextPackPanel canEdit={role === undefined || role === 'admin'} />
    </div>
  );
}
