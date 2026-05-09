import type { ReactNode } from 'react';

// Tabs — typed wrapper for the `.tab-strip` CSS pattern. Used in
// Settings, Anomalies, the Trace detail split-pane, etc.
// Stateless: caller owns the `value` and the `onChange` so the
// active tab can survive mount via URL state, parent state, or a
// reducer — whatever matches the page.
//
// Accessibility: tab buttons get `role="tab"` and the active one
// gets `aria-selected`. Keyboard-aware (Left/Right arrow + Home/
// End cycle through tabs without leaving the strip).

export interface TabItem<T extends string = string> {
  key: T;
  label: ReactNode;
  hint?: string;
  // disabled keeps the tab in the strip for layout consistency
  // but blocks interaction (and dims it) — useful for "coming
  // soon" or "loading" states.
  disabled?: boolean;
}

export interface TabsProps<T extends string = string> {
  items: TabItem<T>[];
  value: T;
  onChange: (next: T) => void;
  // segmented = the joined-button variant for view-toggles
  // (Aggregated vs List on /traces).  default = underlined tab
  // strip for top-of-page section nav.
  variant?: 'tabs' | 'segmented';
  ariaLabel?: string;
}

export function Tabs<T extends string = string>({
  items, value, onChange, variant = 'tabs', ariaLabel,
}: TabsProps<T>) {
  const cls = variant === 'segmented' ? 'segmented' : 'tab-strip';

  // Arrow-key navigation across the strip. Mouseclick is the
  // common path; this is the keyboard-equivalent so power users
  // and screen readers can move through tabs.
  const onKey = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const enabled = items.filter(i => !i.disabled);
    const idx = enabled.findIndex(i => i.key === value);
    if (idx === -1) return;
    let next = idx;
    if (e.key === 'ArrowRight') next = (idx + 1) % enabled.length;
    else if (e.key === 'ArrowLeft') next = (idx - 1 + enabled.length) % enabled.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = enabled.length - 1;
    else return;
    e.preventDefault();
    onChange(enabled[next].key);
  };

  return (
    <div className={cls} role="tablist" aria-label={ariaLabel} onKeyDown={onKey}>
      {items.map(item => {
        const active = item.key === value;
        return (
          <button
            key={item.key}
            type="button"
            role="tab"
            aria-selected={active}
            aria-controls={`tab-panel-${item.key}`}
            disabled={item.disabled}
            tabIndex={active ? 0 : -1}
            title={item.hint}
            className={active ? 'active' : ''}
            onClick={() => !item.disabled && onChange(item.key)}>
            {item.label}
          </button>
        );
      })}
    </div>
  );
}
