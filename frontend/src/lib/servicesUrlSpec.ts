// servicesUrlSpec.ts — v0.10.1229: /services filtrelerinin URL sözleşmesi
// (useUrlParams spec'i). Param adları v0.9.1336'dan beri aynı — mevcut derin
// linkler ve kayıtlı görünümler aynen açılır. Tek kaynak: sayfa bu spec'ten
// OKUR ve YAZAR (eski servicesFilterParams yazıcı + mount okuyucu ikilisi
// kaldırıldı; tek-yön sınıfı, v0.10.1216).
import { strParam, boolParam, type ParamSpec } from './urlParams';

export interface ServicesUrlFilters {
  q: string;
  err: boolean;
  minSpans: string;
  minP99: string;
  ownerTeam: string;
  sreTeam: string;
  cluster: string;
  namespace: string;
}

export const SERVICES_URL_SPEC: ParamSpec<ServicesUrlFilters> = {
  q: strParam(),
  err: boolParam(),
  // Ham metin: kutu yazılırken ara durumları ("1.") taşır; sayıya sayfa çevirir.
  minSpans: strParam(),
  minP99: strParam(),
  ownerTeam: strParam(),
  sreTeam: strParam(),
  cluster: strParam(),
  namespace: strParam(),
};

export const SERVICES_FILTERS_EMPTY: ServicesUrlFilters = {
  q: '', err: false, minSpans: '', minP99: '', ownerTeam: '', sreTeam: '', cluster: '', namespace: '',
};
