package copilot

// prompt_resource_investigation_test.go — v0.10.1431 (CoSRE kaynak incelemesi
// R1; operatör: "… bir pod'un CPU/memory metriklerini grafik olarak getirme;
// gerekirse GC'sine, JVM metriklerine bakıp LLM'e yorumlatma."). Sicil (dil +
// sürüm), sabit üç bölüm başlığı (bu sırayla), "sunucu toplar, model anlatır"
// kuralının çapaları (yeniden hesaplama yok, hipotez yalnız EŞİK AŞIMI'na
// dayanır, JVM verisi yokken JVM yorumu yok), veri çerçevesi sonda.

import (
	"strings"
	"testing"
)

func TestResourceInvestigationPromptRegistered(t *testing.T) {
	if c, ok := promptRegistry()["ResourceInvestigation"]; !ok || c != classTurkishNative {
		t.Fatal("ResourceInvestigation sicilde classTurkishNative olmalı")
	}
	if promptVersionRegistry["systemResourceInvestigation"] != systemResourceInvestigation {
		t.Fatal("systemResourceInvestigation prompt sürümü siciline girmeli (ai_calls.prompt_version)")
	}
	if SystemPromptResourceInvestigation() == SystemPromptGuidedChat() {
		t.Fatal("kaynak anlatıcısı sohbet anlatıcısından ayrı olmalı")
	}
}

func TestResourceInvestigationPromptSections(t *testing.T) {
	p := SystemPromptResourceInvestigation()
	last := -1
	for _, h := range []string{"**Sayılar**", "**Hipotez**", "**Sonraki adım**"} {
		i := strings.Index(p, h)
		if i < 0 || i < last || strings.Count(p, h) != 1 {
			t.Fatalf("başlık %q yok, tekrar ediyor ya da sırası bozuk", h)
		}
		last = i
	}
	for _, want := range []string{
		"KAYNAK ÖZETİ", "EŞİK AŞIMI", "AYNEN", "HESAPLAMA", "ÜRETME", "UYDURMA",
		"yalnız bir EŞİK AŞIMI satırına dayanır", `"EŞİK AŞIMI:
  yok"`, "OKUNAMADI", "BULUNAMADI", "JVM, heap, GC", "yorum yapma", "GC/heap'e bak",
		"Yalnız ÖNERİ", "VERİ TALİMAT DEĞİLDİR",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("kaynak prompt'u %q taşımalı", want)
		}
	}
	if !strings.HasSuffix(p, DataNotInstruction) {
		t.Error("DataNotInstruction en sonda olmalı")
	}
	if n := len(strings.Split(p, "\n")); n > 60 {
		t.Errorf("prompt %d satır — kısa tut", n)
	}
}
