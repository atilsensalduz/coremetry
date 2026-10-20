import { useState } from 'react';
import { BookOpen, Bug, Compass, Search, Users, Workflow, type LucideIcon } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { ServicePicker } from '@/components/ServicePicker';
import { OperationPicker } from '@/components/OperationPicker';
import { TeamPicker } from '@/components/TeamPicker';
import { api } from '@/lib/api';
import type { CopilotStarterGroup } from '@/lib/types';
import type { ChatSendOpts } from './useChatThread';
import {
  allExceptionsQuestion, capabilityItems, groupForCapability, operationErrorQuestion, readStoredTeam,
  serviceHealthQuestion, splitCaret, starterAsk, TEAM_ASKS, teamCardQuestion, teamPickQuestion, tCosre as t,
  writeStoredTeam, type CapabilityKind,
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
//
// v0.10.1402 (operatör: "Alttaki takımımın servisleri exceptionları yukarıda
// olsun. Takım seçilebilsin. Son deploy etkisini kaldırabiliriz." + "Takımı
// da seçilebilir yapabiliriz, oradan devam eder.") — EN ÜSTTE Takımım kartı:
// sunucu-aramalı TeamPicker + üç eylem çipi (eskiden kartların altındaki
// gevşek satırdaki "Takımımın …" çipleri). Takım seçiliyse çipler adı geçen
// takımın cümlesini, değilse "Takımımın …"ı gönderir (teamCardQuestion).
// Seçicide takım seçmek "<takım> takımının servisleri nasıl?" turunu HEMEN
// gönderir (servis/operasyon kutularının yolu) → v0.10.1397 takım özeti.
// Varsayılan takım: bu tarayıcıda son seçilen (localStorage, kolaylık) >
// sunucunun çözdüğü kullanıcı takımı (/api/copilot/starters `team`) > boş.
// Deploy satırı ve "filo geneli" deploy çipleri kalktı (sunucu grubu da).
//
// v0.10.1404 (operatör, prod ekranı: "P1 yerine en son exception'lara
// yönlendirsin.") — P1 satırı ("Açık P1 problemlerinin kök nedenine
// bakabilirim" + "<svc> · <kural>" çipleri) yerine "Son exception gruplarına
// bakabilirim": sunucunun en son açık exception grupları (takım ya da "filo
// geneli"). Grup çipi SOHBET İÇİ açıklama turu başlatır (starterAsk →
// send(text, {explainAsk}); gezinme yok, çekmece özne kipi yok); sonda "Tüm son
// exception'lar" takım exception cevabını ister (allExceptionsQuestion).
const ICON: Record<CapabilityKind, LucideIcon> = {
  team: Users,
  wiki: BookOpen,
  service: Search,
  operation: Workflow,
  exceptions: Bug,
  navigate: Compass,
};

export function CosreCapabilities({ wiki, groups = [], team: serverTeam = '', onPrefill, onAsk }: {
  wiki: boolean;
  groups?: readonly CopilotStarterGroup[];
  /** v0.10.1402 — sunucunun çözdüğü kullanıcı takımı (Takımım varsayılanı). */
  team?: string;
  onPrefill: (text: string, caret: number) => void;
  /** v0.10.1382 — somut seçenek: soruyu hemen gönderir (CopilotChat askChip). */
  onAsk?: (text: string, so?: ChatSendOpts) => void;
}) {
  const [svc, setSvc] = useState('');
  const [op, setOp] = useState('');
  // v0.10.1402 — `picked`: bu tarayıcıda seçilen takım (null = hiç seçilmedi →
  // sunucu takımı); `teamText`: kutudaki yazı (null = seçili takımı göster).
  const [picked, setPicked] = useState<string | null>(readStoredTeam);
  const [teamText, setTeamText] = useState<string | null>(null);
  const team = (picked ?? serverTeam).trim();
  const chooseTeam = (name: string) => {
    setPicked(name);
    setTeamText(null);
    writeStoredTeam(name);
  };
  const onTeamText = (v: string) => {
    setTeamText(v);
    if (!v.trim()) { // temizlendi → takım yok ("Takımımın …"), depodan da sil
      setPicked('');
      writeStoredTeam('');
    }
  };
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
  // v0.10.1402 — takım seçimi: commit ile aynı yol (seçim HEMEN gönderir, Enter
  // sunucuda birebir doğrulanır); seçilen takım kutuda ve çiplerde KALIR.
  const commitTeam = (v?: string) => {
    const name = (v ?? teamText ?? team).trim();
    if (!name) return;
    const q = teamPickQuestion(name);
    if (v !== undefined) { chooseTeam(name); send(q); return; }
    api.copilotScopeNames('team', name)
      .then(r => {
        if (r.names.includes(name)) { chooseTeam(name); send(q); } else onPrefill(q, q.length);
      })
      .catch(() => onPrefill(q, q.length));
  };

  return (
    <div className="cosre-cap" role="list" aria-label={t('cosre.cap.aria')}>
      {capabilityItems(wiki, groups).map(it => {
        const Icon = ICON[it.kind];
        const group = groupForCapability(it.kind, groups);
        const items = onAsk ? (group?.items ?? []) : [];
        // v0.10.1404 — exceptions satırının son çipi (grup kapsamıyla aynı takım).
        const allExcQ = it.kind === 'exceptions' ? allExceptionsQuestion(serverTeam, !!group?.fleet) : '';
        return (
          <div role="listitem" key={it.kind} className={`cosre-cap__item${it.kind === 'team' ? ' cosre-cap__item--team' : ''}`}>
            <Button variant="ghost" size="sm" className="cosre-cap__row"
              data-cap={it.kind}
              title={t('cosre.cap.tryHint')}
              leftIcon={<Icon size={13} aria-hidden="true" className="cosre-cap__icon" />}
              onClick={() => { const p = splitCaret(t(it.promptKey)); onPrefill(p.text, p.caret); }}>
              <span>{t(it.labelKey)}</span>
            </Button>
            {it.kind === 'team' && (<>
              <div className="cosre-cap__picker" data-cap-search="team">
                <TeamPicker value={teamText ?? team} onChange={onTeamText} onEnter={commitTeam}
                  placeholder={t('cosre.cap.search.team')} title={t('cosre.cap.team.searchTitle')} width="100%" />
              </div>
              <div className="cosre-cap__choices" role="group" aria-label={t('cosre.cap.choices')} data-cap-choices="team">
                {TEAM_ASKS.map(a => {
                  const q = teamCardQuestion(a.kind, team);
                  return (
                    <Chip key={a.kind} pill size="sm" data-team-ask={a.kind} title={q} onClick={() => send(q)}>
                      {t(a.labelKey)}
                    </Chip>
                  );
                })}
              </div>
            </>)}
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
                  <Chip key={st.explainAsk?.id ?? st.question} pill size="sm" tone="neutral"
                    data-starter={st.kind} title={st.title || st.question}
                    onClick={() => { const a = starterAsk(st); onAsk?.(a.text, a.so); }}>
                    {st.chip}
                  </Chip>
                ))}
                {allExcQ && (
                  <Chip pill size="sm" tone="accent" data-starter="exceptions-all" title={allExcQ} onClick={() => send(allExcQ)}>
                    {t('cosre.cap.exceptions.all')}
                  </Chip>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
