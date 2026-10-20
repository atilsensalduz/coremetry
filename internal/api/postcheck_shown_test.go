// v0.9.598 — halüsinasyon kalkanı, GÖSTERDİĞİMİZ veriyi uydurma
// sanıyordu: modele verdiğimiz somut kimlikler (CHANNEL_CODE, request_id)
// bilinen kümeye girmiyor, model doğru alıntıladığında "doğrulanamadı"
// rozeti çıkıyordu. addShownTokens bu kümeyi jeton düzeyinde kurar.
//
// v0.10.1408 — analyze-service yüzeyi (postCheckServiceAnalysis + few-shot
// pinleri) kalktı; yardımcının sözleşmesi RCA kalkanı (rca_shields.go) için
// kalır.
package api

import "testing"

// TestAddShownTokensIsTokenLevel — yardımcının sözleşmesi.
func TestAddShownTokensIsTokenLevel(t *testing.T) {
	known := map[string]bool{}
	addShownTokens(known, "CHANNEL_CODE internet-banking / mobile-app")
	for _, want := range []string{"internet-banking", "mobile-app"} {
		if !known[want] {
			t.Errorf("%q jetonu eklenmedi — tarayıcı jeton düzeyinde eşleşiyor, "+
				"ham metni tek anahtar olarak eklemek yetmez", want)
		}
	}
	if len(known) == 0 {
		t.Fatal("hiçbir şey eklenmedi")
	}
}
