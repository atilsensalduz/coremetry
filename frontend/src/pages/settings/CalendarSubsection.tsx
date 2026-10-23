import type { AnomalyCalendarConfig, AnomalySpecialDay, AnomalySpecialDayKind } from '@/lib/types';
import { Button } from '@/components/ui';
import { useMemo } from 'react';
import { Combobox } from '@/components/Combobox';
import { Field } from './shared';
import { browserTz } from '@/lib/browserTz';

// CalendarSubsection — v0.10.1483: mevsimsel baseline'ların takvim etkileri
// (anomaly_sensitivity.calendar; Dedektör hassasiyeti bölümü, aynı Kaydet).
//
// Özel gün (tarih ya da aralık, etiket, tür): holiday → baseline önceki tatil
// günlerinden (yetmezse pazar), half-day → önceki yarım günlerden (yetmezse
// cumartesi), month-end-like → önceki ay sonu benzeri günlerden. Tatil ve
// yarım günde YENİ istek hızı düşüşü Problem açmaz: /anomalies'te
// "takvim günü düşüşü" olarak görünür, bildirim gitmez.
// v0.10.1490 — tarihler takvim diliminin (timezone, IANA) yerel günü; boş =
// UTC (eski kurulumlar). Tarayıcının dilimi yalnız ÖNERİLİR (tek tık), alana
// kendiliğinden yazılmaz: ilgisiz bir Kaydet tatil pencerelerini kaydırmasın.
// Satırlar düzenlenebilir bir liste (≤ 200, sunucu kelepçeler) — kayıt listesi
// değil, bu yüzden DataTable değil.

export const CALENDAR_KINDS: { v: AnomalySpecialDayKind; label: string }[] = [
  { v: 'holiday', label: 'Tatil' },
  { v: 'half-day', label: 'Yarım gün' },
  { v: 'month-end-like', label: 'Ay sonu benzeri' },
];

const EMPTY: AnomalyCalendarConfig = { days: [], monthEndAuto: false, monthEndBusinessDays: 2 };

// tzOptions — tarayıcının bildiği IANA adları (Combobox önerisi); Intl eskiyse boş.
function tzOptions(): string[] {
  try {
    const f = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf;
    return f ? f('timeZone') : [];
  } catch {
    return [];
  }
}

