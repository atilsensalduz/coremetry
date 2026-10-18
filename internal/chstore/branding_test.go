package chstore

// branding_test.go — v0.10.1108 (operatör: "Oracleden gelen problemlerde
// exceptionsta Oracle yazıyor onun yerine başka bir şey yazsa. Teknik Hata
// gibi mesela"). Pinler: oracleGroupLabel blob'da gidiş-dönüş korunur;
// yazışta ve okuyuşta kırpılır, 40 rune'a kesilir; boş = "Teknik hata";
// Get/Put süreç-geneli etiketi yayınlar (öncelik gerekçesi oradan okur).

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBrandingOracleGroupLabelRoundTrip(t *testing.T) {
	long := strings.Repeat("ş", OracleGroupLabelMaxRunes+5)
	cases := []struct{ name, in, want string }{
		{"boş → boş (varsayılan Resolved/FE'de)", "", ""},
		{"yalnız boşluk → boş", "   ", ""},
		{"kırpılır", "  Teknik hata  ", "Teknik hata"},
		{"özel etiket korunur", "Veritabanı hatası", "Veritabanı hatası"},
		{"40 rune tavanı (çok baytlı)", long, strings.Repeat("ş", OracleGroupLabelMaxRunes)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := encodeBranding(BrandingSettings{AppName: "X", OracleGroupLabel: c.in})
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeBranding(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got.OracleGroupLabel != c.want || got.AppName != "X" {
				t.Fatalf("gidiş-dönüş: %+v, beklenen etiket %q", got, c.want)
			}
			if utf8.RuneCountInString(got.OracleGroupLabel) > OracleGroupLabelMaxRunes {
				t.Fatalf("tavan aşıldı: %d", utf8.RuneCountInString(got.OracleGroupLabel))
			}
		})
	}
	// Elle yazılmış (normalize edilmemiş) eski blob da okuyuşta normalize olur.
	got, err := decodeBranding([]byte(`{"oracleGroupLabel":"  ` + long + `  "}`))
	if err != nil || got.OracleGroupLabel != strings.Repeat("ş", OracleGroupLabelMaxRunes) {
		t.Fatalf("okuyuş normalizasyonu: %q %v", got.OracleGroupLabel, err)
	}
	// Boş blob = sıfır değer; boş etiket JSON'a hiç yazılmaz (omitempty).
	if b, err := decodeBranding(nil); err != nil || b != (BrandingSettings{}) {
		t.Fatalf("boş blob: %+v %v", b, err)
	}
	if raw, _ := encodeBranding(BrandingSettings{OracleGroupLabel: "  "}); strings.Contains(string(raw), "oracleGroupLabel") {
		t.Fatalf("boş etiket blob'a yazılmamalı: %s", raw)
	}
}

func TestBrandingResolvedOracleGroupLabel(t *testing.T) {
	if got := (BrandingSettings{}).ResolvedOracleGroupLabel(); got != "Teknik hata" {
		t.Fatalf("varsayılan: %q", got)
	}
	if got := (BrandingSettings{OracleGroupLabel: " DB hatası "}).ResolvedOracleGroupLabel(); got != "DB hatası" {
		t.Fatalf("özel: %q", got)
	}
	t.Cleanup(func() { oracleGroupLabel.Store(nil) })
	oracleGroupLabel.Store(nil)
	if CurrentOracleGroupLabel() != DefaultOracleGroupLabel {
		t.Fatalf("hiç okunmamış süreç varsayılanı görmeli: %q", CurrentOracleGroupLabel())
	}
	publishOracleGroupLabel(BrandingSettings{OracleGroupLabel: "DB hatası"})
	if CurrentOracleGroupLabel() != "DB hatası" {
		t.Fatalf("yayın: %q", CurrentOracleGroupLabel())
	}
	publishOracleGroupLabel(BrandingSettings{})
	if CurrentOracleGroupLabel() != DefaultOracleGroupLabel {
		t.Fatalf("boşa dönüş varsayılana: %q", CurrentOracleGroupLabel())
	}
}

// v0.10.1334 — rehberli tur anahtarı: ""/"on" açık, yalnız "off" kapalı;
// kırpılır + küçük harfe iner, bilinmeyen değer ""ye (açık) düşer; boş
// değer blob'a yazılmaz (omitempty).
func TestBrandingOnboardingTourNormalize(t *testing.T) {
	cases := []struct {
		in, want string
		enabled  bool
	}{
		{"", "", true},
		{"on", "on", true},
		{" ON ", "on", true},
		{"off", "off", false},
		{"  Off", "off", false},
		{"maybe", "", true},
	}
	for _, c := range cases {
		raw, err := encodeBranding(BrandingSettings{OnboardingTour: c.in})
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeBranding(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got.OnboardingTour != c.want {
			t.Errorf("%q → %q, istenen %q", c.in, got.OnboardingTour, c.want)
		}
		if (BrandingSettings{OnboardingTour: c.in}).TourEnabled() != c.enabled {
			t.Errorf("%q TourEnabled istenen %v", c.in, c.enabled)
		}
		if c.want == "" && strings.Contains(string(raw), "onboardingTour") {
			t.Errorf("%q: boş değer blob'a yazılmamalı: %s", c.in, raw)
		}
	}
}
