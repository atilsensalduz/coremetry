import { useMemo, useState } from 'react';
import { BookOpen, Compass, Rocket, Search, Siren, Workflow, type LucideIcon } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { ServicePicker } from '@/components/ServicePicker';
import { getPinnedServices, getRecentServices } from '@/lib/recentServices';
import type { CopilotStarter, CopilotStarterGroup } from '@/lib/types';
import type { ChatSendOpts } from './useChatThread';
import {
  capabilityItems, groupForCapability, localServiceChips, serviceHealthQuestion,
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
  /** v0.10.1382 — somut seçenek çipi: soruyu hemen gönderir (CopilotChat askChip). */
  onAsk?: (text: string, so?: ChatSendOpts) => void;
}) {
  const [picking, setPicking] = useState(false);
  const [pick, setPick] = useState('');
  const svcGroup = groupForCapability('service', groups);
  // Yerel servisler yalnız grup değişince yeniden okunur (localStorage, ağ yok).
  const local = useMemo(
    () => localServiceChips(svcGroup?.items ?? [], getPinnedServices(), getRecentServices()),
    [svcGroup],
  );
  const commitPick = (v?: string) => {
    const name = (v ?? pick).trim();
    if (!name) return;
    const q = serviceHealthQuestion(name);
    setPicking(false);
    setPick('');
    onPrefill(q, q.length);
  };

  return (
    <div className="cosre-cap" role="list" aria-label={t('cosre.cap.aria')}>
      {capabilityItems(wiki, groups).map(it => {
        const Icon = ICON[it.kind];
        const group = groupForCapability(it.kind, groups);
        const items: CopilotStarter[] = onAsk
          ? [...(group?.items ?? []), ...(it.kind === 'service' ? local : [])]
          : [];
        const withPicker = it.kind === 'service';
        return (
          <div role="listitem" key={it.kind} className="cosre-cap__item">
            <Button variant="ghost" size="sm" className="cosre-cap__row"
              data-cap={it.kind}
              title={t('cosre.cap.tryHint')}
              leftIcon={<Icon size={13} aria-hidden="true" className="cosre-cap__icon" />}
              onClick={() => { const p = splitCaret(t(it.promptKey)); onPrefill(p.text, p.caret); }}>
              <span>{t(it.labelKey)}</span>
            </Button>
            {(items.length > 0 || withPicker) && (
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
                {withPicker && (
                  <Chip pill size="sm" data-chip="pick-service" aria-expanded={picking}
                    onClick={() => setPicking(p => !p)}>
                    {t('cosre.cap.pickService')}
                  </Chip>
                )}
              </div>
            )}
            {withPicker && picking && (
              <div className="cosre-cap__picker">
                <ServicePicker value={pick} onChange={setPick} onEnter={commitPick}
                  placeholder={t('cosre.cap.pickService.placeholder')} width="100%" />
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
