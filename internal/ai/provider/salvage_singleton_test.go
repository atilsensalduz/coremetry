// salvage_singleton_test.go — FAZ 1.2'nin YAPISAL kapısı.
//
// Denetim (2026-08-16) kurtarma zincirinin ÜÇ kopyasını saymıştı:
// copilot.go (buffered explain), stream.go (SSE biriktirici) ve Faz
// 1.1'de bilinçli geçici kopya olarak açılan bu paket. Faz 1.2 o sayıyı
// 1'e indiriyor. Kod incelemesi bunu bir kez doğrular; bu test HER
// koşuda doğrular.
//
// Neden gövde/davranış testi yetmiyor: ikinci bir kopya EKLEMEK hiçbir
// davranış testini kırmaz — iki kopya bugün aynı şeyi yapar, sonra
// biri düzeltilir öteki unutulur. 1024 max_tokens paritesi tam olarak
// böyle ~1000 sürüm yaşadı (v0.9.1120).
//
// Tarayıcı bilinçli olarak DAR: yalnız `^func ` çıpası (yorum satırları
// `//` ile başladığı için sayılmaz) ve non-test dosyalar. Önek çakışması
// yok — tam parantezli imza aranıyor, `StripThinkingFoo` eşleşmez.
package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scanNonTestGoFiles — internal/ altındaki tüm non-test .go dosyaları.
// Depo kökündeki scratchpad/ (eski worktree kopyaları) KAPSAM DIŞI:
// orada canlı olmayan kopyalar var ve onları saymak kapıyı sürekli
// kırmızı tutardı.
func scanNonTestGoFiles(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "..", "internal")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("internal/ bulunamadı (%v) — kapı yolu bozulmuş, testin kendisi anlamsızlaşır", err)
	}
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(path)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(out) < 50 {
		// Tarayıcı hiçbir şey bulmazsa test SESSİZCE geçerdi ("0 kopya
		// var" = "1 değil" bile değil). Taban kontrolü o hâli hataya
		// çevirir.
		t.Fatalf("yalnız %d dosya tarandı — tarayıcı bozuk, sonuç anlamsız", len(out))
	}
	return out
}

func TestSalvageChainHasExactlyOneImplementation(t *testing.T) {
	files := scanNonTestGoFiles(t)

	// Her biri zincirin bir parçası; üçü de TEK dosyada (salvage.go)
	// yaşamalı.
	wanted := []string{
		"func StripThinking(",
		"func ThinkingContent(",
		"func SalvageAnswer(",
		// Silinen küçük-harfli ikizler. Geri gelirlerse "thin alias"
		// niyetiyle bile olsa iki yazılış demektir.
		"func stripThinking(",
		"func thinkingContent(",
	}
	for _, sig := range wanted {
		var hits []string
		for path, src := range files {
			for _, line := range strings.Split(src, "\n") {
				if strings.HasPrefix(line, sig) {
					hits = append(hits, path)
					break
				}
			}
		}
		lower := strings.HasPrefix(sig, "func s") || strings.HasPrefix(sig, "func t")
		if lower {
			if len(hits) != 0 {
				t.Errorf("%s hâlâ tanımlı (%v) — küçük-harfli ikizler Faz 1.2'de silindi", sig, hits)
			}
			continue
		}
		if len(hits) != 1 {
			t.Errorf("%s %d yerde tanımlı (%v), want 1", sig, len(hits), hits)
			continue
		}
		if !strings.HasSuffix(hits[0], "internal/ai/provider/salvage.go") {
			t.Errorf("%s beklenmedik dosyada: %s", sig, hits[0])
		}
	}
}

// TestThinkStripIdiomLivesInOneFile — imza adı değiştirilerek ikinci
// bir kopya açılabilir; bu kapı DEYİMİ sayıyor.
//
// v0.10.1285 — eski deyim ("son </think> sonrasını al":
// strings.LastIndex(s, "</think>")) tamponlu yolun AYRI yazılışıydı ve
// Gemma kanal bloğunu tanımadığı için akıştan ayrıştı. Kural artık
// think_stream.go'daki etiket tablosu + durum makinesi; tamponlu
// StripThinking onu çağırır. Kapı üç şeyi pinler:
//   - eski deyim HİÇBİR non-test dosyada yok (geri gelirse iki yazılış);
//   - etiket tablosu (thinkTagPairs) tek dosyada tanımlı;
//   - Gemma açılış etiketi literal'i yalnız o tabloda (toolcall_text.go'nun
//     ayrı regex'i v0.10.1285'te kalktı).
//
// v0.10.1304 — stream.go'nun eski ayrı kapısı (yalnız baştaki <think>)
// kalktı; tek-atış akış da artık aynı makineyi kullanır
// (live_think_gate.go → thinkStripper). Nihai metin yine SalvageAnswer →
// StripThinking.
func TestThinkStripIdiomLivesInOneFile(t *testing.T) {
	files := scanNonTestGoFiles(t)
	const thinkStream = "internal/ai/provider/think_stream.go"
	for _, c := range []struct {
		needle string
		want   []string // izinli dosyalar (boş = hiçbiri)
	}{
		{`strings.LastIndex(s, "</think>")`, nil},
		{"var thinkTagPairs = ", []string{thinkStream}},
		{`"<|channel>thought"`, []string{thinkStream}},
		{"<\\|channel>thought", nil}, // regex yazılışı (eski gemmaThoughtRe)
	} {
		var hits []string
		for path, src := range files {
			if strings.Contains(src, c.needle) {
				hits = append(hits, path)
			}
		}
		ok := len(hits) == len(c.want)
		for i := range hits {
			if !ok {
				break
			}
			ok = strings.HasSuffix(hits[i], c.want[i])
		}
		if !ok {
			t.Errorf("%s: %d dosyada (%v), want %v", c.needle, len(hits), hits, c.want)
		}
	}
	// StripThinking makineye bağlı kalmalı — gövde yeniden elle yazılırsa
	// parite testi davranışı yakalar ama niyet burada okunur.
	if src := files["../../../internal/ai/provider/salvage.go"]; !strings.Contains(src, "thinkVisible(s)") {
		t.Error("StripThinking artık thinkVisible'ı çağırmıyor — tamponlu ve akan düşünce kuralı yeniden ayrıştı")
	}
}

// TestBufferedBuildersAreGone — Faz 1.2'nin silme sözü. Bu üç üretici
// gövde şeklinin üç ayrı yazılışıydı; geri gelmeleri paritenin yeniden
// ayrışabileceği anlamına gelir.
func TestBufferedBuildersAreGone(t *testing.T) {
	files := scanNonTestGoFiles(t)
	for _, name := range []string{
		"explainOpenAIWithUsage",
		"explainAnthropicWithUsage",
		"explainGitHubWithUsage",
		"parseOpenAIChatResponse",
		"parseAnthropicResponse",
	} {
		for path, src := range files {
			if strings.Contains(src, "func "+name+"(") || strings.Contains(src, ") "+name+"(") {
				t.Errorf("%s hâlâ tanımlı: %s", name, path)
			}
		}
	}
}
