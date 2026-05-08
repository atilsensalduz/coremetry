// OpenTelemetry brand mark — the official logo path from
// Simple Icons / techicons.dev. Single solid path that forms the
// otto-the-mascot silhouette: an "O" with a stylised tail.
//
// Defaults to the brand orange (#F5A800) so the icon keeps the
// "this is OpenTelemetry" recognisability across the sidebar,
// login, and public pages without per-call configuration. Pass
// `color` to swap (`'currentColor'` to follow surrounding text).
export function TelescopeIcon({ size = 22, color = '#F5A800', title = 'OpenTelemetry' }: {
  size?: number;
  color?: string;
  title?: string;
}) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" role="img" aria-label={title}
         fill={color}>
      <title>{title}</title>
      <path d="M16.5 8.7c-.5 0-.9-.1-1.3-.4l-2.2-2.2c-.4-.4-.4-1.1 0-1.5L17.4.3c.4-.4 1.1-.4 1.5 0l2.2 2.2c.4.4.4 1.1 0 1.5l-3.4 3.4c-.4.4-.8.6-1.3.6l.1.7zM3.6 24c-1 0-1.9-.4-2.6-1.1-1.4-1.4-1.4-3.7 0-5.1l5.6-5.6c1.4-1.4 3.7-1.4 5.1 0 .5.5.9 1.2 1.1 1.9.1.6.1 1.3-.1 1.9l-2.5-2.5c-.5-.5-1.4-.5-1.9 0l-3.7 3.7c-.5.5-.5 1.4 0 1.9.5.5 1.4.5 1.9 0l3-3 1.9 1.9-3 3c-.7.7-1.6 1-2.6 1zm10.6-6.7c-1 0-1.9-.4-2.6-1.1-1.5-1.5-1.5-3.7 0-5.1l3-3 1.9 1.9-3 3c-.5.5-.5 1.4 0 1.9.5.5 1.4.5 1.9 0l3.6-3.6c.5-.5.5-1.4 0-1.9l-2.5-2.5c.6-.2 1.3-.2 1.9-.1.7.2 1.4.5 1.9 1.1 1.4 1.4 1.4 3.7 0 5.1l-3.6 3.6c-.7.5-1.6.7-2.5.7zm-4.5-4.5c-.6 0-1.2.5-1.2 1.2s.5 1.2 1.2 1.2 1.2-.5 1.2-1.2-.6-1.2-1.2-1.2z"/>
    </svg>
  );
}
