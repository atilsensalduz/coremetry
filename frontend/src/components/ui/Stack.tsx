import type { HTMLAttributes, ReactNode } from 'react';

// Stack / Row — minimal flex helpers that map to the existing
// `.stack`, `.row`, `.gap-N`, `.row-end`, `.row-between` CSS
// utilities in globals.css. Intentionally not a full
// utility framework: this is just the recurring 4-token spacing
// pattern (4 / 8 / 12 / 16 / 24) without committing to Tailwind.
//
// Usage:
//   <Stack gap={3}>...</Stack>
//   <Row gap={2} justify="between">...</Row>

type Gap = 1 | 2 | 3 | 4 | 6;

const gapCls = (g?: Gap) => (g ? `gap-${g}` : '');

export interface StackProps extends HTMLAttributes<HTMLDivElement> {
  gap?: Gap;
  children?: ReactNode;
}

export function Stack({ gap, className, children, ...rest }: StackProps) {
  const cls = ['stack', gapCls(gap), className].filter(Boolean).join(' ');
  return <div className={cls} {...rest}>{children}</div>;
}

export interface RowProps extends HTMLAttributes<HTMLDivElement> {
  gap?: Gap;
  // 'end' = flex-end, 'between' = space-between. Keeping the
  // surface narrow on purpose; if a third option appears, fall
  // back to inline style.
  justify?: 'start' | 'end' | 'between';
  wrap?: boolean;
  // grow=true stretches the Row so it consumes free horizontal
  // space — handy in toolbars where the right cluster needs to
  // sit at the edge.
  grow?: boolean;
}

export function Row({
  gap, justify, wrap, grow, className, children, ...rest
}: RowProps) {
  const cls = [
    'row',
    gapCls(gap),
    justify === 'end' ? 'row-end' : justify === 'between' ? 'row-between' : '',
    wrap ? 'row-wrap' : '',
    grow ? 'row-grow' : '',
    className,
  ].filter(Boolean).join(' ');
  return <div className={cls} {...rest}>{children}</div>;
}
