// v0.10.1502 — Columns ▾ seçicisinin saf dönüşümleri.
import { describe, it, expect } from 'vitest';
import { moveLogColumn, removeLogColumn, addLogColumn, addableLogColumns } from './logColumns';
import { DEFAULT_LOG_COLUMNS, BUILTIN_LOG_COLUMNS } from '@/components/LogTable';

describe('logColumns', () => {
  const d = DEFAULT_LOG_COLUMNS; // cluster, message, service, pod
  it('taşı: komşuyla yer değiştirir, uçta aynen', () => {
    expect(moveLogColumn(d, 'service', -1)).toEqual(['cluster', 'service', 'message', 'pod']);
    expect(moveLogColumn(d, 'cluster', -1)).toEqual(d);
    expect(moveLogColumn(d, 'pod', 1)).toEqual(d);
    expect(moveLogColumn(d, 'nope', 1)).toEqual(d);
  });
  it('kaldır: Message kaldırılamaz', () => {
    expect(removeLogColumn(d, 'pod')).toEqual(['cluster', 'message', 'service']);
    expect(removeLogColumn(d, 'message')).toEqual(d);
  });
  it('ekle: sona, tekrar yok, çerçeve id yok', () => {
    expect(addLogColumn(d, 'k8s.namespace')).toEqual([...d, 'k8s.namespace']);
    expect(addLogColumn(d, 'pod')).toEqual(d);
    expect(addLogColumn(d, 'time')).toEqual(d);
    expect(addLogColumn(d, ' ')).toEqual(d);
  });
  it('message yoksa (eski tercih) sona eklenmiş anatomi üzerinden çalışır', () => {
    expect(moveLogColumn(['pod'], 'message', -1)).toEqual(['message', 'pod']);
  });
  it('adaylar: yerleşik + yüklenmiş alanlar, mevcutlar ve tekrarlar hariç, süzgeç + tavan', () => {
    const f = ['k8s.namespace', 'http.status_code', 'pod'];
    expect(addableLogColumns(d, BUILTIN_LOG_COLUMNS, f, '')).toEqual(['level', 'k8s.namespace', 'http.status_code']);
    expect(addableLogColumns(d, BUILTIN_LOG_COLUMNS, f, 'HTTP')).toEqual(['http.status_code']);
    expect(addableLogColumns(d, BUILTIN_LOG_COLUMNS, f, '', 1)).toEqual(['level']);
  });
});
