import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import type { SavedView } from '@/lib/types';

// SavedViewsBar lives at the top of filter-heavy pages (/traces,
// /logs, /anomalies) and gives the operator a one-click way to
// stash the current URL filter combo and recall it later. The
// query state is the page's existing URL search string — saving
// = remembering window.location.search, applying = restoring it.
// No coupling between server schema and SPA filter shape.
//
// Permissions:
//   - Anyone signed in can save personal views.
//   - Admins can flip "shared" so everyone on the team sees it.
//   - You can only delete your own views (admins can delete any).
export function SavedViewsBar({ page }: { page: string }) {
  const navigate = useNavigate();
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';

  const [views, setViews] = useState<SavedView[] | undefined>(undefined);
  const [showSaver, setShowSaver] = useState(false);
  const [name, setName] = useState('');
  const [shared, setShared] = useState(false);

  useEffect(() => {
    api.savedViews(page).then(v => setViews(v ?? [])).catch(() => setViews([]));
  }, [page]);

  const apply = (v: SavedView) => {
    const target = window.location.pathname + (v.queryString ? '?' + v.queryString : '');
    navigate(target);
  };

  const save = async () => {
    const trimmed = name.trim();
    if (!trimmed) return;
    // Strip leading '?' so the server stores a stable search string.
    const qs = window.location.search.replace(/^\?/, '');
    await api.createSavedView({ name: trimmed, page, queryString: qs, shared });
    setName('');
    setShared(false);
    setShowSaver(false);
    api.savedViews(page).then(v => setViews(v ?? []));
  };

  const remove = async (v: SavedView) => {
    if (!confirm(`Delete saved view "${v.name}"?`)) return;
    await api.deleteSavedView(v.id);
    api.savedViews(page).then(v => setViews(v ?? []));
  };

  if (views === undefined) return null;

  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8,
      flexWrap: 'wrap', fontSize: 11,
    }}>
      <span style={{ color: 'var(--text3)', marginRight: 2 }}>Saved:</span>
      {views.length === 0 && (
        <span style={{ color: 'var(--text3)', fontStyle: 'italic' }}>
          (none yet — Save current view to pin a filter combo)
        </span>
      )}
      {views.map(v => (
        <span key={v.id} style={{
          display: 'inline-flex', alignItems: 'center', gap: 4,
          padding: '2px 8px', borderRadius: 3,
          background: v.ownerId === '' ? 'rgba(56,139,253,.10)' : 'var(--bg3)',
          border: v.ownerId === '' ? '1px solid rgba(56,139,253,.35)' : '1px solid var(--border)',
        }}>
          <button type="button" onClick={() => apply(v)}
            style={{
              background: 'transparent', border: 'none', cursor: 'pointer',
              color: 'var(--text)', padding: 0, fontSize: 11,
            }}
            title={v.ownerId === '' ? 'Team-shared view' : 'Your view'}>
            {v.ownerId === '' && <span style={{ fontSize: 9, marginRight: 4 }}>★</span>}
            {v.name}
          </button>
          {(v.ownerId === user?.id || isAdmin) && (
            <button type="button" onClick={() => remove(v)} title="Delete"
              style={{
                background: 'transparent', border: 'none', cursor: 'pointer',
                color: 'var(--text3)', padding: 0, lineHeight: 1, fontSize: 11,
              }}>×</button>
          )}
        </span>
      ))}
      <button type="button"
        onClick={() => setShowSaver(s => !s)}
        style={{
          padding: '2px 8px', fontSize: 11, borderRadius: 3,
          background: 'var(--bg3)', border: '1px solid var(--border)',
          color: 'var(--accent2)', cursor: 'pointer',
        }}>
        {showSaver ? '✕ Cancel' : '＋ Save current view'}
      </button>

      {showSaver && (
        <span style={{
          display: 'inline-flex', alignItems: 'center', gap: 6,
          padding: '4px 8px', borderRadius: 3,
          background: 'var(--bg2)', border: '1px solid var(--border)',
        }}>
          <input autoFocus
            placeholder="Name…"
            value={name}
            onChange={e => setName(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter') save(); }}
            style={{ width: 200, fontSize: 11 }} />
          {isAdmin && (
            <label style={{ display: 'flex', alignItems: 'center', gap: 4, color: 'var(--text2)' }}>
              <input type="checkbox" checked={shared}
                onChange={e => setShared(e.target.checked)} />
              Share with team
            </label>
          )}
          <button type="button" onClick={save}
            style={{ padding: '2px 10px', fontSize: 11 }}>
            Save
          </button>
        </span>
      )}
    </div>
  );
}
