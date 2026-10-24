import type { ErrorRampConfig } from '@/lib/types';

// errorRamp — v0.10.1518 sürekli teknik hata (5xx) yükselişi kuralının
// (anomaly_sensitivity.errorRamp) form çekirdeği: varsayılanlar, aralıklar ve
// kayıt öncesi doğrulama. SAF — ErrorRampSubsection ve AnomalyTab'ın Kaydet'i
// buradan okur.
//
// ARALIKLAR chstore.NormalizeErrorRamp İLE BİREBİR (internal/chstore/
// anomaly_sensitivity_error_ramp.go errRamp*Lo/Hi/Def). Sunucu aralık dışı
// değeri KELEPÇELEMEZ, sessizce VARSAYILANA döndürür — operatör 0.1× yazıp
// kaydettiğinde 2× kaydedildiğini fark etmezdi. Bu yüzden form aralık dışını
// Kaydet'ten ÖNCE reddeder. Go tarafı bu dosyayı okuyup sayıları çiviler
// (anomaly_sensitivity_error_ramp_ui_pin_test.go): biri değişirse diğeri de.

export const ERROR_RAMP_DEFAULTS: Required<ErrorRampConfig> = {
  enabled: true, windowBuckets: 6, ratioFactor: 2, minRatioPct: 0.2, minErrorsPerSec: 1,
};

export type ErrorRampNumKey = Exclude<keyof ErrorRampConfig, 'enabled'>;

export const ERROR_RAMP_BOUNDS: Record<ErrorRampNumKey, { min: number; max: number; int?: boolean }> = {
  windowBuckets: { min: 2, max: 24, int: true },
  ratioFactor: { min: 1.2, max: 50 },
  minRatioPct: { min: 0.01, max: 50 },
  minErrorsPerSec: { min: 0.05, max: 100000 },
};

// withErrorRampDefaults — bölümü taşımayan (eski backend) blob için doldurur.
export function withErrorRampDefaults(v?: ErrorRampConfig): Required<ErrorRampConfig> {
  return { ...ERROR_RAMP_DEFAULTS, ...v, enabled: v?.enabled !== false };
}

// errorRampFieldError — tek alanın aralık hatası (Türkçe), geçerliyse null.
export function errorRampFieldError(k: ErrorRampNumKey, v: number): string | null {
  const b = ERROR_RAMP_BOUNDS[k];
  if (!Number.isFinite(v)) return 'Sayı girin.';
  if (b.int && !Number.isInteger(v)) return 'Tam sayı olmalı.';
  if (v < b.min || v > b.max) return `${fmt(b.min)}–${fmt(b.max)} aralığında olmalı.`;
  return null;
}

// errorRampErrors — kayıt engelleyen alan hataları; boş = kaydedilebilir.
// Kural kapalıyken de doğrulanır: kapalı bölümün aralık dışı değeri sunucuda
// yine varsayılana dönerdi.
export function errorRampErrors(v?: ErrorRampConfig): Partial<Record<ErrorRampNumKey, string>> {
  const c = withErrorRampDefaults(v);
  const out: Partial<Record<ErrorRampNumKey, string>> = {};
  for (const k of Object.keys(ERROR_RAMP_BOUNDS) as ErrorRampNumKey[]) {
    const e = errorRampFieldError(k, c[k]);
    if (e) out[k] = e;
  }
  return out;
}

function fmt(n: number): string {
  return n.toLocaleString('tr-TR', { maximumFractionDigits: 2 });
}
