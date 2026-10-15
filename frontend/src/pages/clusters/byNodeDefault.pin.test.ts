import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';

// Operatör isteği (2026-10-15, v0.10.1188): Clusters Overview CPU/Memory
// grafikleri varsayılan "By node" açılır; Total toggle'da kalır.
describe('Clusters CPU/Mem varsayılan kırılım', () => {
  const src = readFileSync(new URL('../Clusters.tsx', import.meta.url), 'utf8');
  it('ikisi de by-node başlar', () => {
    expect(src).toContain('const [cpuByNode, setCpuByNode] = useState(true);');
    expect(src).toContain('const [memByNode, setMemByNode] = useState(true);');
  });
  it('Total şıkkı hâlâ seçilebilir', () => {
    expect(src).toMatch(/by=\{cpuByNode\} onToggle=\{setCpuByNode\}/);
    expect(src).toMatch(/by=\{memByNode\} onToggle=\{setMemByNode\}/);
  });
});
