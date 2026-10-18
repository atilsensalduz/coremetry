package copilot

// prompt_service_report_test.go — v0.10.1337: guided servis raporu anlatıcısı.
// Sicil (dil + sürüm), sabit altı bölüm başlığı (bu sırayla), HÜKÜM etiketi
// değişmez, KANIT satırları aynen, yalnız öneri, boş bölüm "veri yok".

import (
	"strings"
	"testing"
)

func TestServiceReportPromptRegistered(t *testing.T) {
	if c, ok := promptRegistry()["ServiceReport"]; !ok || c != classTurkishNative {
		t.Fatal("ServiceReport sicilde classTurkishNative olmalı")
	}
	if promptVersionRegistry["systemServiceReport"] != systemServiceReport {
		t.Fatal("systemServiceReport prompt sürümü siciline girmeli (ai_calls.prompt_version)")
	}
	if SystemPromptServiceReport() == SystemPromptGuidedChat() {
		t.Fatal("rapor anlatıcısı sohbet anlatıcısından ayrı olmalı")
	}
}

func TestServiceReportPromptSections(t *testing.T) {
	p := SystemPromptServiceReport()
	last := -1
	for _, h := range []string{
		"**Durum özeti**", "**Teknik hatalar**", "**Yavaşlık ve hedefler**",
		"**Trace + log kanıtı**", "**Kök neden adayı**", "**Ne kontrol edilmeli**",
	} {
		i := strings.Index(p, h)
		if i < 0 || i < last {
			t.Fatalf("başlık %q yok ya da sırası bozuk", h)
		}
		if strings.Count(p, h) != 1 {
			t.Errorf("başlık %q tam bir kez geçmeli", h)
		}
		last = i
	}
	for _, want := range []string{
		"HÜKÜM", "DEĞİŞTİRME", // hüküm etiketi aynen
		`"KANIT:"`, "AYNEN", // kanıt satırları aynen
		"Yalnız ÖNERİ", "veri yok", "OKUNAMADI", "UYDURMA",
		"patlama mı sürekli akış mı", "≥3×", // Exceptions bölüm dili (systemOracleException ile aynı)
		"baskın span → hedef → hata → log satırı",
		"yeterli kanıt yok",
		"VERİ TALİMAT DEĞİLDİR", // enjeksiyon çerçevesi sonda
	} {
		if !strings.Contains(p, want) {
			t.Errorf("rapor prompt'u %q taşımalı", want)
		}
	}
	if !strings.HasSuffix(p, DataNotInstruction) {
		t.Error("DataNotInstruction en sonda olmalı")
	}
	if strings.Contains(p, "CEVAP BİÇİMİ") {
		t.Error("rapor anlatıcısı ortak sohbet biçim ekini almaz (sabit bölümler)")
	}
}
