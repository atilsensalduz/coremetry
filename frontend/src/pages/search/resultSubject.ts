// resultSubject — v0.10.1335: arama sonucu → (a) CoSRE öznesi, (b) çekmecenin
// tek birincil "Open in …" hedefi. SAF; resultSubject.test.ts tabloyu çiviler.
//
// Özne haritası (operatör onaylı): trace→trace, problem→problem,
// exception→exception (parmak izi), service→service-health{fromNs,toNs},
// log→log (lib/logLineSubject — servis + zaman yoksa null), endpoint→ ÜST
// servisin service-health'i (endpoint'in kendi öznesi yok; boş vaat yerine
// servis sağlığı sorulur).
//
// Hedefler mevcut kurucular üzerinden (pencere unutulmasın — pivotHref
// sözleşmesi): traceHref, logsHref, sunucunun problem/exception href'i,
// serviceHref, operationTracesHref. Sunucu href'i yalnız isSafeAppPath'ten
// geçerse kullanılır.
import type { AISubject } from '@/lib/aiSubject';
import { logLineSubjectOf } from '@/lib/logLineSubject';
import { logsHref } from '@/lib/logsUrl';
import { operationTracesHref } from '@/lib/pivotHref';
import { isSafeAppPath } from '@/lib/safeAppPath';
import { serviceHref } from '@/lib/serviceHref';
import { traceHref } from '@/lib/traceHref';
import type { SearchResult, TimeRange } from '@/lib/types';

export function resultSubject(r: SearchResult, win: { fromNs: number; toNs: number }): AISubject | null {
  switch (r.type) {
    case 'trace':
      return { kind: 'trace', id: r.id };
    case 'problem':
      return { kind: 'problem', id: r.id };
    case 'exception':
      return { kind: 'exception', id: r.payload?.fingerprint || r.id };
    case 'service':
      return r.id ? { kind: 'service-health', id: r.id, fromNs: win.fromNs, toNs: win.toNs } : null;
    case 'endpoint': {
      const svc = r.payload?.service || r.service;
      return svc ? { kind: 'service-health', id: svc, fromNs: win.fromNs, toNs: win.toNs } : null;
    }
    case 'log': {
      const p = r.payload;
      if (!p) return null;
      return logLineSubjectOf({
        timestamp: p.timestamp, serviceName: p.serviceName, traceId: p.traceId ?? '',
        body: p.body, attributes: {}, resourceAttributes: {},
      });
    }
  }
}

/** Birincil eylemin etiket anahtarı (i18n) ve hedefi; hedef güvenli değilse null. */
export interface ResultOpenTarget { labelKey: string; href: string }

export function resultOpenTarget(
  r: SearchResult, ctx: { range: TimeRange; env: string; q: string },
): ResultOpenTarget | null {
  const safe = (h: string): string | null => (h && isSafeAppPath(h) ? h : null);
  let href: string | null = null;
  let labelKey = '';
  switch (r.type) {
    case 'trace':
      href = traceHref(r.id, { pageRange: ctx.range });
      labelKey = 'search.open.trace';
      break;
    case 'log':
      href = logsHref({ window: ctx.range, service: r.service || undefined, q: ctx.q.trim(), env: ctx.env || null });
      labelKey = 'search.open.log';
      break;
    case 'problem':
      href = safe(r.href);
      labelKey = 'search.open.problem';
      break;
    case 'exception':
      href = safe(r.href) ?? `/problems?exc=${encodeURIComponent(r.id)}`;
      labelKey = 'search.open.exception';
      break;
    case 'service':
      href = serviceHref(r.id, { range: ctx.range, env: ctx.env || null });
      labelKey = 'search.open.service';
      break;
    case 'endpoint': {
      const name = r.payload?.name || r.title;
      href = operationTracesHref({ window: ctx.range, operation: name, service: r.payload?.service || r.service || undefined });
      labelKey = 'search.open.endpoint';
      break;
    }
  }
  return href && isSafeAppPath(href) ? { labelKey, href } : null;
}
