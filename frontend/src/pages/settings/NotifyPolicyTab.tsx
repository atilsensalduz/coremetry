import { useState } from 'react';
import { Button, useConfirm } from '@/components/ui';
import { Spinner } from '@/components/Spinner';
import { Field, SelectField } from '@/components/ui/Field';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState } from '@/components/ui/DataTable';
import type { DataTableColumn } from '@/lib/dataTable';
import { api } from '@/lib/api';
import type { NotifyPolicy, NotifyReplayFamily, NotifyReplayReport, NotifyDigestItem } from '@/lib/types';
import { enforceReadiness, holdLabel, parseDigestTimes, reductionPct } from '@/lib/notifyPolicyForm';
import { tsCompact } from '@/lib/utils';
import { FlashBox, SettingsSection, humanize, useSettingsLoad, SettingsLoadError } from './shared';

// ── Bildirim politikası (v0.10.1533, Problem standardı R5) ──────────────────
// Operatör kararları D2–D10 (docs/DECISIONS.md "Problem standardı + triage +
// bildirim politikası"). Bu ekran üç şey yapar:
//
//   1. Politikayı düzenler (kip, P3 özeti, sessiz saat, tekrar ve "problem
//      değil" pencereleri, etki tabanları, şablon) → PUT /api/admin/notify-policy.
//   2. Enforce'a geçişin kanıtını gösterir: 14 günlük replay (aile başına önce /
//      sonra, tutma kodları) ve recall (D8: "Gerçek problem" işaretleri + onaylı
//      vakalarda %100). Recall temiz değilse enforce seçimi açık bir onay ister.
//   3. Bekleyen P3 özet kuyruğunu gösterir (enforce + p3=digest).
//
// Replay ağır bir okuma (sunucu 5 dk önbellek): sekme açılınca DEĞİL, düğmeyle
// koşar (ES-maliyet disiplini: aç-üzerine-getir).

const MODE_HINT: Record<NotifyPolicy['mode'], string> = {
  off: 'Yeni bar ölçülmez; bildirimler bugünkü gibi gider.',
  shadow: 'Varsayılan. Bildirimler bugünkü gibi gider; yeni barın kararı yalnız ölçülür (replay + gölge satırları).',
  enforce: 'Yeni bar gönderimi yönetir: P3 özete düşer, düşük güven ve taban altı tutulur, tekrar 12 sa susar.',
};

const FAMILY_TR: Record<string, string> = {
  error: 'Hata', latency: 'Gecikme', traffic: 'Trafik', availability: 'Erişilebilirlik', exception: 'Exception',
  db: 'Veritabanı', external: 'Dış kaynak', runtime: 'Runtime', self: 'Coremetry', incident: 'Olay',
  cluster: 'Küme', rule: 'Kural',
};

const FAMILY_COLS: DataTableColumn<NotifyReplayFamily>[] = [
  { id: 'family', label: 'Aile', sortValue: f => FAMILY_TR[f.family] ?? f.family, naturalDir: 'asc', flex: true, minWidth: 120, priority: 1 },
  { id: 'opened', label: 'Açılış', sortValue: f => f.opened, numeric: true, width: 90, minWidth: 80, priority: 1 },
  { id: 'sentBefore', label: 'Bugün giden', sortValue: f => f.sentBefore, numeric: true, width: 110, minWidth: 96, priority: 1 },
  { id: 'wouldSendAfter', label: 'Yeni barla', sortValue: f => f.wouldSendAfter, numeric: true, width: 110, minWidth: 96, priority: 1 },
  { id: 'reduction', label: 'Azalma', sortValue: f => reductionPct(f.sentBefore, f.wouldSendAfter) ?? -1, numeric: true, width: 90, minWidth: 80, priority: 2 },
  { id: 'holds', label: 'Tutma nedenleri', width: 300, minWidth: 200, priority: 2 },
  { id: 'noiseMarked', label: '“Problem değil”', sortValue: f => f.noiseMarked, numeric: true, width: 120, minWidth: 100, priority: 3 },
];

