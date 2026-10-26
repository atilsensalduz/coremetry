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

// v0.10.1552 — operatör: "Kısa ve öze yerine modelin genel yetkinliğince tam
// cevabı alabilmeli kullanıcılar". Anlatım üreten sohbet istemleri kısalık
// istemez; eksiksiz cevap ister.
func TestChatPromptsAskForFullAnswers(t *testing.T) {
	for name, p := range map[string]string{
		"ChatAnswerStyle": ChatAnswerStyle, "GeneralChat": SystemPromptGeneralChat(),
		"OffTopicGeneral": SystemPromptOffTopicGeneral(), "WikiGeneral": SystemPromptWikiGeneral(),
	} {
		for _, bad := range []string{"Kısa ve öz", "kısa ve Türkçe yaz", "eksiksiz ama öz"} {
			if strings.Contains(p, bad) {
				t.Errorf("%s %q içermemeli", name, bad)
			}
		}
	}
	for name, p := range map[string]string{"ChatAnswerStyle": ChatAnswerStyle, "GeneralChat": SystemPromptGeneralChat(), "OffTopicGeneral": SystemPromptOffTopicGeneral()} {
		if !strings.Contains(p, "EKSİKSİZ") && !strings.Contains(p, "TAM cevap") {
			t.Errorf("%s eksiksiz cevap istemeli", name)
		}
	}
}
