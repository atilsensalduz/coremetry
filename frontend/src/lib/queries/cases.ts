// cases.ts — v0.10.1475 — /ai "Onay bekleyen vakalar" (GET /api/cases/pending).
// staleTime = sunucu TTL (15 s); yoklama YOK (pollingBudget.pin) — liste
// operatör onayladıkça invalidate edilir (onay ucu sunucu önbelleğini de
// düşürür). Sayfa değişirken önceki sayfa küresel placeholderData ile kalır;
// panel isPlaceholderData'da satırları kilitler (yanlış satır onaylanmasın).
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { PendingCaseSource } from '@/lib/types';
import { keys } from './keys';

export const PENDING_CASES_STALE_MS = 15_000;

export function usePendingCases(f: { source: PendingCaseSource; days: number; offset: number; limit: number }) {
  return useQuery({
    queryKey: keys.cases.pending(f),
    queryFn: ({ signal }) => api.pendingCases(f, signal),
    staleTime: PENDING_CASES_STALE_MS,
  });
}
