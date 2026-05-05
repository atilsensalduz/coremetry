'use client';
import { useEffect, useId, useRef, useState } from 'react';
import { api } from '@/lib/api';

/**
 * ServicePicker — drop-in replacement for the old `<Combobox options={services}>`
 * pattern. Fetches matching service names from /api/service-names with a
 * debounced query so it works at any scale (10k+ services).
 *
 * Why not just preload all names client-side?
 *   /api/services is top-N capped for the dashboard view, which used to
 *   silently truncate every service-name dropdown that scraped its
 *   response. This component asks the dedicated /api/service-names
 *   endpoint instead — uncapped, MV-backed, supports `*` / `?`
 *   wildcards (e.g. `pay*`, `*pay*`, `p?y`).
 *
 * The page count badge ("showing 50 of 1234 — type to refine") helps
 * users understand they're seeing a subset and need to type to narrow.
 */
export function ServicePicker({
  value, onChange, placeholder, width, onEnter,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  width?: number | string;
  onEnter?: () => void;
}) {
  const listId = useId();
  const [opts, setOpts] = useState<string[]>([]);
  const [total, setTotal] = useState(0);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Debounced server fetch keyed off the typed value. Empty value → load
  // top-200 (alphabetical). Updates the datalist options so the browser's
  // native dropdown reflects whatever the user is filtering for.
  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      api.serviceNames(value, 200)
        .then(r => { setOpts(r.names); setTotal(r.total); })
        .catch(() => { setOpts([]); setTotal(0); });
    }, 180);
    return () => { if (debounceRef.current) clearTimeout(debounceRef.current); };
  }, [value]);

  const truncated = total > opts.length;

  return (
    <div className="cb-wrap" style={{ width }}>
      <input
        list={listId}
        value={value}
        placeholder={placeholder}
        onChange={e => onChange(e.target.value)}
        onKeyDown={e => e.key === 'Enter' && onEnter?.()}
        autoComplete="off"
        spellCheck={false}
        title={
          truncated
            ? `Showing ${opts.length} of ${total} services — type to refine. Wildcards: pay*, *pay*, p?y`
            : 'Type to filter. Wildcards: pay*, *pay*, p?y'
        }
      />
      {value && (
        <button className="cb-clear" type="button"
          aria-label="Clear" title="Clear"
          onClick={() => onChange('')}
          onMouseDown={e => e.preventDefault()}>
          ✕
        </button>
      )}
      <datalist id={listId}>
        {opts.map(o => <option key={o} value={o} />)}
        {truncated && <option value="" disabled>… +{total - opts.length} more — refine search</option>}
      </datalist>
    </div>
  );
}
