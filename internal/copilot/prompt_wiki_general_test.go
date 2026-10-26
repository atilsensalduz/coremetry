package copilot

import (
	"strings"
	"testing"
)

// v0.10.1550 — wiki "bulunamadı" sonrası genel cevap: ayrıntılı teknik istem
// (örnek kod/config, adımlar, doğrulama), etiketi model YAZMAZ, Türkçe.
func TestSystemPromptWikiGeneral(t *testing.T) {
	p := SystemPromptWikiGeneral()
	for _, want := range []string{"UYGULANABİLİR", "dil etiketli kod", "DOĞRULANACAĞINI", "UYDURMA", "Cevabın genel bilgi olduğunu YAZMA", "CEVAP BİÇİMİ", answerInTurkishLine} {
		if !strings.Contains(p, want) {
			t.Errorf("istem %q içermeli", want)
		}
	}
	if strings.Contains(p, "Kısa ve öz") {
		t.Error("wiki genel istemi kısalık istememeli (operatör: detaylı anlatım)")
	}
}
