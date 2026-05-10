// Runtime brand icons — ElasticAPM-style. Recognisable
// silhouettes for each language stack, drawn as monochrome
// SVG so they inherit the surrounding text colour. The shape
// alone tells the operator "this service runs Java / Python /
// Go" at a glance; the colour stays neutral to match the
// design-system palette.
//
// Paths are simplified hand-drawn approximations rather than
// pixel-accurate brand logos — small enough that fidelity
// doesn't matter, large enough to read against the badge
// background. Single 16×16 viewbox per icon.
//
// Why not an external icon package: adding @icons-pack/react-
// simple-icons or devicons would pull ~2 MB of svg paths into
// the bundle for the 8 we use. Inline keeps it tight.

import type { SVGProps } from 'react';

interface RuntimeIconProps extends Omit<SVGProps<SVGSVGElement>, 'children'> {
  size?: number;
}

function Wrap({ size = 14, ...rest }: RuntimeIconProps & { children?: React.ReactNode }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16"
         fill="currentColor" stroke="none"
         {...rest} />
  );
}

// Java — stylised steaming cup silhouette.
export const IconJava = (p: RuntimeIconProps) => (
  <Wrap {...p}>
    <path d="M5.5 2c-.5.5-.7 1 .2 1.6.8.5.6 1.2 0 1.6-1 .5-1.4 1.4-.5 2 .5.4 1 .3 1.2-.2.2-.5-.3-1 0-1.4.4-.6.7-1.2.4-2-.3-.8-1.6-1.1-1.3-1.6z" />
    <path d="M3.6 9.3c0 .9 1.3 1.4 4.4 1.4s4.4-.5 4.4-1.4c0-.4-.4-.6-.8-.7.2.5-.5 1-3.6 1s-3.8-.5-3.6-1c-.4.1-.8.3-.8.7z" />
    <path d="M3 11.6c0 1.4 1.4 2 5 2s5-.6 5-2c0-.5-.4-.8-1-.9.2.6-.7 1.4-4 1.4s-4.2-.8-4-1.4c-.6.1-1 .4-1 .9z" />
    <path d="M9.7 5.4c1.6.7 1.5 1.6.5 2.3.2.1.4.3.4.6 1.4-.7 1.6-2 .3-2.7-.6-.3-1.1-.4-1.2-.2z" />
  </Wrap>
);

// .NET — angled chevron / "N" mark.
export const IconDotNet = (p: RuntimeIconProps) => (
  <Wrap {...p}>
    <path d="M2 4h1.6l3 5.4V4h1.4v8H6.4l-3-5.3V12H2zm9 0h3.5v1.2h-2v2h2v1.2h-2V12H11z" />
    <circle cx="9.5" cy="11" r=".7" />
  </Wrap>
);

// Go — stylised gopher head silhouette (simplified).
export const IconGo = (p: RuntimeIconProps) => (
  <Wrap {...p}>
    <ellipse cx="6.5" cy="7" rx="3" ry="3.5" />
    <ellipse cx="11" cy="7" rx="2.4" ry="3" />
    <circle cx="5.5" cy="6" r=".5" fill="#fff" />
    <circle cx="10.5" cy="6.2" r=".4" fill="#fff" />
    <path d="M5 11c0 1 1 1.6 2 1.6s2-.6 2-1.6" fill="none" stroke="currentColor" strokeWidth="0.8" />
    <path d="M2 5l1 1m11-1l-1 1" stroke="currentColor" strokeWidth="1" fill="none" />
  </Wrap>
);

// Node.js — hexagonal mark with a wedge.
export const IconNode = (p: RuntimeIconProps) => (
  <Wrap {...p}>
    <path d="M8 1.2L1.5 4.8v6.4L8 14.8l6.5-3.6V4.8L8 1.2zm0 1.5l5.2 2.9v5.8L8 13.3 2.8 10.4V4.6L8 2.7z" />
    <path d="M8 5.5v5l-1.5-.8c-.3-.2-.5-.5-.5-.9V7.4l1 .5v2l1 .5z" />
  </Wrap>
);

