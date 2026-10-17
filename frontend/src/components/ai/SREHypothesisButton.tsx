// SREHypothesisButton — v0.10.1288 (AI SRE Faz 2) — sohbetteki
// "<hipotez> hipotezini doğrula" aksiyonu. Yük sunucudan (get_problem tool
// sonucundan türemiş; model metninden değil); burada yalnız doğrulama koşusu
// kuyruğa alınır (POST investigation {kind:'verify'}). Yazma editor+ ister:
// sunucu viewer'a aksiyonu hiç üretmez, uç ayrıca 403 döner.
import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/ui';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries';
import { toast } from '@/lib/toast';
import type { SREHypothesisAction } from '@/lib/pageActions';

export function SREHypothesisButton({ action }: { action: SREHypothesisAction }) {
  const qc = useQueryClient();
  const [state, setState] = useState<'idle' | 'busy' | 'done'>('idle');
  const run = async () => {
    setState('busy');
    try {
      const r = await api.enqueueProblemInvestigation(action.problemId, { kind: 'verify', hypothesis: action.hypothesis });
      setState('done');
      if (r.created) toast.success('Doğrulama kuyruğa alındı — sonuç problem sayfasındaki AI SRE kartında');
      else toast.info(r.reason ? `Zaten ${r.reason}` : 'Zaten kuyrukta');
      await qc.invalidateQueries({ queryKey: keys.problems.investigation(action.problemId) });
    } catch (e) {
      setState('idle');
      toast.error(`Kuyruğa alınamadı: ${e instanceof Error ? e.message : String(e)}`);
    }
  };
  return (
    <Button variant="secondary" size="sm" loading={state === 'busy'} disabled={state === 'done'} onClick={() => { void run(); }}>
      🔎 {state === 'done' ? 'Doğrulama kuyrukta' : action.label}
    </Button>
  );
}
