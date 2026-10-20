import type { VMSnapshot, VMSelfMetricsMode } from '@/lib/types';

// vmForm (v0.9.1164) — MetricsBackendTab'in SAYI kutusu ile tel arasındaki
// çeviri. aiTuning.ts'in aynı deseni, aynı sebeple: SAF ve TABLO-TESTLİ,
// çünkü buradaki tek bir yanlış dal SESSİZ AYAR DONMASI demek.
//
// SÖZLEŞME: backend `rateWindowFloorS`'i EFEKTİF pencere olarak değil
// ÜSTÜNE YAZMA olarak döndürür. 0 — ve `omitempty` yüzünden JSON'da hiç
// GÖRÜNMEMESİ — "üstüne yazma yok, yerleşik 300s taban çalışıyor" demek.
//
// KUSUR SINIFI (bu modülün var olma sebebi, aiTuning'den devralınmış):
// formun boş kutuya yerleşik varsayılanı YAZMASI. Görsel olarak masum —
// kutuda "300" görünür ve doğru görünür. Ama PUT gövdesi TÜM blob'u
// değiştiriyor, yani operatör tamamen ilgisiz bir sebeple (token döndürmek,
// VM'i kapatmak) Kaydet'e bastığında o 300 blob'a YAZILIR. O andan sonra
// ayar "varsayılanı takip et" değil "300'de sabitlen"dir; Coremetry
// varsayılanı yarın değiştirse bu kurulum onu hiç görmez ve kimse nedenini
// bilmez — çünkü ekranda hiçbir şey değişmemişti.
//
// Bu yüzden form state'i SAYI değil DİZİ tutar: '' tek dürüst "unset"
// gösterimidir, 300 placeholder'da yaşar ve boş kutu tele 0 (= sıfırla)
// olarak gider.
//
// SINIR DOĞRULAMASI BİLEREK BURADA YOK. Aralık ([10, 3600]) tek yerde,
// vmetrics'te yaşıyor ve sunucu aralık dışını 400 + mesajla reddediyor.
// Buraya bir kopya koymak, formun kabul edip sorgunun görmezden geldiği
// (ya da tersi) bir değer sınıfı yaratmanın en kısa yoludur — kutuya
// `min`/`max` koymak da aynı şey, üstüne formun TAMAMINI native bir
// balonla kilitler.

/** Sunucudan gelen override → kutu metni. */
export function vmFloorToForm(v: number | null | undefined): string {
  // Üç "unset" yazılışı var ve ÜÇÜ de '' olmalı: alanın hiç gelmemesi
  // (omitempty), null, ve 0. Burada 0 GEÇERLİ BİR DEĞER DEĞİL — sıfır
  // saniyelik taban diye bir şey yok — o yüzden aiTuning'in temperature
  // ayrımına gerek kalmıyor: falsy kontrolü doğru kontroldür.
  if (v === null || v === undefined || v === 0) return '';
  return String(v);
}

/** Kutu metni → PUT gövdesi. */
export function vmFloorToWire(v: string): number {
  const n = Number(v.trim());
  // Boş VE ayrıştırılamaz girdi aynı yere gider: sıfırlama işareti.
  // Alternatifler daha kötüydü — NaN göndermek (JSON'da null olur,
  // Go 0'a düşürür, ama tel gövdesi artık tipini yalan söyler) ya da eski
  // değeri korumak (kutu boş görünürken ayar duruyor = kutu YALAN söylüyor).
  if (v.trim() === '' || !Number.isFinite(n)) return 0;
  // ARALIK DIŞI DEĞER OLDUĞU GİBİ GİDER, kırpılmaz. Operatör 5 yazdıysa
  // sunucunun "10 ile 3600 arasında olmalı" cevabını görmesi gerekir;
  // sessizce 10'a çekmek, sormadıkları bir pencereyi kaydetmek olurdu.
  return Math.round(n);
}

// ── v0.10.1422 — Coremetry'nin kendi metrikleri → VM: durum satırı ──
// SAF: snapshot → tek satır. Durum CEVABI VEREN POD'undur (her pod kendi
// metriklerini yazar); satır bunu söyler, "kurulum geneli" diye okunmasın.

/** Sunucu modu → select değeri (bilinmeyen/eksik = auto, sunucu kuralı). */
export function vmSelfModeToForm(v: string | undefined): VMSelfMetricsMode {
  return v === 'on' || v === 'off' ? v : 'auto';
}

function hhmmss(ms: number): string {
  const d = new Date(ms);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

export function vmSelfStatusLine(s: Pick<VMSnapshot, 'selfMetrics' | 'selfMetricsActive' | 'selfMetricsReason' | 'selfMetricsStatus'>): { kind: 'ok' | 'err' | 'muted'; text: string } {
  if (vmSelfModeToForm(s.selfMetrics) === 'off') {
    return { kind: 'muted', text: 'Kapalı — Coremetry’nin kendi metrikleri VictoriaMetrics’e yazılmıyor.' };
  }
  if (!s.selfMetricsActive) {
    return { kind: 'muted', text: `Etkin değil — ${s.selfMetricsReason || 'yazım hedefi yok'}.` };
  }
  const st = s.selfMetricsStatus;
  const counts = st ? ` · ${st.exported} gönderim, ${st.failed} hata (bu pod)` : '';
  if (st?.lastError && (st.lastErrorAt ?? 0) > (st.lastOkAt ?? 0)) {
    return { kind: 'err', text: `Son gönderim başarısız: ${st.lastError}${counts}` };
  }
  if (st?.lastOkAt) {
    return { kind: 'ok', text: `Yazılıyor — son başarılı gönderim ${hhmmss(st.lastOkAt)}${counts}` };
  }
  return { kind: 'muted', text: 'Etkin — ilk gönderim 30 sn içinde (bu pod).' };
}
