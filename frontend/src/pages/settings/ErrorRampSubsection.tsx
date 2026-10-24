import type { ErrorRampConfig } from '@/lib/types';
import { Field } from './shared';
import { ERROR_RAMP_BOUNDS, errorRampFieldError, withErrorRampDefaults, type ErrorRampNumKey } from './errorRamp';

// ErrorRampSubsection — v0.10.1518 sürekli teknik hata (5xx) yükselişi
// kuralının vidaları (anomaly_sensitivity.errorRamp; Dedektör hassasiyeti
// bölümü, yaygın yavaşlamanın altında, aynı Kaydet). Kural
// internal/anomaly/error_ramp.go; aynı `anomaly:<servis>:error_rate` satırını
// açar. Aralıklar errorRamp.ts'te (Go Normalize ile birebir, pinli); aralık
// dışı alan satır içinde uyarı gösterir ve Kaydet'i AnomalyTab durdurur.

const FIELDS: { k: ErrorRampNumKey; label: string; step: number; help: string }[] = [
  { k: 'windowBuckets', label: 'Süreklilik (× 5 dk)', step: 1,
    help: 'Oran bu kadar ardışık tamamlanmış 5 dk kovanın HER BİRİNDE eşiğin üstünde kalmalı. 2–24, varsayılan 6.' },
  { k: 'ratioFactor', label: 'Artış katı (× taban medyanı)', step: 0.1,
    help: 'Kova oranı servisin kendi taban medyanının en az bu katı olmalı (ve taban p90’ının üstünde). 1,2–50, varsayılan 2.' },
  { k: 'minRatioPct', label: 'Oran tabanı (%)', step: 0.05,
    help: 'Kova oranı en az bu yüzde olmalı; tabanı %0 olan serviste eşik budur. 0,01–50, varsayılan 0,2.' },
  { k: 'minErrorsPerSec', label: 'Hata hızı tabanı (hata/sn)', step: 0.5,
    help: 'Kova başına en az bu kadar teknik hata/sn (1 = 5 dk’da 300 hata). Düşük hacimli servisi susturan kapı. 0,05–100000, varsayılan 1.' },
];

export function ErrorRampSubsection({ value, onChange }: {
  value?: ErrorRampConfig;
  onChange: (v: ErrorRampConfig) => void;
}) {
  const v = withErrorRampDefaults(value);
  const on = v.enabled;
  const set = (patch: Partial<ErrorRampConfig>) => onChange({ ...v, ...patch });
  const minutes = Number.isFinite(v.windowBuckets) ? v.windowBuckets * 5 : 0;
  return (
    <div style={{ borderTop: '1px solid var(--border)', paddingTop: 16, marginTop: 4 }}>
      <label style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <input type="checkbox" aria-label="Sürekli teknik hata yükselişi"
          checked={on} onChange={e => set({ enabled: e.target.checked })} />
        <span style={{ fontSize: 13, color: 'var(--text)' }}>Sürekli teknik hata (5xx) yükselişi</span>
      </label>
      <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 4, marginLeft: 24, lineHeight: 1.5 }}>
        Açık (varsayılan): <b>teknik hata</b> (HTTP 5xx / sunucu hatası) oranı servisin kendi tabanına
        göre <b>{minutes} dakika</b> boyunca kesintisiz yüksek kalırsa servisin hata oranı problemi açılır
        (aynı <code>error_rate</code> satırı, ikinci bir problem yok). Yakaladığı: yüksek hacimli
        servislerde saatlere yayılan rampa. Ani-sapma kolunun %1 mutlak tabanı bunu gizliyordu &mdash;
        saniyede 1000 istek alan bir gateway&apos;de 8 hata/sn yalnız %0,8&apos;dir.
      </div>
      <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 4, marginLeft: 24, lineHeight: 1.5 }}>
        Yok saydıkları: <b>4xx</b> (iş hatası, teknik hata değil); {minutes} dakikadan <b>kısa
        sıçramalar</b> (onları ani-sapma kolu değerlendirir); <b>olağan günlük yükseliş</b> (taban
        14 günün aynı saatinden gelir, eşik taban p90&apos;ının altına inmez; trafikle orantılı artan hata
        sayısı oranı değiştirmez); <b>düşük hacim</b> (saniyede {v.minErrorsPerSec} hatanın altı).
      </div>
      <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 4, marginLeft: 24, lineHeight: 1.5 }}>
        Karar kaynağı: VictoriaMetrics bağlıysa ve servisin HTTP/gRPC sunucu histogramı varsa
        örneklenmemiş VM serisi (hata kümesi yalnız <code>5..</code>); VM servisi biliyorsa hata oranı
        span&apos;e düşmez. Yoksa örneklenmiş span. Geçmiş bir aralığın kova kova kararı:
        {' '}<code>GET /api/settings/anomaly-red-source/explain</code>.
      </div>
      <div style={{
        display: 'grid', gap: 12, gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))',
        marginTop: 10, marginLeft: 24, opacity: on ? 1 : 0.5,
      }}>
        {FIELDS.map(f => {
          const b = ERROR_RAMP_BOUNDS[f.k];
          const err = errorRampFieldError(f.k, v[f.k]);
          return (
            <Field key={f.k} label={f.label}>
              <input type="number" min={b.min} max={b.max} step={f.step}
                aria-label={`Teknik hata yükselişi: ${f.label}`} aria-invalid={err ? true : undefined}
                disabled={!on} value={Number.isFinite(v[f.k]) ? v[f.k] : ''}
                onChange={e => set({ [f.k]: e.target.value === '' ? NaN : Number(e.target.value) })} />
              {f.k === 'windowBuckets' && !err && (
                <div style={{ fontSize: 11, color: 'var(--text2)', marginTop: 4 }}>= {minutes} dakika</div>
              )}
              {err && <div role="alert" style={{ fontSize: 11, color: 'var(--warn)', marginTop: 4 }}>{err}</div>}
              <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 4, lineHeight: 1.5 }}>{f.help}</div>
            </Field>
          );
        })}
      </div>
    </div>
  );
}
