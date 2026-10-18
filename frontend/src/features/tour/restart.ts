// restart.ts — v0.10.1334 (rehberli tur). "Turu yeniden başlat" girişleri
// (Sidebar kullanıcı menüsü, ShortcutsHelp alt bağlantısı) ile TourProvider
// arasındaki tek kanal. TourProvider AppShell'de Sidebar'ın KARDEŞİ olarak
// mount edildiği için bağlam (context) sarmalayıcısı yerine modül-düzeyi
// abone kümesi: girişler sağlayıcıyı import etmez, örtü yükü taşımaz.

type Listener = () => void;
const listeners = new Set<Listener>();

/** Turu baştan başlat (oto-başlatma kapılarından BAĞIMSIZ — kullanıcı istedi). */
export function requestTourRestart(): void {
  for (const l of [...listeners]) l();
}

export function onTourRestart(l: Listener): () => void {
  listeners.add(l);
  return () => { listeners.delete(l); };
}
