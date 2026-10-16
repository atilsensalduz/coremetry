import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1240 — trace → metrics pivotu: kök servis + trace penceresi + env.
const src = readFileSync(resolve(__dirname, 'Trace.tsx'), 'utf8');

describe('Trace → Metrics', () => {
  it('kök servisle, trace penceresinde, env taşıyarak', () => {
    expect(src).toMatch(/<DrillButton to="\/metrics"\s+params=\{\{ service: root\.serviceName, editor: '0', range: logsRangeParam\(logWin\?\.from, logWin\?\.to\) \}\}\s+carry=\{\['env'\]\}/);
  });
});