export function CalendarSubsection({ value, onChange }: {
  value?: AnomalyCalendarConfig;
  onChange: (v: AnomalyCalendarConfig) => void;
}) {
  const v: AnomalyCalendarConfig = { ...EMPTY, ...value, days: value?.days ?? [] };
  const suggested = browserTz().tz;
  const tz = v.timezone ?? '';
  const zones = useMemo(tzOptions, []);
  const setDay = (i: number, patch: Partial<AnomalySpecialDay>) =>
    onChange({ ...v, days: v.days.map((d, j) => (j === i ? { ...d, ...patch } : d)) });
  const removeDay = (i: number) => onChange({ ...v, days: v.days.filter((_, j) => j !== i) });
  const addDay = () => onChange({ ...v, days: [...v.days, { from: '', label: '', kind: 'holiday' }] });
  return (
    <div style={{ borderTop: '1px solid var(--border)', paddingTop: 16, marginTop: 4 }}>
      <div style={{ fontSize: 13, color: 'var(--text)' }}>Takvim etkileri (özel günler)</div>
      <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 4, lineHeight: 1.5 }}>
        Tatil günü baseline&apos;ı önceki tatil günlerinden kurulur (yeterli örnek yoksa pazar günü
        sınıfı); yarım gün önceki yarım günlerden (yoksa cumartesi); ay sonu benzeri önceki ay sonu
        günlerinden. Tatil ve yarım günde yeni <b>istek hızı düşüşü</b> Problem açmaz ve bildirim
        göndermez; /anomalies&apos;te &laquo;takvim günü düşüşü&raquo; olarak kaydedilir. Hata oranı,
        gecikme ve sıçramalar her gün olduğu gibi çalışır.
      </div>
      <div style={{ marginTop: 10, maxWidth: 420 }}>
        <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end', flexWrap: 'wrap' }}>
          <Field label="Takvim saat dilimi (IANA)">
            <Combobox ariaLabel="Takvim saat dilimi" options={zones} width={260}
              placeholder={suggested ? `UTC (öneri: ${suggested})` : 'UTC'} value={tz}
              onChange={z => onChange({ ...v, timezone: z.trim() || undefined })} />
          </Field>
          {suggested && suggested !== tz && (
            <div style={{ marginBottom: 12 }}>
              <Button size="sm" variant="secondary" onClick={() => onChange({ ...v, timezone: suggested })}>
                Tarayıcının dilimini kullan ({suggested})
              </Button>
            </div>
          )}
        </div>
        <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 4, lineHeight: 1.5 }}>
          Özel günler bu dilimin yerel günü olarak eşleşir (ör. Europe/Istanbul: tatilin ilk 3 saati
          UTC&apos;de önceki güne düşer, yine tatil sayılır); ay sonu iş günleri de bu dilimde hesaplanır.
          Boş = UTC günü (önceki davranış). Mevsimsel baseline&apos;ın saat eşlemesi UTC&apos;de kalır.
          Bilinmeyen ad kaydedilmez.
        </div>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 10 }}>
        {v.days.length === 0 && (
          <div style={{ fontSize: 12, color: 'var(--text3)' }}>Özel gün yok.</div>
        )}
        {v.days.map((d, i) => (
          <div key={i} style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <input type="date" aria-label={`Özel gün ${i + 1} başlangıç`} value={d.from}
              onChange={e => setDay(i, { from: e.target.value })} />
            <span style={{ fontSize: 12, color: 'var(--text3)' }}>–</span>
            <input type="date" aria-label={`Özel gün ${i + 1} bitiş`} value={d.to ?? ''}
              onChange={e => setDay(i, { to: e.target.value || undefined })} />
            <input type="text" aria-label={`Özel gün ${i + 1} etiket`} placeholder="Etiket" maxLength={64}
              value={d.label} onChange={e => setDay(i, { label: e.target.value })} style={{ minWidth: 160 }} />
            <select aria-label={`Özel gün ${i + 1} tür`} value={d.kind}
              onChange={e => setDay(i, { kind: e.target.value as AnomalySpecialDayKind })}>
              {CALENDAR_KINDS.map(k => <option key={k.v} value={k.v}>{k.label}</option>)}
            </select>
            <Button size="sm" variant="ghost-danger" onClick={() => removeDay(i)} aria-label={`Özel gün ${i + 1} sil`}>Sil</Button>
          </div>
        ))}
        <div>
          <Button size="sm" variant="secondary" onClick={addDay}>Özel gün ekle</Button>
        </div>
      </div>
      <label style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 12 }}>
        <input type="checkbox" aria-label="Otomatik ay sonu sınıfı"
          checked={v.monthEndAuto} onChange={e => onChange({ ...v, monthEndAuto: e.target.checked })} />
        <span style={{ fontSize: 13, color: 'var(--text)' }}>Otomatik ay sonu sınıfı</span>
      </label>
      <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 4, marginLeft: 24, lineHeight: 1.5 }}>
        Kapalı (varsayılan). Açıkken ayın son iş günleri (hafta içi, tatil değil) &laquo;ay sonu
        benzeri&raquo; sınıfına girer.
      </div>
      {v.monthEndAuto && (
        <div style={{ marginLeft: 24, marginTop: 8, maxWidth: 260 }}>
          <Field label="Ayın son kaç iş günü (1–5)">
            <input type="number" min={1} max={5} aria-label="Ayın son kaç iş günü"
              value={v.monthEndBusinessDays}
              onChange={e => onChange({ ...v, monthEndBusinessDays: Number(e.target.value) })} />
          </Field>
        </div>
      )}
    </div>
  );
}
