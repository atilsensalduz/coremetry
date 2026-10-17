// sreHypothesisVerify — v0.10.1291: "hipotezi doğrula" kuyruğa alma çekirdeği.
// Aksiyon bloğu (SREHypothesisButton, v0.10.1288) ve takip çipi (FollowupChips)
// AYNI çağrıyı paylaşır: POST /api/problems/{id}/investigation {kind:'verify'}
// (editor+, audit; uç türü yeniden doğrular).
import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries';
import { toast } from '@/lib/toast';
import type { SREHypothesisAction } from '@/lib/pageActions';

export function useSREHypothesisVerify(action: Pick<SREHypothesisAction, 'problemId' | 'hypothesis'>) {
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
  return { state, run };
}

