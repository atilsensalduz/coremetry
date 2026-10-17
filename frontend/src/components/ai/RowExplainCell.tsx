// RowExplainCell — v0.10.1291 (CoSRE follow-up D): sohbet tablolarının satır
// başı ✨ hücresi (kanıt log tablosu `rowSubjects`, ChatTraceList satırı).
// Tek affordance AIExplainButton (xs, ikon): adrese `?ai=` yazar, açıklama
// AI çekmecesinde. Çekmecenin AÇIKLAMA kipinde sütun HİÇ çizilmez
// (aiDrawerContext — tık açık özneyi değiştirirdi); özne yoksa hücre boş.
import { AIExplainButton } from './AIExplainButton';
import { IconSparkles } from '@/components/icons';
import type { AISubject } from '@/lib/aiSubject';

export function RowExplainCell({ subject, title }: { subject: AISubject | null; title: string }) {
  return (
    <td className="cm-row-ai" data-row-ai="">
      {subject && <AIExplainButton subject={subject} size="xs" title={title} label={<IconSparkles />} />}
    </td>
  );
}

export function RowExplainHead() {
  return <th className="cm-row-ai"><span className="sr-only">AI açıkla</span></th>;
}
