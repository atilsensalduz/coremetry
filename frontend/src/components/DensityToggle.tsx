import { useEffect, useState } from 'react';

// Density toggle — global compact/comfortable switch.
// Datadog and Grafana both ship one; the operator who packs
// 30 services into a vertical screen wants compact rows, the
// one investigating a single trace wants the comfortable
// default.
//
// Mechanism: same pattern as ThemeToggle — sets a `data-
// density` attribute on <html> and persists in localStorage.
// All component CSS that wants to react to compact mode reads
// `[data-density="compact"] .selector { … }` in globals.css.
// No JS prop drilling.

type Density = 'comfortable' | 'compact';
const STORAGE_KEY = 'coremetry-density';

export function DensityToggle() {
  const [density, setDensity] = useState<Density>('comfortable');

  useEffect(() => {
    const stored = (() => {
      try { return localStorage.getItem(STORAGE_KEY) as Density | null; } catch { return null; }
    })();
    const initial = stored ?? 'comfortable';
    setDensity(initial);
    document.documentElement.setAttribute('data-density', initial);
  }, []);

  const toggle = () => {
    const next: Density = density === 'comfortable' ? 'compact' : 'comfortable';
    setDensity(next);
    document.documentElement.setAttribute('data-density', next);
    try { localStorage.setItem(STORAGE_KEY, next); } catch { /* noop */ }
  };

  return (
    <button className="theme-toggle" onClick={toggle}
      aria-label={density === 'compact' ? 'Switch to comfortable density' : 'Switch to compact density'}
      title={density === 'compact' ? 'Switch to comfortable density' : 'Switch to compact density'}
      style={{ fontSize: 14, lineHeight: 1 }}>
      {density === 'compact' ? '☰' : '≡'}
    </button>
  );
}
