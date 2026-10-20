import { api } from '@/lib/api';
import { Combobox } from '@/components/Combobox';
import { shouldAutoCommit } from '@/components/ServicePicker';
import { usePickerSearch } from '@/components/usePickerSearch';
import { getPickerRecents } from '@/lib/pickerRecents';

/** v0.10.1402 — "Son kullanılan" kapsamı: CoSRE takım adları. */
export const TEAM_RECENT_KEY = 'team:cosre';

/**
 * TeamPicker — v0.10.1402 (operatör: "Takım seçilebilsin."): CoSRE
 * başlangıcındaki Takımım kartının takım seçicisi. Service/OperationPicker'ın
 * kardeşi: aynı debounce'lu sunucu araması (usePickerSearch), aynı seçim
 * sezgiseli (shouldAutoCommit), aynı Combobox açılır listesi. Katalog
 * ÖNDEN ÇEKİLMEZ — kaynak /api/copilot/scope-names?kind=team (composer'ın
 * `@team:` tamamlamasıyla aynı uç, ≤20 aday; sohbetin kapsamı doğruladığı
 * AYNI takım listesi, yani önerilen her ad sohbette takım olarak çözülür).
 * Uç requireCopilot arkasında — seçici yalnız CoSRE'de kullanılır.
 */
export function TeamPicker({ value, onChange, onEnter, placeholder, width, title }: {
  value: string;
  onChange: (v: string) => void;
  /** Listeden seçim: seçilen ad; Enter: undefined (çağıran kendi değerini okur). */
  onEnter?: (value?: string) => void;
  placeholder?: string;
  width?: number | string;
  title?: string;
}) {
  const search = usePickerSearch(value, 'team', q =>
    api.copilotScopeNames('team', q.trim()).then(r => ({ items: r.names, total: r.names.length })));
  // Önceki değer = o anki prop: varsayılan takım dışarıdan (sunucu / depolama)
  // gelebildiği için ayrı bir ref bayatlardı.
  const handleChange = (next: string) => {
    const prev = value;
    onChange(next);
    const isPick = search.itemsRef.current.includes(next) || getPickerRecents(TEAM_RECENT_KEY).includes(next);
    if (shouldAutoCommit(prev, next, isPick) && onEnter) {
      setTimeout(() => onEnter(next), 0);
    }
  };

  return (
    <Combobox
      value={value}
      onChange={handleChange}
      options={search.items}
      serverFiltered
      resultCount={search.total}
      status={search.status}
      recentKey={TEAM_RECENT_KEY}
      placeholder={placeholder}
      width={width}
      title={title}
      ariaLabel={placeholder}
      onEnter={() => onEnter?.(undefined)}
    />
  );
}
