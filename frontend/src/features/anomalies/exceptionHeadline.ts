// exceptionHeadline — v0.10.1398 (operatör: "Çok uzun bir exception geldiğinde
// tüm sayfayı kaplıyor. Halbuki zaten aşağıda aynısı yazıyor").
//
// Exception detayının kırmızı başlığının altındaki mesaj satırı, mesajın
// TAMAMINI basıyordu. Bazı çerçeveler (Feign hata çözücüsü gibi) mesajın
// içine yığın çerçevelerini de gömdüğü için 500 satırlık bir yığın sayfanın
// üstünü kaplıyor, aynı metin aşağıdaki "Stack trace" kartında zaten
// duruyordu. Başlık artık mesajın yalnız ilk kısmını gösterir:
//   • ilk gömülü Java/.NET yığın çerçevesinden (" at a.b.C.m(") önce kesilir;
//   • sonra HEADLINE_MAX karakterde kesilir (kelime sınırında, "…" ile).
// Tam metin `title` + "Tamamı" ile açılabilir. SAF — exceptionHeadline.test.ts.

export const HEADLINE_MAX = 320;

// "at pkg.Class.method(" — JVM ("at deployment.X.war//feign.F.m(") ve .NET
// ("at Ns.Type.Method(") çerçeveleri. Satır başı ya da boşluktan sonra.
const FRAME_RE = /(?:^|\s)at\s+[\w$./<>-]+\(/;

export interface ExceptionHeadline {
  text: string;
  cut: boolean;
}

export function exceptionHeadline(msg: string | undefined | null, max = HEADLINE_MAX): ExceptionHeadline {
  const full = (msg ?? '').trim();
  if (!full) return { text: '', cut: false };
  let text = full;
  const m = FRAME_RE.exec(text);
  if (m && m.index > 0) text = text.slice(0, m.index).trimEnd();
  if (text.length > max) {
    const slice = text.slice(0, max);
    const sp = slice.lastIndexOf(' ');
    text = (sp > max * 0.6 ? slice.slice(0, sp) : slice).trimEnd();
  }
  const cut = text.length < full.length;
  return { text: cut ? `${text} …` : text, cut };
}
