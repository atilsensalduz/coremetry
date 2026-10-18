// v0.10.1332 (operatör onaylı mockup "Filtre barı · Search son filtrenin
// arkasında") — filtre barı Search'ünün yanındaki "Enter ↵" kısayol ipucu.
//
// Neden ayrı bir atom: aynı ipucu üç barda (Services / Traces / Logs) ve
// /traces'in trace-id kutusunda duruyor; dört satır-içi `<kbd>` yazımı
// `.fq-hint kbd` / `.cm-kbd` / `.tb-search-kbd` gibi bir dördüncü yerel
// spelling doğururdu. Görünüm `.kbd` (globals.css, token'larla).
//
// ERİŞİLEBİLİRLİK: ok glifi (↵) `aria-hidden` — ekran okuyucu "Enter"
// okur, "aşağı-sol ok" değil. `title` ipucunun NEREDE geçerli olduğunu
// söyler: Enter her alanda değil, arama kutusunda çalışır (dürüst ipucu).
export interface EnterHintProps {
  /** Enter'ın nerede çalıştığı — ör. "Arama kutusunda Enter". */
  title: string;
}

export function EnterHint({ title }: EnterHintProps) {
  return (
    <kbd className="kbd" title={title}>
      Enter <span aria-hidden="true">↵</span>
    </kbd>
  );
}
