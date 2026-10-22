// AiToolsetsPanel — v0.10.1434 (CoSRE araştırma programı PR 1;
// docs/audits/cosre-investigation-model-audit.md §6.1). Ayarlar › CoSRE ›
// "Araç setleri" (?tab=toolsets): 74 aracın kaynak bazlı 12 toolset'i, her
// birinin türetilmiş sağlığı ve aç/kapa durumu. GET /api/ai/toolsets her rol
// (viewer salt-okunur GÖRÜR); PUT admin + audit (sunucu kapısı esas —
// buradaki canEdit yalnız düğmeleri gizler).
//
// Tablo türü: statik <table> (T1 gerekçesi — düzenlenebilir sabit katalog:
// 12 satır, sunucu sırası = toolset kataloğunun sırası, sıralanmaz; satır
// başına aç/kapa düğmesi). useDataTable değil: sıralama/yeniden boyutlama
// sabit bir katalogda anlam taşımaz. Polling yok (tek okuma; yazım cevabı
// yeni durumu taşır) — pollingBudget.pin'e girmez.
//
// Renk yalnız sapan değerde (CLAUDE.md tablo standardı): sağlıklı = nötr
// rozet; kısmi = uyarı; yapılandırılmamış = bilgi; erişilemiyor / yetki /
// zaman aşımı / hata = tehlike.
import { useEffect, useState } from 'react';
import { Badge, Button, useConfirm, type Tone } from '@/components/ui';
import { DataTableState } from '@/components/ui/DataTable';
import { useAuthRole } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import { toast } from '@/lib/toast';
import { fmtDateTime } from '@/lib/utils';
import type { AISourceToolset, AISourceToolsetHealthState, AISourceToolsetsPayload } from '@/lib/types';
import { SectionTitle, humanize } from './shared';

const HEALTH_TONE: Record<AISourceToolsetHealthState, Tone> = {
  ok: 'neutral', empty: 'neutral', delayed: 'warning', truncated: 'warning', partial: 'warning',
  not_configured: 'info', unreachable: 'danger', unauthorized: 'danger', timeout: 'danger', error: 'danger',
};

/** SAF: bir toolset kapatılınca/açılınca gönderilecek kapalı ad listesi (sıralı, tekrarsız). */
export function nextDisabled(current: string[], name: string, disable: boolean): string[] {
  const set = new Set(current);
  if (disable) set.add(name); else set.delete(name);
  return [...set].sort();
}

/** SAF: bilinmeyen anahtar için sunucu metnine düşen çeviri. */
function tOr(t: (k: string) => string, key: string, fallback: string): string {
  const v = t(key);
  return v === key ? fallback : v;
}

export function AiToolsetsPanel({ canEdit: canEditProp }: { canEdit?: boolean }) {
  const t = useT();
  const confirm = useConfirm();
  const role = useAuthRole();
  const canEdit = canEditProp ?? role === 'admin';
  const [data, setData] = useState<AISourceToolsetsPayload | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    const ctl = new AbortController();
    setErr(null);
    api.aiToolsets(ctl.signal)
      .then(d => setData(d))
      .catch(e => { if (!ctl.signal.aborted) setErr(humanize(e)); });
    return () => ctl.abort();
  }, [reload]);

  const toggle = async (ts: AISourceToolset) => {
    if (!data) return;
    const disable = ts.enabled;
    if (disable && !await confirm({
      title: t('aiToolsets.confirmTitle').replace('{name}', ts.name),
      body: t('aiToolsets.confirmBody').replace('{n}', String(ts.tools.length)),
      confirmLabel: t('aiToolsets.disable'),
      danger: true,
    })) return;
    setBusy(ts.name);
    try {
      const next = await api.putAIToolsets(nextDisabled(data.disabled, ts.name, disable));
      setData(next);
      toast.success(t('aiToolsets.saved').replace('{name}', ts.name).replace('{state}', t(disable ? 'aiToolsets.off' : 'aiToolsets.on')));
    } catch (e) {
      toast.error(t('aiToolsets.saveFailed').replace('{err}', humanize(e)));
    } finally {
      setBusy(null);
    }
  };

  const toolCount = data?.toolsets.reduce((n, ts) => n + ts.tools.length, 0) ?? 74;
  const rows = data?.toolsets ?? [];
  return (
    <div data-testid="ai-toolsets-panel" style={{ marginTop: 12 }}>
      <SectionTitle>{t('aiToolsets.title')}</SectionTitle>
      <p style={{ fontSize: 12, color: 'var(--text2)', maxWidth: 820, marginTop: 0 }}>
        {t('aiToolsets.intro').replace('{n}', String(toolCount))}
      </p>
      {!canEdit && <p className="field-hint">{t('aiToolsets.readOnly')}</p>}
      {data?.updatedBy && data.updatedAt && (
        <p className="field-hint">
          {t('aiToolsets.lastChange').replace('{by}', data.updatedBy).replace('{at}', fmtDateTime(new Date(data.updatedAt)))}
        </p>
      )}
      {/* v0.10.1434 — statik tablo (T1): düzenlenebilir sabit katalog, 12 satır, sunucu sırası, sıralanmaz. */}
      <table>
        <thead>
          <tr>
            <th>{t('aiToolsets.col.toolset')}</th>
            <th>{t('aiToolsets.col.source')}</th>
            <th>{t('aiToolsets.col.tools')}</th>
            <th>{t('aiToolsets.col.health')}</th>
            <th>{t('aiToolsets.col.status')}</th>
          </tr>
        </thead>
        <tbody>
          {err ? (
            <DataTableState colSpan={5} kind="error" message={t('aiToolsets.loadFailed')} detail={err} onRetry={() => setReload(n => n + 1)} />
          ) : !data ? (
            <DataTableState colSpan={5} kind="loading" skeletonRows={6} />
          ) : rows.length === 0 ? (
            <DataTableState colSpan={5} kind="empty" message={t('aiToolsets.empty')} />
          ) : rows.map(ts => {
            const h = ts.health;
            const healthNote = [h.detail, ...(h.notes ?? [])].filter(Boolean).join(' · ');
            return (
              <tr key={ts.name} data-toolset={ts.name}>
                <td className="mono">
                  {ts.name}
                  {ts.required && <> <Badge tone="neutral">{t('aiToolsets.required')}</Badge></>}
                </td>
                <td>{tOr(t, `aiToolsets.src.${ts.name}`, ts.source)}</td>
                <td title={ts.tools.join(', ')}>
                  <span className="cell-strong">{ts.tools.length}</span>
                  <div className="field-hint mono">{ts.tools.join(', ')}</div>
                </td>
                <td>
                  <Badge tone={HEALTH_TONE[h.state] ?? 'neutral'} data-health={h.state}>
                    {tOr(t, `aiToolsets.health.${h.state}`, h.state)}
                  </Badge>
                  {h.backend && <span className="cell-hint mono">{h.backend}</span>}
                  {healthNote && <div className="field-hint">{healthNote}</div>}
                </td>
                <td>
                  <Badge tone={ts.enabled ? 'neutral' : 'warning'}>{t(ts.enabled ? 'aiToolsets.on' : 'aiToolsets.off')}</Badge>
                  {canEdit && !ts.required && (
                    <>
                      {' '}
                      <Button variant="secondary" size="xs" loading={busy === ts.name} disabled={busy !== null}
                        onClick={() => { void toggle(ts); }}>
                        {t(ts.enabled ? 'aiToolsets.disable' : 'aiToolsets.enable')}
                      </Button>
                    </>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
