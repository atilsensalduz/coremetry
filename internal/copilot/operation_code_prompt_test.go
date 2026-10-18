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

// TestTraceWithOperationCandidatePrompt — v0.10.1349 (operatör-bildirimli):
// hatasız trace'te operasyon fonksiyon kodu sözlüğünden ADAY. Operasyon kodu
// prompt'unun AYNISI + tek maddelik aday eki (sicilde); dil direktifi sonda.
func TestTraceWithOperationCandidatePrompt(t *testing.T) {
	p := SystemPromptTraceWithOperationCandidate()
	if p != systemTraceBody+systemOperationCodeAddendum+systemOperationCandidateAddendum+AnswerInTurkish {
		t.Fatal("aday prompt'u = trace tabanı + operasyon eki + aday eki + dil direktifi olmalı")
	}
	if strings.Count(p, AnswerInTurkish) != 1 || !strings.HasSuffix(p, AnswerInTurkish) {
		t.Fatal("dil direktifi tek kez ve sonda olmalı")
	}
	if promptVersionRegistry["systemOperationCandidateAddendum"] != systemOperationCandidateAddendum {
		t.Error("aday eki promptVersionRegistry'de değil")
	}
	if strings.Count(systemOperationCandidateAddendum, "\n- ") != 1 {
		t.Errorf("aday eki tek madde olmalı:\n%s", systemOperationCandidateAddendum)
	}
	for _, want := range []string{"ADAY", "bir kez söyle", "iddia etme"} {
		if !strings.Contains(systemOperationCandidateAddendum, want) {
			t.Errorf("aday eki %q kuralını taşımıyor", want)
		}
	}
	if SystemPromptTraceWithOperationCode() == p {
		t.Error("kesin operasyon prompt'u değişmemeli (aday eki yalnız aday yolunda)")
	}
}
