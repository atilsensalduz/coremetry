import { useCallback, useState } from 'react';
import { apiErrorText } from '@/lib/api';

// useRuleSaveError — v0.10.1341: /alerts kural formunun kaydetme hatası
// (gösterim: RuleSaveError.tsx). Ayrı dosya: react-refresh yalnız-bileşen kuralı.

/** Form oturumuna bağlı kaydetme hatası. `formKey` değişince (form kapandı,
 *  başka kural düzenleniyor, yeni taslak) hata kendiliğinden temizlenir. */
export function useRuleSaveError(formKey: string | null) {
  const [err, setErr] = useState<string | null>(null);
  const [key, setKey] = useState(formKey);
  if (key !== formKey) {
    setKey(formKey);
    setErr(null);
  }
  /** fn'i koşar; reddedilirse hatayı tutar ve false döner. */
  const run = useCallback(async (fn: () => Promise<unknown>): Promise<boolean> => {
    setErr(null);
    try {
      await fn();
      return true;
    } catch (e) {
      setErr(apiErrorText(e, 'Kural kaydedilemedi'));
      return false;
    }
  }, []);
  return { error: err, run };
}
