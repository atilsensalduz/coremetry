// OpenTelemetry mark — the canonical hexagon + lens design used on
// opentelemetry.io and the project's brand assets.
//
// Two-tone composition:
//   - Outer hexagon outline in OTel **orange** (#F5A800) — the "O".
//   - Smaller filled hexagon in OTel **blue** (#425CC7) at the upper
//     right — the telescope lens / eyepiece.
//
// The shape stays a single SVG so it inherits sizing cleanly. Pass
// `monoColor` to render the whole thing in a single colour (e.g. for
// a sidebar that wants the icon to match its text colour).
export function TelescopeIcon({ size = 22, monoColor, title = 'OpenTelemetry' }: {
  size?: number;
  monoColor?: string;
  title?: string;
}) {
  const ringColor = monoColor ?? '#F5A800';
  const lensColor = monoColor ?? '#425CC7';
  // Hexagon vertices for the outer ring — flat-top orientation, ~28-
  // unit diameter centred at (16, 17). Trigonometric coordinates:
  // (cx + r·cos(θ), cy + r·sin(θ)) for θ = 0°, 60°, 120°, 180°, 240°, 300°.
  // Pre-computed to keep the runtime trig out of the render path.
  const outer = '28,17 22,27.39 10,27.39 4,17 10,6.61 22,6.61';
  // Smaller hexagon at the upper-right "lens" position, ~8-unit
  // diameter centred at (24.5, 8.5).
  const lens  = '28,8.5 26.25,11.53 22.75,11.53 21,8.5 22.75,5.47 26.25,5.47';
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" role="img" aria-label={title}>
      <title>{title}</title>
      {/* Outer ring: bold polygon outline */}
      <polygon points={outer} fill="none" stroke={ringColor} strokeWidth="2.6" strokeLinejoin="round" />
      {/* Lens / eyepiece: filled hexagon. White stroke gives a subtle
          gap so the lens reads as a separate element when it overlaps
          the outer ring outline. */}
      <polygon points={lens} fill={lensColor} stroke="var(--bg, #0d1117)" strokeWidth="1.4" strokeLinejoin="round" />
    </svg>
  );
}