// Python — two intertwined snake heads.
export const IconPython = (p: RuntimeIconProps) => (
  <Wrap {...p}>
    <path d="M7 1.5c-2 0-3.2.7-3.2 2v1.7H8V6H3.2c-1.4 0-2.5.9-2.5 3 0 1.7.8 3 2.2 3h1.4V10c0-1.4 1.1-2.5 2.5-2.5h3c1.2 0 2.2-1 2.2-2.2V3.5c0-1.3-1-2-3-2zm-1 1.4c.4 0 .7.3.7.7s-.3.7-.7.7-.7-.3-.7-.7.3-.7.7-.7z" />
    <path d="M9 14.5c2 0 3.2-.7 3.2-2v-1.7H8V10h4.8c1.4 0 2.5-.9 2.5-3 0-1.7-.8-3-2.2-3h-1.4v2c0 1.4-1.1 2.5-2.5 2.5h-3C5 8.5 4 9.5 4 10.7v1.8c0 1.3 1 2 3 2zm1-1.4c-.4 0-.7-.3-.7-.7s.3-.7.7-.7.7.3.7.7-.3.7-.7.7z" />
  </Wrap>
);

// Ruby — gem with facets.
export const IconRuby = (p: RuntimeIconProps) => (
  <Wrap {...p}>
    <path d="M3 6L8 1.5 13 6 8 14.5z M3 6h10 M5 6L8 1.5L11 6 M5 6L8 14.5L11 6"
          stroke="currentColor" strokeWidth="0.9" fill="none" strokeLinejoin="round" />
  </Wrap>
);

// PHP — stylised "<?>".
export const IconPHP = (p: RuntimeIconProps) => (
  <Wrap {...p}>
    <ellipse cx="8" cy="8" rx="7" ry="4" fill="none" stroke="currentColor" strokeWidth="0.9" />
    <text x="8" y="10.2" textAnchor="middle" fontSize="6" fontFamily="monospace" fontWeight="700" fill="currentColor">php</text>
  </Wrap>
);

// Rust — gear with R inside.
export const IconRust = (p: RuntimeIconProps) => (
  <Wrap {...p}>
    <path d="M8 1.5l1 1.7 1.7-.5.4 1.7 1.7.4-.5 1.7 1.7 1-1.7 1 .5 1.7-1.7.4-.4 1.7-1.7-.5-1 1.7-1-1.7-1.7.5-.4-1.7-1.7-.4.5-1.7-1.7-1 1.7-1-.5-1.7L4.9 2.7l.4-1.7L7 1.5z"
          fill="none" stroke="currentColor" strokeWidth="0.7" />
    <text x="8" y="10.3" textAnchor="middle" fontSize="5.5" fontFamily="monospace" fontWeight="700" fill="currentColor">R</text>
  </Wrap>
);

// Generic — plain dot for unknown languages.
export const IconGeneric = (p: RuntimeIconProps) => (
  <Wrap {...p}>
    <circle cx="8" cy="8" r="3" fill="none" stroke="currentColor" strokeWidth="1" />
    <circle cx="8" cy="8" r="1" />
  </Wrap>
);

// LanguageIcon — dispatches on the OTel telemetry.sdk.language
// value. Anything unknown falls to the generic dot so a service
// emitting an unmapped runtime still gets a badge.
export function LanguageIcon({ lang, size = 14, ...rest }: RuntimeIconProps & { lang?: string }) {
  switch ((lang || '').toLowerCase()) {
    case 'java':
    case 'kotlin':     return <IconJava size={size} {...rest} />;
    case 'dotnet':
    case 'csharp':     return <IconDotNet size={size} {...rest} />;
    case 'go':         return <IconGo size={size} {...rest} />;
    case 'nodejs':
    case 'javascript':
    case 'webjs':      return <IconNode size={size} {...rest} />;
    case 'python':     return <IconPython size={size} {...rest} />;
    case 'ruby':       return <IconRuby size={size} {...rest} />;
    case 'php':        return <IconPHP size={size} {...rest} />;
    case 'rust':       return <IconRust size={size} {...rest} />;
    default:           return <IconGeneric size={size} {...rest} />;
  }
}
