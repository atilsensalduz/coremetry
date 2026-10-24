import { describe, expect, it } from 'vitest';
import { ERROR_RAMP_BOUNDS, ERROR_RAMP_DEFAULTS, errorRampErrors, errorRampFieldError, withErrorRampDefaults } from './errorRamp';

// errorRamp — v0.10.1518 kuralının form çekirdeği. Aralıklar Go
// NormalizeErrorRamp ile birebir (Go tarafında ayrıca pin testi var); burada
// sınırların KAPALI aralık olduğu ve varsayılanların aralık içinde olduğu çivili.
describe('errorRamp form çekirdeği', () => {
  it('varsayılanlar Go DefaultErrorRamp ile aynı ve aralık içinde', () => {
    expect(ERROR_RAMP_DEFAULTS).toEqual({ enabled: true, windowBuckets: 6, ratioFactor: 2, minRatioPct: 0.2, minErrorsPerSec: 1 });
    expect(errorRampErrors(ERROR_RAMP_DEFAULTS)).toEqual({});
  });
  it('sınırlar kapalı aralık: tam sınır geçerli, bir adım ötesi değil', () => {
    for (const [k, b] of Object.entries(ERROR_RAMP_BOUNDS) as [keyof typeof ERROR_RAMP_BOUNDS, { min: number; max: number }][]) {
      expect(errorRampFieldError(k, b.min)).toBeNull();
      expect(errorRampFieldError(k, b.max)).toBeNull();
      expect(errorRampFieldError(k, b.min - 0.001)).not.toBeNull();
      expect(errorRampFieldError(k, b.max + 0.001)).not.toBeNull();
    }
  });
  it('NaN / Infinity / kesirli kova reddedilir', () => {
    expect(errorRampFieldError('minRatioPct', NaN)).toBe('Sayı girin.');
    expect(errorRampFieldError('ratioFactor', Infinity)).toBe('Sayı girin.');
    expect(errorRampFieldError('windowBuckets', 3.5)).toBe('Tam sayı olmalı.');
  });
  it('withErrorRampDefaults: eksik bölüm varsayılan, enabled yok = açık, false korunur', () => {
    expect(withErrorRampDefaults(undefined)).toEqual(ERROR_RAMP_DEFAULTS);
    expect(withErrorRampDefaults({ windowBuckets: 4, ratioFactor: 2, minRatioPct: 0.2, minErrorsPerSec: 1 }).enabled).toBe(true);
    expect(withErrorRampDefaults({ enabled: false, windowBuckets: 4, ratioFactor: 2, minRatioPct: 0.2, minErrorsPerSec: 1 }).enabled).toBe(false);
  });
  it('kapalı bölüm de doğrulanır (sunucu yine varsayılana döndürürdü)', () => {
    expect(Object.keys(errorRampErrors({ enabled: false, windowBuckets: 1, ratioFactor: 2, minRatioPct: 0.2, minErrorsPerSec: 1 }))).toEqual(['windowBuckets']);
  });
});
