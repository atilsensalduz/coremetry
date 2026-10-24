// ContextPackPanel — CoSRE Context Pack ayarı (2026-10-24; uç v0.10.1494).
// system_settings["sre_agent.contextpack"] — sre_agent formundan AYRI anahtar ve
// AYRI uç (GET/PUT /api/settings/sre/contextpack, admin, PUT audit'li), bu yüzden
// AI SRE formunun altında kendi formu ve kendi Kaydet'i var: tek düğme iki ucu
// yazsaydı biri düştüğünde diğeri yarım kalırdı.
//
// canEdit=false (admin olmayan) → alanlar salt okunur, Kaydet yok. Settings
// bugün admin kapılı; kapı gevşerse bölüm yazma açmaz. Okuma 403/hata →
// SettingsLoadError (boş formu kaydetmek saklı ayarın üstüne yazardı).
import { useId, useMemo, useState, type FormEvent } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button, Field } from '@/components/ui';
import { api } from '@/lib/api';
import { agoTR, tsLong } from '@/lib/utils';
import type { SREContextPackSettings } from '@/lib/types';
import {
  PACK_BASELINES, PACK_MAX_TOKENS, PACK_TOP_ANOMALIES, packDefaultForm, packFormToWire,
  packToForm, toggleBaseline, validatePackForm, type PackForm,
} from './contextPackForm';
import { FlashBox, SectionTitle, SettingsLoadError, SettingsSection, humanize, useSettingsLoad } from './shared';

export function ContextPackPanel({ canEdit }: { canEdit: boolean }) {
  const [form, setForm] = useState<PackForm>(packDefaultForm);
  const [meta, setMeta] = useState<Pick<SREContextPackSettings, 'updatedBy' | 'updatedAt'>>({});
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const blHintId = useId();

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.sreContextPackSettings(),
    s => { setForm(packToForm(s)); setMeta({ updatedBy: s?.updatedBy, updatedAt: s?.updatedAt }); },
  );
  const errors = useMemo(() => validatePackForm(form), [form]);
  const ro = !canEdit || busy;

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!canEdit) return;
    const r = packFormToWire(form);
    if (!r.ok) {
      setFlash({ kind: 'err', text: `${Object.keys(r.errors).length} alan geçersiz — kaydedilmedi.` });
      return;
    }
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putSREContextPackSettings(r.wire);
      setForm(packToForm(saved));
      setMeta({ updatedBy: saved.updatedBy, updatedAt: saved.updatedAt });
      setFlash({ kind: 'ok', text: `Kaydedildi — Context Pack ${saved.enabled ? 'AÇIK' : 'KAPALI'} · ${saved.maxTokens} token · ${saved.baselines.length} referans · ilk ${saved.topAnomalies} anomali. Tüm pod'larda ≤30 sn.` });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const body = loadErr ? <SettingsLoadError error={loadErr} onRetry={retry} />
    : !loaded ? <Spinner label="Context Pack ayarı okunuyor…" />
    : (
      <form onSubmit={save} noValidate data-context-pack>
        <SettingsSection>
          <label className="sre-check">
            <input type="checkbox" checked={form.enabled} disabled={ro}
              onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))} />
            <span>
              <span>Context Pack'i etkinleştir</span>
              <span className="field-hint">Varsayılan: kapalı. Kapalıyken anlatım girdisi bugünkü 4 KB kesimidir.</span>
            </span>
          </label>

          <div className="grid-2 sre-grid">
            <Field label="Token tavanı" inputMode="numeric" value={form.maxTokens} disabled={ro}
              placeholder={String(PACK_MAX_TOKENS.def)} error={errors.maxTokens}
              onChange={e => setForm(f => ({ ...f, maxTokens: e.target.value }))}
              hint={`öncelikli kırpmanın tavanı (≈ 4 bayt/token) · varsayılan ${PACK_MAX_TOKENS.def} · ${PACK_MAX_TOKENS.min}–${PACK_MAX_TOKENS.max}`} />
            <Field label="En çok anomali" inputMode="numeric" value={form.topAnomalies} disabled={ro}
              placeholder={String(PACK_TOP_ANOMALIES.def)} error={errors.topAnomalies}
              onChange={e => setForm(f => ({ ...f, topAnomalies: e.target.value }))}
              hint={`öznenin skor sıralı ilk N anomalisi · varsayılan ${PACK_TOP_ANOMALIES.def} · ${PACK_TOP_ANOMALIES.min}–${PACK_TOP_ANOMALIES.max}`} />
          </div>

          <fieldset className="sso-fieldset" aria-describedby={blHintId} aria-invalid={errors.baselines ? 'true' : undefined}>
            <legend className="field-label">Taban referansları</legend>
            {PACK_BASELINES.map(b => (
              <label key={b.value} className="sre-check is-inline">
                <input type="checkbox" checked={form.baselines.includes(b.value)} disabled={ro}
                  onChange={() => setForm(f => ({ ...f, baselines: toggleBaseline(f.baselines, b.value) }))} />
                <span>{b.label} <span className="field-hint">({b.hint})</span></span>
              </label>
            ))}
            <div id={blHintId} className={errors.baselines ? 'field-error' : 'field-hint'}>
              {errors.baselines ?? 'Varsayılan: önceki pencere + geçen hafta. Her referans RED karşılaştırma satırı ekler.'}
            </div>
          </fieldset>

          <div className="sre-actions">
            {canEdit
              ? <Button type="submit" variant="primary" loading={busy}>Kaydet</Button>
              : <span className="field-hint">Salt okunur — yalnız admin değiştirebilir.</span>}
            {meta.updatedAt ? (
              <span className="field-hint" title={tsLong(meta.updatedAt)}>
                Son değişiklik{meta.updatedBy ? ` ${meta.updatedBy}` : ''} · {agoTR(meta.updatedAt)}
              </span>
            ) : (
              <span className="field-hint">Henüz kaydedilmedi — varsayılanlar geçerli (kapalı).</span>
            )}
          </div>
          {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
        </SettingsSection>
      </form>
    );

  return (
    <section aria-label="Context Pack">
      <SectionTitle>Context Pack</SectionTitle>
      <p className="sso-note">
        Araştırma anlatımına giden deterministik bağlamı zenginleştirir: seçili taban
        referanslarıyla RED karşılaştırması, öznenin en yüksek skorlu anomalileri, hata özeti
        ve deploy etkisi — token tavanına öncelik sırasıyla kırpılarak. Yalnız modelin okuduğu
        metni değiştirir; hipotez skoru bu paketi hiç okumaz (skor aynı kalır).
      </p>
      {body}
    </section>
  );
}
