// SREHypothesisButton — v0.10.1288 (AI SRE Faz 2) — sohbetteki
// "<hipotez> hipotezini doğrula" aksiyonu. Yük sunucudan (get_problem tool
// sonucundan türemiş; model metninden değil); burada yalnız doğrulama koşusu
// kuyruğa alınır (POST investigation {kind:'verify'}). Yazma editor+ ister:
// sunucu viewer'a aksiyonu hiç üretmez, uç ayrıca 403 döner.
import { Button } from '@/components/ui';
import type { SREHypothesisAction } from '@/lib/pageActions';
import { useSREHypothesisVerify } from './sreHypothesisVerify';

export function SREHypothesisButton({ action }: { action: SREHypothesisAction }) {
  const { state, run } = useSREHypothesisVerify(action);
  return (
    <Button variant="secondary" size="sm" loading={state === 'busy'} disabled={state === 'done'} onClick={() => { void run(); }}>
      🔎 {state === 'done' ? 'Doğrulama kuyrukta' : action.label}
    </Button>
  );
}
