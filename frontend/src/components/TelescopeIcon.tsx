// OpenTelemetry mark — the canonical telescope-through-an-O logo.
// Two visual elements:
//   1. A bold ring (the "O" of OpenTelemetry).
//   2. A diagonal telescope barrel passing through the ring, eyepiece
//      at the bottom-left, objective lens at the top-right, with a
//      small star above the objective ("observing the cosmos").
//
// Default colour is the OTel brand blue (#425CC7).
export function TelescopeIcon({ size = 22, color = '#425CC7', title = 'OpenTelemetry' }: {
  size?: number;
  color?: string;
  title?: string;
}) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" role="img" aria-label={title}>
      <title>{title}</title>
      {/* The "O" — bold open ring centred on the icon */}
      <circle cx="16" cy="16" r="12.5" fill="none" stroke={color} strokeWidth="2.6" />

      {/* Telescope barrel — diagonal cylinder pointing up-right, drawn
          on top of the ring so it appears to pass through. The barrel
          is two parallel strokes; the eyepiece + objective are tiny
          filled caps at each end. */}
      <g transform="rotate(-32 16 16)">
        {/* Barrel body */}
        <rect x="9" y="14.5" width="14" height="3" rx="1.5" fill={color} />
        {/* Eyepiece (lower-left when rotated back) */}
        <rect x="7" y="13.5" width="2.5" height="5" rx="0.6" fill={color} />
        {/* Objective lens (upper-right) */}
        <rect x="22.4" y="13" width="2.6" height="6" rx="1.3" fill={color} />
      </g>

      {/* Star observed by the telescope — upper right, slightly past
          the objective lens. Small enough to read as a glint, not a
          competing element. */}
      <g fill={color}>
        <circle cx="26" cy="6" r="1.2" />
        <circle cx="28" cy="9" r="0.6" opacity="0.7" />
      </g>
    </svg>
  );
}
