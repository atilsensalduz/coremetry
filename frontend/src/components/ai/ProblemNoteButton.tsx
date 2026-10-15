// ProblemNoteButton — v0.10.1169 — "Bulguyu probleme not olarak kaydet".
// Yük sunucudan (tool sonucundan türemiş metin + imzalı jeton); burada yalnız
// gönderilir. Yazma editör+ ister: viewer'da sunucu 403 döner, hata gösterilir.
import { useState } from 'react';
import { Button } from '@/components/ui';
import { api } from '@/lib/api';
import { toast } from '@/lib/toast';
import type { ProblemNoteAction } from '@/lib/pageActions';

// onSaved — CoSRE öğrenme Faz 2: başarılı kayıt cevabın olumlu örtük sinyali.
export function ProblemNoteButton({ action, onSaved }: { action: ProblemNoteAction; onSaved?: () => void }) {
  const [state, setState] = useState<'idle' | 'busy' | 'done'>('idle');
  const save = async () => {
    setState('busy');
    try {
      await api.saveProblemNote(action.problemId, { token: action.token, text: action.text, tool: action.tool });
      setState('done');
      onSaved?.();
      toast.success('Bulgu probleme not olarak kaydedildi');
    } catch (e) {
      setState('idle');
      toast.error(`Not kaydedilemedi: ${e instanceof Error ? e.message : String(e)}`);
    }
  };
  return (
    <Button variant="secondary" size="sm" title={action.text} loading={state === 'busy'}
      disabled={state === 'done'} onClick={() => { void save(); }}>
      📝 {state === 'done' ? 'Not kaydedildi' : action.label}
    </Button>
  );
}
