import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { jmxTitle } from './PodJmxInline';

// v0.10.1210 (operatör) — pod paneli başlığı runtime'a göre; satırın kendisi açar.
describe('pod satırı açılır paneli', () => {
  it('başlık keşfedilen metriklerden', () => {
    expect(jmxTitle([])).toBe('Pod kaynak kullanımı');
    expect(jmxTitle(['jvm_memory_used_bytes', 'tomcat_threads_busy'])).toBe('JVM (JMX)');
    expect(jmxTitle(['jboss_datasource_active_count'])).toBe('JBoss / JVM (JMX)');
  });
  it('pod hücresi satırı açar (yalnız bağlantı durdurur); workload öneksiz', () => {
    const src = readFileSync(new URL('./ServicePodsTable.tsx', import.meta.url), 'utf8');
    expect(src).toContain("onClick={e => { if ((e.target as HTMLElement).closest('a')) e.stopPropagation(); }}");
    expect(src).not.toContain('{r.workload.kind}/{r.workload.name}</Link>');
  });
});
