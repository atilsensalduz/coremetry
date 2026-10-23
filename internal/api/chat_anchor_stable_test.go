package api

import (
	"testing"
	"time"
)

// v0.10.1469 — iki kollu bayt-kimliği testlerinin çıpası saniye sınırını
// geçince DEĞİŞMEZ. Eski anchoredToMs (her çağrıda time.Now()) burada
// kırmızıydı; TestReadSourceCodeOfferedOnlyWithDevOpsAndPromptByteIdentical
// tam paket -race koşusunda tam bu yüzden ara sıra düşüyordu (kollar arası
// saniye sınırı → AKTİF BAĞLAM pencereleri 1 s kayık).
func TestAnchoredToMsStableAcrossSecondBoundary(t *testing.T) {
	a := anchoredToMs()
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second + 5*time.Millisecond)))
	if b := anchoredToMs(); b != a {
		t.Fatalf("çıpa kollar arasında kaydı: %d → %d", a, b)
	}
	if d := time.Since(time.UnixMilli(a)); d < time.Hour || d > chatAnchorMaxAge {
		t.Fatalf("çıpa kabul aralığı dışında: %v önce", d)
	}
	// Bayt-kimliği kolunu saniye sınırının iki yanında koşan sürüm.
	body1 := traceDrawerBody("Özetle?", anchoredToMs(), "conv-x")
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second + 5*time.Millisecond)))
	body2 := traceDrawerBody("Özetle?", anchoredToMs(), "conv-x")
	if body1["context"].(map[string]any)["toMs"] != body2["context"].(map[string]any)["toMs"] {
		t.Fatal("trace çekmecesi gövdesi kollar arasında farklı")
	}
}
