// FacetRail — v0.10.1335: /search facet rayı. Dört FacetMultiSelect (Tür,
// Servis, Ortam, Önem ≥) + problem durumu (Açık | Tümü).
//
// Sayılar cevabın facet'lerinden: tür sayımı okuyucunun toplamı (kesikse
// "N+"), servis/ortam/önem sayımı yalnız DÖNEN satırlar üstünde — rayın
// altındaki not bunu söyler ("within shown results"). Sayısı 0 olan seçenek
// soluk; seçilmeyen tür sorgulanmadığı için sayısı "—".
//
// Servis / ortam / önem sunucuda TEK değer (service=, env=, sev= tabanı):
// atom çoklu seçim gibi görünse de seçim tek değere iner — bir satıra tık o
// değeri seçer, seçili olana tık temizler, "tümünü seç" temizler. Önem bir
// TABANDIR (critical ⊂ warning ⊂ info): taban altındaki seviyeler kapalı görünür.
import { FacetMultiSelect, SegmentedControl } from '@/components/ui';
import type { SearchFacets, SearchResultType, SearchSev, SearchStatus } from '@/lib/types';
import { SEARCH_SEVS, SEARCH_TYPES, type SearchUrlState } from '@/lib/searchUrl';
import { typeCountLabel } from './searchText';

const SEV_RANK: Record<SearchSev, number> = { critical: 3, warning: 2, info: 1 };

export function FacetRail({ facets, state, env, t, onPatch, onEnv }: {
  facets: SearchFacets | undefined;
  state: SearchUrlState;
  env: string;
  t: (k: string) => string;
  onPatch: (p: Partial<SearchUrlState>) => void;
  onEnv: (e: string) => void;
}) {
  const typeSet = new Set<string>(state.types);
  const typeOptions = SEARCH_TYPES.map(ty => {
    const f = facets?.type[ty];
    return {
      value: ty, label: t(`search.type.${ty}`),
      count: f?.count ?? 0,
      countLabel: typeSet.has(ty) ? typeCountLabel(f) : '—',
    };
  });
  const toggleType = (v: string) => {
    const next = new Set(typeSet);
    if (next.has(v)) next.delete(v); else next.add(v);
    onPatch({ types: SEARCH_TYPES.filter(x => next.has(x)) as SearchResultType[], sel: null });
  };

  const serviceOptions = singleOptions(facets?.service, state.service);
  const envOptions = singleOptions(facets?.env, env);

  const sevOptions = SEARCH_SEVS.map(s => ({ value: s, label: s, count: facets?.sev[s] ?? 0 }));
  const sevSelected = new Set<string>(state.sev
    ? SEARCH_SEVS.filter(s => SEV_RANK[s] >= SEV_RANK[state.sev as SearchSev])
    : SEARCH_SEVS);
  const pickSev = (v: string) => onPatch({ sev: state.sev === v ? '' : (v as SearchSev), sel: null });

  return (
    <aside className="srch-rail" aria-label="Facets">
      <FacetMultiSelect label={t('search.facet.type')} options={typeOptions} selected={typeSet}
        onToggle={toggleType}
        onSolo={v => onPatch({ types: [v as SearchResultType], sel: null })}
        onAll={() => onPatch({ types: [...SEARCH_TYPES], sel: null })} />
      <FacetMultiSelect label={t('search.facet.service')} options={serviceOptions}
        selected={selectedSingle(serviceOptions, state.service)}
        onToggle={v => onPatch({ service: state.service === v ? '' : v, sel: null })}
        onSolo={v => onPatch({ service: v, sel: null })}
        onAll={() => onPatch({ service: '', sel: null })} />
      <FacetMultiSelect label={t('search.facet.env')} options={envOptions}
        selected={selectedSingle(envOptions, env)}
        onToggle={v => onEnv(env === v ? '' : v)}
        onSolo={v => onEnv(v)}
        onAll={() => onEnv('')} />
      <FacetMultiSelect label={t('search.facet.sev')} options={sevOptions} selected={sevSelected}
        onToggle={pickSev} onSolo={pickSev} onAll={() => onPatch({ sev: '', sel: null })} />
      <div className="srch-rail-row">
        <span className="srch-rail-l">{t('search.facet.status')}</span>
        <SegmentedControl<SearchStatus> size="sm" aria-label={t('search.facet.status')}
          value={state.status}
          onChange={v => onPatch({ status: v, sel: null })}
          options={[
            { value: 'open', label: t('search.status.open') },
            { value: 'all', label: t('search.status.all') },
          ]} />
      </div>
      <p className="srch-rail-note">{t('search.facet.scope')}</p>
    </aside>
  );
}

/** Tek-değerli facet seçenekleri: sayıya göre azalan; seçili değer cevapta
 *  yoksa (0 sayı) yine listede durur — seçimi görünmez kılmayız. */
function singleOptions(counts: Record<string, number> | undefined, selected: string) {
  const entries = Object.entries(counts ?? {});
  if (selected && !entries.some(([k]) => k === selected)) entries.push([selected, 0]);
  return entries
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([value, count]) => ({ value, label: value, count }));
}

function selectedSingle(options: { value: string }[], selected: string): Set<string> {
  return selected ? new Set([selected]) : new Set(options.map(o => o.value));
}
