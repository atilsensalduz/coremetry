import { useState } from 'react';
import { BookOpen, Compass, Rocket, Search, Siren, Workflow, type LucideIcon } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { ServicePicker } from '@/components/ServicePicker';
import { OperationPicker } from '@/components/OperationPicker';
import { api } from '@/lib/api';
import type { CopilotStarterGroup } from '@/lib/types';
import type { ChatSendOpts } from './useChatThread';
import {
  capabilityItems, groupForCapability, operationErrorQuestion, serviceHealthQuestion,
  splitCaret, starterSendOpts, tCosre as t, type CapabilityKind,
} from './capabilityHints';

// CosreCapabilities — v0.10.1128: boş CoSRE sohbetinde "neler yapabilirim"
// satırları (çekmece + /cosre aynı bileşen: CopilotChat). Tıklama composer'ı
// örnek soruyla doldurur, GÖNDERMEZ (onPrefill). Kurallar capabilityHints.ts.
// Metinler sohbetin dilinde (tCosre, sabit TR) — UI dilinde DEĞİL; gerekçe orada.
//
// v0.10.1382 (operatör: "hazır soruları daha anlamlı cevapları seçilebilir
// halde getirsek") — her satır = başlık (şablonu DOLDURUR, eskisi gibi) +
// altında canlı veriden seçenek çipleri (role=group, /api/copilot/starters):
// somut çip soruyu HEMEN GÖNDERİR (onAsk); "başka bir servis…" satır içi
// sunucu-aramalı ServicePicker açar, seçim tam cümleyi DOLDURUR (göndermez).
// Servis satırına bu tarayıcının sabitlenmiş/son servisleri düz çip olarak
// eklenir (sunucuya gitmez). Boş grup çizilmez; p1/deploy satırları yalnız
// öğe varken (capabilityItems).
//
// v0.10.1390 (operatör: "Burada filo geneli ve böyle birçok servis yazmasın,
// search kısmı çıksa yeter. Aynısı hata veren teknik operasyonlar için de
// geçerli, çok karışık gösteriyor.") — servis ve teknik operasyon satırlarında
// "filo geneli" etiketi, servis/yol/yerel çipleri, "başka bir servis…"
// anahtarı ve "<svc> teknik hataları" çipleri KALKTI. Yerlerinde başlığın
// altında HER ZAMAN görünen sunucu-aramalı kutu: ServicePicker ("Servis
// ara…") ve OperationPicker source="cosre" ("Operasyon ara…" — sohbet
// ön-getirmesinin kataloğu: span + Oracle operasyon kodları). Seçim (ya da
// birebir ad + Enter) tam cümleyi HEMEN GÖNDERİR (onAsk = askChip; çiplerin
// yolu): "<svc> sağlığı nasıl?" / "<OP> operasyonu neden hata veriyor?" —
// ikisi de LLM'siz yönlenir (copilot_starters_test TestStarterQuestionsRoute).
// Enter'la yazılan ad sunucuya SORULUR (örneklenmiş listeye karşı doğrulama
// yok, v0.8.265); birebir değilse cümle composer'a DOLDURULUR (sessiz no-op
// yok). P1 / deploy satırları ve çipleri aynen.
const ICON: Record<CapabilityKind, LucideIcon> = {
  wiki: BookOpen,
  service: Search,
  operation: Workflow,
  p1: Siren,
  deploy: Rocket,
  navigate: Compass,
};

export function CosreCapabilities({ wiki, groups = [], onPrefill, onAsk }: {
  wiki: boolean;
  groups?: readonly CopilotStarterGroup[];
  onPrefill: (text: string, caret: number) => void;
  /** v0.10.1382 — somut seçenek: soruyu hemen gönderir (CopilotChat askChip). */
  onAsk?: (text: string, so?: ChatSendOpts) => void;
}) {
  const [svc, setSvc] = useState('');
  const [op, setOp] = useState('');
  const send = (q: string) => (onAsk ? onAsk(q) : onPrefill(q, q.length));
  // v0.10.1390 — seçim (picked) doğrudan gönderir; Enter'da yazılan ad sunucu
  // aramasıyla birebir doğrulanır, değilse cümle doldurulur.
  const commit = (picked: string | undefined, typed: string, clear: () => void,
    question: (name: string) => string, exact: (name: string) => Promise<string[]>) => {
    const name = (picked ?? typed).trim();
    if (!name) return;
    const q = question(name);
    clear();
    if (picked !== undefined) { send(q); return; }
    exact(name)
      .then(names => (names.includes(name) ? send(q) : onPrefill(q, q.length)))
      .catch(() => onPrefill(q, q.length));
  };
  const commitService = (v?: string) => commit(v, svc, () => setSvc(''), serviceHealthQuestion,
    name => api.serviceNames(name, 50).then(r => r.names));
  const commitOperation = (v?: string) => commit(v, op, () => setOp(''), operationErrorQuestion,
    name => api.copilotOperationSearch(name).then(r => r.names));

  return (
    <div className="cosre-cap" role="list" aria-label={t('cosre.cap.aria')}>
      {capabilityItems(wiki, groups).map(it => {
        const Icon = ICON[it.kind];
        const group = groupForCapability(it.kind, groups);
        const items = onAsk ? (group?.items ?? []) : [];
        return (
          <div role="listitem" key={it.kind} className="cosre-cap__item">
            <Button variant="ghost" size="sm" className="cosre-cap__row"
              data-cap={it.kind}
              title={t('cosre.cap.tryHint')}
              leftIcon={<Icon size={13} aria-hidden="true" className="cosre-cap__icon" />}
              onClick={() => { const p = splitCaret(t(it.promptKey)); onPrefill(p.text, p.caret); }}>
              <span>{t(it.labelKey)}</span>
            </Button>
            {it.kind === 'service' && (
              <div className="cosre-cap__picker" data-cap-search="service">
                <ServicePicker value={svc} onChange={setSvc} onEnter={commitService}
                  placeholder={t('cosre.cap.search.service')} width="100%" />
              </div>
            )}
            {it.kind === 'operation' && (
              <div className="cosre-cap__picker" data-cap-search="operation">
                <OperationPicker source="cosre" value={op} onChange={setOp} onEnter={commitOperation}
                  placeholder={t('cosre.cap.search.operation')} width="100%" />
              </div>
            )}
            {items.length > 0 && (
              <div className="cosre-cap__choices" role="group" aria-label={t('cosre.cap.choices')} data-cap-choices={it.kind}>
                {group?.fleet && (
                  <span className="cosre-cap__fleet" title={t('cosre.cap.fleet.title')}>{t('cosre.cap.fleet')}</span>
                )}
                {items.map(st => (
                  <Chip key={st.question} pill size="sm" tone={st.kind === 'p1' ? 'accent' : 'neutral'}
                    data-starter={st.kind} title={st.question}
                    onClick={() => onAsk?.(st.question, starterSendOpts(st))}>
                    {st.chip}
                  </Chip>
                ))}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
