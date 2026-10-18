package copilot

import (
	"strings"
	"testing"
)

// TestTraceWithOperationCodePrompt — v0.10.1330: stack yokken ana operasyonun
// kodu. Taban trace prompt'unun ÜSTÜNE ek (yeniden yazım yok), kendi eki
// sicilde, kurallar: alıntı + yol:aralık, span/Oracle/hata kodu eşlemesi,
// "operasyon kodunda görünmüyor", satır suçlaması yok, "+N okunmadı".
func TestTraceWithOperationCodePrompt(t *testing.T) {
	p := SystemPromptTraceWithOperationCode()
	if !strings.HasPrefix(p, systemTraceBody) {
		t.Fatal("operasyon kodu prompt'u trace tabanından başlamıyor")
	}
	if !strings.HasSuffix(p, AnswerInTurkish) || strings.Count(p, AnswerInTurkish) != 1 {
		t.Fatal("dil direktifi tek kez ve sonda olmalı")
	}
	if strings.Contains(p, systemCodeAddendum) {
		t.Error("stack kod eki (>>> / en derin frame) operasyon prompt'una girmemeli")
	}
	if promptVersionRegistry["systemOperationCodeAddendum"] != systemOperationCodeAddendum {
		t.Error("ek promptVersionRegistry'de değil")
	}
	for _, want := range []string{
		"KOD ALINTISI ZORUNLU", "```", "// <yol>:<başlangıç>-<bitiş>", "Numaraları UYDURMA",
		"Oracle", "errorCode", "operasyon kodunda görünmüyor", "SUÇLAMA",
		"+N diğer operasyon okunmadı", "kaynak çözülemedi", "NOT — kod bağlamı EKSİK",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt %q kuralını taşımıyor", want)
		}
	}
	if strings.Contains(systemOperationCodeAddendum, CodeFrameMarker) {
		t.Error("operasyon eki >>> işaretini anmamalı — pencerede hata satırı yok")
	}
}
