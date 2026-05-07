export function Spinner() { return <div className="spinner" />; }

// Empty state — accepts either a glyph string (◫, ⚠, ⋮ — the
// CLI-style geometric shapes already in use across the app) or an
// SVG icon node from `components/icons`. Using ReactNode keeps the
// callers backward-compatible without forcing a sweep of every
// existing Empty.
export function Empty({ icon, title, children }: {
  icon: React.ReactNode; title: string; children?: React.ReactNode;
}) {
  return (
    <div className="empty">
      <div className="icon">{icon}</div>
      <h3>{title}</h3>
      {children && <p>{children}</p>}
    </div>
  );
}
