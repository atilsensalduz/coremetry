package chstore

// bubbleup_rank.go — v0.10.1495 (operatör, prod Problem detayı): "Where errors
// concentrate" bir JVM GC problemi için `messaging.message.body.size` gösterdi
// (değerler 749, 725, 731…). Bir mesajın boyutu bir ÖLÇÜDÜR, kategori değil:
// her değer seçimin küçük bir dilimi, "şu değerde yoğunlaşıyor" iddiası
// anlamsız. Sıralamaya yalnız düşük kardinaliteli kategorik attribute'lar
// girer; anlamsız bir attribute göstermektense hiç gösterme.
//
// İki süzgeç, ikisi de SAF (bubbleup_rank_test.go tablo):
//   - isUnrankableAttributeKey — ADA bakar: kimlik (id/uuid/trace/span/
//     request/correlation), ölçü (size/length/count/bytes/rows/offset/…),
//     zaman damgası (timestamp/time/…) son ekleri. Sorgu koşmadan eler (ham
//     spans taramasından tasarruf).
//   - isScatteredNumericAttribute — DEĞERLERE bakar: dönen değerlerin hepsi
//     sayıysa ve en çok 6 değer seçimin yarısını bile kapsamıyorsa (dağınık
//     sayısal dağılım) attribute elenir. Kategorik sayılar (http durum kodu
//     500 → seçimin %90'ı, kafka bölümü 3 değer) kapsamı yüksek olduğu için
//     kalır.

import (
	"strconv"
	"strings"
)

// unrankableKeySuffixes — son ad parçası ('.' ya da '_' ile ayrılan) bu
// kümedeyse attribute bir kimlik, ölçü ya da zaman damgasıdır.
var unrankableKeySuffixes = map[string]bool{
	// kimlik
	"id": true, "uuid": true, "guid": true, "key": true, "token": true, "nonce": true,
	"hash": true, "checksum": true, "seq": true, "sequence": true,
	// ölçü
	"size": true, "length": true, "len": true, "count": true, "bytes": true,
	"rows": true, "offset": true, "duration": true, "ms": true, "ns": true,
	// zaman damgası
	"timestamp": true, "time": true, "ts": true, "epoch": true,
}

// isUnrankableAttributeKey — ad tabanlı süzgeç (SAF).
func isUnrankableAttributeKey(k string) bool {
	lk := strings.ToLower(strings.TrimSpace(k))
	if lk == "" {
		return true
	}
	// v0.9.x kökenli kurallar (aynen): kimlik taşıyan anahtarlar.
	if strings.Contains(lk, "trace_id") || strings.Contains(lk, "span_id") ||
		strings.Contains(lk, "request_id") || strings.Contains(lk, "correlation") {
		return true
	}
	if strings.Contains(lk, "offset") || strings.Contains(lk, "timestamp") {
		return true
	}
	last := lk
	if i := strings.LastIndexAny(lk, "._"); i >= 0 {
		last = lk[i+1:]
	}
	return unrankableKeySuffixes[last]
}

// scatteredCoverageMax — sayısal attribute'ta dönen değerlerin seçim kapsamı
// bunun altındaysa dağılım dağınıktır (yoğunlaşma yok).
const scatteredCoverageMax = 0.5

// isScatteredNumericAttribute — değer tabanlı süzgeç (SAF). Boş değer listesi
// burada elenmez (çağıran zaten atlar).
func isScatteredNumericAttribute(a BubbleUpAttribute) bool {
	if len(a.Values) == 0 {
		return false
	}
	cover := 0.0
	for _, v := range a.Values {
		if _, err := strconv.ParseFloat(strings.TrimSpace(v.Value), 64); err != nil {
			return false
		}
		cover += v.SelectionPct
	}
	return cover < scatteredCoverageMax
}
