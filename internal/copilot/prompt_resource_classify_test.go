package copilot

// prompt_resource_classify_test.go — v0.10.1442 (CoSRE kaynak incelemesi R3):
// dar kaynak sınıflandırıcısının istemi kısa (≤40 satır), Türkçe-native,
// enjeksiyon kalkanlı, dört niyeti anlatıyor ve PromQL yazdırmıyor.

import (
	"strings"
	"testing"
)

func TestResourceClassifyPrompt(t *testing.T) {
	p := SystemPromptResourceClassify()
	if n := strings.Count(p, "\n") + 1; n > 40 {
		t.Fatalf("istem %d satır, ≤40 olmalı", n)
	}
	if !strings.HasPrefix(p, "Sen Coremetry") || !strings.Contains(p, IntentNoInstructionLine) {
		t.Fatal("Türkçe çıpa ya da enjeksiyon kalkanı eksik")
	}
	if strings.Contains(p, DataNotInstruction) {
		t.Fatal("sınıflandırıcı DataNotInstruction taşımamalı (anlamı ters)")
	}
	for _, s := range []string{"- resource_investigation:", "- metric_chart:", "- pod_health:", "- none:",
		"confidence", "PromQL, metrik adı ya da sorgu YAZMA", "YALNIZ JSON"} {
		if !strings.Contains(p, s) {
			t.Errorf("istemde %q yok", s)
		}
	}
	if !strings.Contains(SystemPromptIntentClassify(), "- resource_investigation:") {
		t.Error("genel sınıflandırıcı resource_investigation'ı anlatmıyor")
	}
}
