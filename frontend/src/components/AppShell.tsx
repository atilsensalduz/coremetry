import { Outlet, useLocation } from 'react-router-dom';
import { Sidebar } from './Sidebar';
import { useAuth } from './AuthProvider';
import { useEventStream } from '@/lib/queries';
import { isPublicPath } from '@/lib/auth-paths';

// AppShell is the layout-route wrapper. React Router renders the
// active child route inside <Outlet/>. Public pages (login,
// public-status, public/trace) bypass the sidebar by being
// registered OUTSIDE this layout in App.tsx — but we keep the
// isPublicPath check as a defensive belt-and-suspenders so a
// future route refactor that accidentally puts a public page
// under this layout still won't render the sidebar to a
// not-yet-authenticated visitor.
export function AppShell() {
  const { pathname } = useLocation();
  const { user, loading } = useAuth();
  const isPublic = isPublicPath(pathname);

  // SSE event stream — opens once we're authed + outside the
  // public surface (login, public-status). Receives
  // problem.open / problem.resolve / anomaly.* events and
  // invalidates the matching React Query caches so live state
  // changes show up in <1s. Closes on logout / unmount.
  useEventStream(!!user && !isPublic);

  if (isPublic) {
    return <Outlet />;
  }
  if (loading) {
    return (
      <div style={{
        position: 'fixed', inset: 0, display: 'grid', placeItems: 'center',
        color: 'var(--text3)', fontSize: 13,
      }}>
        Loading…
      </div>
    );
  }
  if (!user) {
    // AuthProvider is in the middle of redirecting to /login.
    return null;
  }
  return (
    <div id="app">
      <Sidebar />
      <div id="main"><Outlet /></div>
    </div>
  );
}