export function NotifyPolicyTab() {
  const confirm = useConfirm();
  const [pol, setPol] = useState<NotifyPolicy | null>(null);
  const [digestRaw, setDigestRaw] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [replay, setReplay] = useState<NotifyReplayReport | null>(null);
  const [replayBusy, setReplayBusy] = useState(false);
  const [replayErr, setReplayErr] = useState<string | null>(null);
  const [digest, setDigest] = useState<{ items: NotifyDigestItem[]; lastSlot: string } | null>(null);

  const { loaded, error: loadErr, retry } = useSettingsLoad(() => api.notifyPolicy(), p => {
    setPol(p);
    setDigestRaw((p.digestTimes ?? []).join(', '));
    api.notifyDigest().then(setDigest).catch(() => setDigest(null));
  });

  const famDt = useDataTable<NotifyReplayFamily>({
    storageKey: 'notify-replay-families', columns: FAMILY_COLS, rows: replay?.families ?? [],
    initialSort: { id: 'sentBefore', dir: 'desc' },
  });

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;
  if (!pol) return <Spinner />;

  const set = <K extends keyof NotifyPolicy>(k: K, v: NotifyPolicy[K]) => setPol({ ...pol, [k]: v });
  const setFloor = (k: keyof NotifyPolicy['impactFloors'], v: string) =>
    setPol({ ...pol, impactFloors: { ...pol.impactFloors, [k]: Number(v) } });
  const parsedDigest = parseDigestTimes(digestRaw);
  const ready = enforceReadiness(replay);

  const save = async () => {
    if (parsedDigest.error) { setMsg({ kind: 'err', text: `Özet saatleri: ${parsedDigest.error}` }); return; }
    if (pol.mode === 'enforce') {
      const ok = await confirm({
        title: 'Enforce kipine geçilsin mi?',
        body: (
          <>
            Yeni bar bildirimlerin <b>gönderimini</b> yönetecek: P3 özete düşer, düşük güvenli ve etki tabanının altındaki
            sinyaller bildirilmez, aynı imza 12 sa tekrar etmez. {ready.ok ? ready.text : <b>{ready.text}</b>}
          </>
        ),
        confirmLabel: 'Enforce’a geç',
        danger: !ready.ok,
      });
      if (!ok) return;
    }
    setBusy(true); setMsg(null);
    try {
      const saved = await api.putNotifyPolicy({ ...pol, digestTimes: parsedDigest.times });
      setPol(saved);
      setDigestRaw(saved.digestTimes.join(', '));
      setMsg({ kind: 'ok', text: `Kaydedildi — kip: ${saved.mode}. Tüm pod'lara ≤ 30 sn içinde yayılır.` });
    } catch (e) {
      setMsg({ kind: 'err', text: humanize(e) });
    } finally {
      setBusy(false);
    }
  };

  const runReplay = async () => {
    setReplayBusy(true); setReplayErr(null);
    try { setReplay(await api.notifyPolicyReplay(14)); } catch (e) { setReplayErr(humanize(e)); } finally { setReplayBusy(false); }
  };

  const num = (label: string, value: number, onChange: (v: string) => void, hint?: string) => (
    <Field label={label} hint={hint} type="number" min={0} value={Number.isFinite(value) ? value : 0}
      onChange={e => onChange(e.target.value)} />
  );

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <SettingsSection title="Bildirim politikası"
        description="Problem standardı (D2–D10): bildirim önceliği = görünüm önceliği; düşük güven bildirilmez; etki tabanları; tekrar ve “problem değil” pencereleri. Enforce’a geçiş yöneticinin kararı — önce replay ile recall’u doğrulayın (D8).">
        <div className="np-grid">
          <SelectField label="Kip" value={pol.mode} hint={MODE_HINT[pol.mode]}
            onChange={e => set('mode', e.target.value as NotifyPolicy['mode'])}>
            <option value="off">off — ölçme</option>
            <option value="shadow">shadow — yalnız ölç (varsayılan)</option>
            <option value="enforce">enforce — gönderimi yönet</option>
          </SelectField>
          <SelectField label="Şablon" value={pol.template ?? 'tr'}
            hint="tr: Türkçe standart + yaşam döngüsü + imza başına tek e-posta konusu. legacy: eski biçim (geri dönüş)."
            onChange={e => set('template', e.target.value as 'tr' | 'legacy')}>
            <option value="tr">tr — Türkçe standart</option>
            <option value="legacy">legacy — eski biçim</option>
          </SelectField>
          <SelectField label="P3 bildirimleri" value={pol.p3}
            hint="Yalnız enforce kipinde etkili. digest: günün özet saatlerinde tek mesaj."
            onChange={e => set('p3', e.target.value as NotifyPolicy['p3'])}>
            <option value="digest">özet (varsayılan)</option>
            <option value="immediate">anında</option>
            <option value="off">gönderme</option>
          </SelectField>
          <Field label="Özet saatleri (yerel)" value={digestRaw} placeholder="09:00, 16:00"
            error={parsedDigest.error} hint="Takvim saat diliminde; en çok 6."
            onChange={e => setDigestRaw(e.target.value)} />
          <Field label="Sessiz saat başlangıcı (yalnız P2)" value={pol.quietHours?.from ?? ''} placeholder="ör. 22:00"
            onChange={e => set('quietHours', e.target.value || pol.quietHours?.to ? { from: e.target.value, to: pol.quietHours?.to ?? '' } : null)} />
          <Field label="Sessiz saat bitişi" value={pol.quietHours?.to ?? ''} placeholder="ör. 07:00"
            onChange={e => set('quietHours', e.target.value || pol.quietHours?.from ? { from: pol.quietHours?.from ?? '', to: e.target.value } : null)} />
          {num('Tekrar penceresi (sa)', pol.noiseDedupeHours, v => set('noiseDedupeHours', Number(v)),
            'D3: aynı imza bu süre tekrar gitmez; öncelik yükselir, etki ≥ 2× ya da yüksek güvenli P1 ise gider.')}
          {num('“Problem değil” susturması (sa)', pol.noiseMuteHours, v => set('noiseMuteHours', Number(v)),
            'D5: işaret bildirimi bu süre susturur (varsayılan 168 = 7 gün); kalıcı gizleme yönetici işaretinde.')}
        </div>
        <h3 className="np-sub">Etki tabanları (D6 — “asla yalnız yüzde”)</h3>
        <div className="np-grid">
          {num('Hata: başarısız / 5 dk', pol.impactFloors.errorFailed5m, v => setFloor('errorFailed5m', v))}
          {num('Hata: oran (%)', pol.impactFloors.errorPct, v => setFloor('errorPct', v), 'Yüzde kolu yalnız çağrı tabanı geçilmişken.')}
          {num('Çağrı tabanı / 5 dk', pol.impactFloors.minCalls5m, v => setFloor('minCalls5m', v))}
          {num('Gecikme: p99 artışı (ms)', pol.impactFloors.latencyP99DeltaMs, v => setFloor('latencyP99DeltaMs', v))}
          {num('Trafik düşüşü (%)', pol.impactFloors.trafficDropPct, v => setFloor('trafficDropPct', v))}
          {num('Trafik: taban çağrı / 5 dk', pol.impactFloors.trafficMinBaselineCalls5m, v => setFloor('trafficMinBaselineCalls5m', v))}
        </div>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center', marginTop: 12, flexWrap: 'wrap' }}>
          <Button variant="primary" onClick={() => { void save(); }} loading={busy}>Kaydet</Button>
          {pol.updatedBy && <span className="field-hint">Son değişiklik: {pol.updatedBy}{pol.updatedAt ? ` · ${tsCompact(pol.updatedAt)}` : ''}</span>}
        </div>
        {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
      </SettingsSection>

      <SettingsSection title="Replay — son 14 gün"
        description="Geçmiş bildirimler yeni barla yeniden oynatılır: aile başına bugün giden ve yeni barla gidecek açılışlar, tutma nedenleri ve recall (D8).">
        <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap', marginBottom: 10 }}>
          <Button variant="secondary" onClick={() => { void runReplay(); }} loading={replayBusy}>
            {replay ? 'Yeniden çalıştır' : 'Replay’i çalıştır'}
          </Button>
          {replay && (
            <span className={ready.ok ? 'badge b-gray' : 'badge b-warn'} title={replay.recall.cases.map(c => `${c.headline} → ${holdLabel(c.hold)}`).join('\n') || undefined}>
              {ready.text}
            </span>
          )}
        </div>
        {replayErr && <FlashBox kind="err">{replayErr}</FlashBox>}
        {replay && (
          <>
            <div className="table-wrap">
              <table {...famDt.tableProps}>
                <DataTableColgroup dt={famDt} />
                <DataTableHead dt={famDt} />
                <tbody>
                  {famDt.sortedRows.length === 0 ? <DataTableState dt={famDt} kind="empty" message="Bu pencerede bildirim yok" /> : famDt.sortedRows.map((f, i) => {
                    const red = reductionPct(f.sentBefore, f.wouldSendAfter);
                    const holds = Object.entries(f.holds ?? {}).sort((a, b) => b[1] - a[1]);
                    return (
                      <tr key={f.family} {...famDt.rowProps(i)}>
                        <DataTableCell dt={famDt} col="family" row={f} value={FAMILY_TR[f.family] ?? f.family} />
                        <DataTableCell dt={famDt} col="opened" row={f} value={f.opened} />
                        <DataTableCell dt={famDt} col="sentBefore" row={f} value={f.sentBefore} />
                        <DataTableCell dt={famDt} col="wouldSendAfter" row={f} value={f.wouldSendAfter} />
                        <DataTableCell dt={famDt} col="reduction" row={f} value={red == null ? '' : `%${red}`} />
                        <DataTableCell dt={famDt} col="holds" row={f}
                          value={holds.map(([k, n]) => `${holdLabel(k)} ${n}`).join(' · ')}
                          title={holds.map(([k, n]) => `${k}: ${n}`).join('\n') || undefined} />
                        <DataTableCell dt={famDt} col="noiseMarked" row={f} value={f.noiseMarked} />
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            {replay.recall.held > 0 && (
              <div className="np-recall" role="alert">
                <b>Yeni barın tutacağı gerçek olaylar:</b>
                <ul>
                  {replay.recall.cases.slice(0, 20).map(c => (
                    <li key={c.id}>{c.headline} — {c.priority} · {holdLabel(c.hold)}{c.reason === 'confirmed_case' ? ' · onaylı vaka' : ' · “Gerçek problem” işareti'}</li>
                  ))}
                </ul>
              </div>
            )}
            {replay.notes.length > 0 && <div className="field-hint">{replay.notes.join(' · ')}</div>}
          </>
        )}
      </SettingsSection>

      <SettingsSection title="P3 özet kuyruğu"
        description="Enforce kipinde özete düşen P3 bildirimleri; günün özet saatlerinde kanal ve ekip başına tek mesajda gider.">
        {digest === null ? <div className="field-hint">Kuyruk okunamadı.</div>
          : digest.items.length === 0 ? <div className="field-hint">Bekleyen kalem yok{digest.lastSlot ? ` · son özet ${digest.lastSlot}` : ''}.</div>
            : (
              <>
                <div className="field-hint">{digest.items.length} bekleyen kalem{digest.lastSlot ? ` · son özet ${digest.lastSlot}` : ''}</div>
                <ul className="np-digest">
                  {digest.items.slice(0, 20).map(it => (
                    <li key={it.signature}>{it.headline} — {it.impact}{it.count > 1 ? ` (×${it.count})` : ''}</li>
                  ))}
                </ul>
              </>
            )}
      </SettingsSection>
    </div>
  );
}
