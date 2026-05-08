// OpenTelemetry brand mark — the official two-tone "ring + filled
// disc" composition from cncf/artwork (projects/opentelemetry/icon).
// Solid blue disc inside an orange ring; same proportions as the
// SVG asset on opentelemetry.io.
//
// `monoColor` collapses both layers to one colour so the icon can
// render flat in a sidebar that wants it to match the surrounding
// text colour.
export function TelescopeIcon({ size = 22, monoColor, title = 'OpenTelemetry' }: {
  size?: number;
  monoColor?: string;
  title?: string;
}) {
  const ring = monoColor ?? '#F5A800'; // OTel orange
  const disc = monoColor ?? '#425CC7'; // OTel blue
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" role="img" aria-label={title}>
      <title>{title}</title>
      {/* Solid blue disc — inner element of the mark */}
      <circle cx="16" cy="16" r="11.62" fill={disc} />
      {/* Orange ring — drawn as an outer disc with the inner cut out
          via fillRule="evenodd" so the disc above shows through. */}
      <path
        fill={ring}
        fillRule="evenodd"
        d="M16,2.62 A13.38,13.38 0 1 0 16,29.38 A13.38,13.38 0 1 0 16,2.62 Z
           M16,6.38 A9.62,9.62 0 1 1 16,25.62 A9.62,9.62 0 1 1 16,6.38 Z"
      />
    </svg>
  );
}
