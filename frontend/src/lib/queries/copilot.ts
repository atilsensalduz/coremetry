import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { CopilotStarterGroup } from '@/lib/types';

// queries/copilot.ts — v0.10.702. Boş sohbetin veri çipleri: YALNIZ çekmece
// açık + sohbet boşken (aç-üzerine-getir; liste prefetch'i yok, poll yok).
// staleTime sunucu cache'iyle aynı (60 s). Hata → boş liste: statik çipler
// zaten var, veri çipi bir ek.
// v0.10.1382 — gruplu yanıt; env (useUrlEnv) sorguya VE anahtara girer
// (sunucu anahtarı da env'i özetler). Env yalnız P1 grubuna uygulanır.
export function useCopilotStarters(enabled: boolean, env = '', rangeS = 3600): CopilotStarterGroup[] {
  const q = useQuery({
    queryKey: ['copilot', 'starters', rangeS, env],
    queryFn: ({ signal }) => api.copilotStarters(rangeS, env, signal),
    enabled,
    staleTime: 60_000,
    retry: false,
  });
  return q.data?.groups ?? [];
}

// v0.10.1289 — CoSRE operatör kuralları (GET /api/ai/rules, her rol). Yalnız
// panel / modal açıkken (enabled); sunucu önbelleği yok, staleTime 30 s
// (süreç-içi yenileme aralığı). Yazımdan sonra çağıran anahtarı geçersizler.
export const COSRE_RULES_KEY = ['copilot', 'rules'] as const;

export function useCosreRules(enabled = true) {
  return useQuery({
    queryKey: COSRE_RULES_KEY,
    queryFn: ({ signal }) => api.cosreRules(signal),
    enabled,
    staleTime: 30_000,
    retry: false,
  });
}
