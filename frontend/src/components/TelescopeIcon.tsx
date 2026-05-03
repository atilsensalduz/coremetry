// OpenTelemetry-flavoured telescope mark, sized + coloured per the call
// site. Default colour is the OTel brand blue (#425CC7).
//
// Stylised — the official OTel logo is a stroked "O" forming the
// barrel; a literal telescope-on-tripod reads better at sidebar size.
export function TelescopeIcon({ size = 22, color = '#425CC7', title = 'OpenTelemetry' }: {
  size?: number;
  color?: string;
  title?: string;
}) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" role="img" aria-label={title}>
      <title>{title}</title>
      {/* Tripod legs */}
      <g stroke={color} strokeWidth="1.6" strokeLinecap="round">
        <line x1="12" y1="13" x2="6"  y2="22" />
        <line x1="12" y1="13" x2="18" y2="22" />
        <line x1="12" y1="13" x2="12" y2="22" />
      </g>
      {/* Telescope barrel — diagonal cylinder pointing up-right */}
      <g transform="rotate(-30 12 10)">
        <rect x="3.5" y="7" width="14" height="6" rx="1.6"
              fill={color} fillOpacity="0.85"/>
        {/* Eyepiece */}
        <rect x="2.2" y="8.4" width="2.6" height="3.2" rx="0.5"
              fill={color} />
        {/* Objective lens */}
        <ellipse cx="17" cy="10" rx="1.2" ry="3"
                 fill={color} fillOpacity="0.55" />
      </g>
      {/* "Observed" star above objective */}
      <circle cx="20" cy="3.5" r="1.1" fill={color} />
    </svg>
  );
}
