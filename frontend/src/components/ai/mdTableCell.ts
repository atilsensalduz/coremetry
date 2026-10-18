// mdTableCell — v0.10.1346 (operatör-bildirimli, /cosre ekran görüntüsü):
// sohbet markdown tablosunun HÜCRE kararları, SAF (mdTableCell.test.ts piner).
//
// Kusur: "Kolon adı kısa ama satırlardaki değerler uzun olduğunda tablo kötü
// gözüküyor" — yüklenen belge satırları tablosunda (Satır · Ad · Banka
// Fonksiyon ID · … · Statü) "LOGIN_14300.aspx" L/O/GI/N_/14/30/0./as/px,
// "1526" 152/6, "AKTİF" AKTİ/F diye harf harf kırılıyordu. Kök neden CSS'te
// (`.cm-md-table tbody td { overflow-wrap: anywhere }`): `anywhere` hücrenin
// min-content genişliğini TEK HARFE indirir, otomatik tablo düzeni de kısa
// başlıklı kolonu o genişliğe kadar sıkıştırır. Sarma artık `break-word`
// (min-content'i etkilemez); buradaki üç karar onu tamamlar:
//   - cellNoWrap: kısa değer / tek kelimelik kimlik ASLA bölünmez;
//   - wbrSegments: uzun kimlik yalnız `_ . / -` SONRASINDA kırılabilir
//     (harf ortasında değil; <wbr> kopyalanan metne karışmaz, ZWSP karışırdı);
//   - numericColumn: tamamen sayısal kolon sağa yaslı + tabular-nums (`.num`).
import { inlinePlain, parseInline } from './chatInline';

/** Bu uzunluğa kadar (boşluklu da olsa) hücre tek satır kalır. */
export const CELL_NOWRAP_MAX = 24;
/** Boşluksuz tek kelime bu uzunluğa kadar tek satır kalır (kimlik, dosya adı). */
export const TOKEN_NOWRAP_MAX = 40;
/** Bu uzunluktan kısa kelimeye kırılma noktası EKLENMEZ ("AKTİF", "1526"). */
export const WBR_MIN = 12;

const len = (s: string) => Array.from(s).length;

/** Hücrenin görünen düz metni (işaretler düşer, `\|` kaçışı çözülür). */
export function cellText(raw: string): string {
  return inlinePlain(parseInline(raw)).trim();
}

/** Satır içi işaretleme YOKSA hücrenin metni, varsa null (MdInline çizer). */
export function plainCell(raw: string): string | null {
  const nodes = parseInline(raw);
  if (!nodes.every(n => n.t === 'text')) return null;
  return nodes.map(n => (n.t === 'text' ? n.v : '')).join('');
}

/** Kısa değer ya da tek kelimelik kimlik → hücre `white-space: nowrap`. */
export function cellNoWrap(text: string): boolean {
  const t = text.trim();
  if (t === '') return false;
  if (len(t) <= CELL_NOWRAP_MAX) return true;
  return !/\s/.test(t) && len(t) <= TOKEN_NOWRAP_MAX;
}

const NUMERIC = /^[-+]?\d[\d.,]*\s?%?$/;
const BREAK_AFTER = /[_./-]/;

/** Kolonun dolu hücrelerinin TAMAMI sayı mı (en az bir dolu hücre şartıyla). */
export function numericColumn(rows: readonly (readonly string[])[], col: number): boolean {
  let seen = false;
  for (const r of rows) {
    const v = cellText(r[col] ?? '');
    if (v === '') continue;
    if (!NUMERIC.test(v)) return false;
    seen = true;
  }
  return seen;
}

/**
 * Metni <wbr> ile birleştirilecek parçalara böler: yalnız ≥ WBR_MIN uzunluktaki
 * sayı olmayan kelimelerde, `_ . / -` karakterinin ARDINDAN (kelimenin ilk
 * ya da son karakteri değilse). Kısa kelime, sayı ve boşluk olduğu gibi kalır;
 * parçaların birleşimi her zaman girdinin kendisidir.
 */
export function wbrSegments(text: string): string[] {
  const out: string[] = [];
  let cur = '';
  for (const part of text.split(/(\s+)/)) {
    const chars = Array.from(part);
    if (chars.length < WBR_MIN || /\s/.test(part) || NUMERIC.test(part)) {
      cur += part;
      continue;
    }
    chars.forEach((ch, i) => {
      cur += ch;
      if (BREAK_AFTER.test(ch) && i > 0 && i < chars.length - 1 && !BREAK_AFTER.test(chars[i + 1])) {
        out.push(cur);
        cur = '';
      }
    });
  }
  if (cur !== '' || out.length === 0) out.push(cur);
  return out;
}
