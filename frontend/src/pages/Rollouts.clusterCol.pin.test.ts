// Rollouts.clusterCol.pin.test.ts — v0.10.565 (operatör-raporlu, prod
// ekran görüntüsü): Rollouts listesinde cluster adı Workload hücresinin
// soluk ekindeydi ve kırpılıyordu. Sözleşme: cluster KENDİ kolonunda,
// Workload'dan hemen sonra; workload eki yalnız namespace taşır.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'Rollouts.tsx'), 'utf8');

describe('Rollouts cluster kolonu', () => {
  it('COLS: cluster, workload ile kind arasında', () => {
    const w = src.indexOf("{ id: 'workload', label: 'Workload'");
    const c = src.indexOf("{ id: 'cluster', label: 'Cluster'");
    const k = src.indexOf("{ id: 'kind', label: 'Tür'");
    expect(w).toBeGreaterThan(0);
    expect(c).toBeGreaterThan(w);
    expect(k).toBeGreaterThan(c);
  });
  // v0.10.945 (tablo standardı) — hücre sözleşmesi: dizge `value` tam adı
  // `title`a koyar (cellProps), "…" tabandan (`tbody td`); satır içi stil yok.
  it('hücre: çözülmüş ad + title, ellipsis', () => {
    expect(src).toContain('<DataTableCell dt={dt} col="cluster" row={r} value={cname} />');
    const col = src.slice(src.indexOf("{ id: 'cluster', label: 'Cluster'"), src.indexOf("{ id: 'kind', label: 'Tür'"));
    // title yalnız sayısal olmayan, sarmayan kolonda basılır; kolon kırpar.
    expect(col).not.toMatch(/numeric|truncate/);
  });
  it('workload eki artık cluster TEKRARLAMAZ (kırpılma kaynağı)', () => {
    expect(src).not.toContain('· {r.namespace} · {cname}');
  });
  // v0.10.1262 — operatör 2026-10-17: "workload ve namespace ayrı kolonlarda
  // yazılabilir". Namespace Workload'dan hemen sonra kendi kolonunda; workload
  // hücresi artık namespace eki taşımaz. v0.10.1267 (operatör 2026-10-17):
  // sortValue VAR ama yalnız SUNUCU sıralamasının işareti (serverSort +
  // sortIds; ORDER BY chstore rolloutOrderBy'da) — istemci sıralaması değil.
  it('namespace kendi kolonunda, workload ile cluster arasında', () => {
    const w = src.indexOf("{ id: 'workload', label: 'Workload'");
    const n = src.indexOf("{ id: 'namespace', label: 'Namespace'");
    const c = src.indexOf("{ id: 'cluster', label: 'Cluster'");
    expect(n).toBeGreaterThan(w);
    expect(c).toBeGreaterThan(n);
    expect(src).toContain('<DataTableCell dt={dt} col="namespace" row={r} value={r.namespace} />');
    expect(src).not.toContain('<span className="field-hint"> · {r.namespace}</span>');
    const col = src.slice(n, c);
    expect(col).toMatch(/sortValue/);
  });
  // v0.10.1267 — sıralanabilir kolonlar TAM OLARAK sunucunun sözlüğü
  // (started/workload/namespace/cluster); öteki kolonlarda sortValue yok ve
  // tablo serverSort (kesik sayfayı istemcide sıralamak yalan söyler — T3).
  it('yalnız sunucu-sıralanabilir kolonlar sortValue taşır', () => {
    const cols = src.slice(src.indexOf('const COLS: ColumnDef'), src.indexOf('const COLS_V2'));
    const sortable = [...cols.matchAll(/\{ id: '([a-z]+)'[^\n]*sortValue/g)].map(m => m[1]).sort();
    expect(sortable).toEqual(['cluster', 'namespace', 'started', 'workload']);
    expect(src).toMatch(/serverSort: true, initialSort: ROLLOUT_SORT_DEFAULT, persistSort: false, sortIds: ROLLOUT_SORT_IDS/);
  });
});
