package sre

// contextpack_settings.go — v0.10.1494 (CoSRE 2d): Context Pack bayrağı ve
// bütçesi — system_settings["sre_agent.contextpack"] (ayrı anahtar: sre_agent
// formu tüm blobu yazar, yeni alanı sessizce silerdi; /aiops §11 deseni).
//
// Enabled VARSAYILAN KAPALI. Kapalıyken: dört deterministik okuyucu
// (baseline / anomaliler / hata özeti + deploy etkisi) planlanmaz, anlatım
// girdisi eski 4 KB legacy kesimi (bayt bayt — testdata/narration altınları),
// kayıt contextPack künyesi taşımaz. Açıkken: okuyucular dalga 1/2'de koşar,
// anlatım öncelikli kırpmayla (MaxTokens × 4 bayt) yeni bölümleri taşır,
// künye (PackMeta) kayda yazılır.
//
// Değerlendirme tekrarı (Kind=replay) bu bayrağı OKUMAZ: tekrar işinin paketi
// koşu isteğinden gelir (EvalRunRequest.ContextPack → Investigation.Pack) —
// bayrak açıkken koşulan taban tekrarı yine paketsizdir, B ↔ C' kıyası aynı
// kümede yalnız paket farkını ölçer.

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync/atomic"
)

// ContextPackSettingsKey — system_settings anahtarı.
const ContextPackSettingsKey = "sre_agent.contextpack"

// Baseline referans türleri (mcptools.ReferenceWindow sözlüğü).
const (
	BaselinePrevious   = "previous"
	BaselineDayBefore  = "day_before"
	BaselineWeekBefore = "week_before"
)

// DefaultBaselines — boş liste / yok = önceki pencere + 7 gün önce.
var DefaultBaselines = []string{BaselinePrevious, BaselineWeekBefore}

// ContextPackSettings — blob.
type ContextPackSettings struct {
	Enabled bool `json:"enabled"`
	// MaxTokens — öncelikli kırpmanın tavanı (≈ 4 bayt/token; 512–8000).
	MaxTokens int `json:"maxTokens"`
	// Baselines — previous | week_before | day_before (sıra korunur, tekrarsız).
	Baselines []string `json:"baselines"`
	// TopAnomalies — öznenin skor sıralı en çok N anomalisi (1–10).
	TopAnomalies int    `json:"topAnomalies"`
	UpdatedBy    string `json:"updatedBy,omitempty"`
	UpdatedAt    int64  `json:"updatedAt,omitempty"`
}

// DefaultContextPackSettings — tasarım varsayılanları (kapalı).
func DefaultContextPackSettings() ContextPackSettings {
	return ContextPackSettings{MaxTokens: 2000, Baselines: append([]string(nil), DefaultBaselines...), TopAnomalies: 5}
}

// NormalizeContextPackSettings — SAF: sıfır alan varsayılana, aralık dışı
// tavana; bilinmeyen baseline türü düşer, boş liste varsayılana döner.
func NormalizeContextPackSettings(c ContextPackSettings) ContextPackSettings {
	d := DefaultContextPackSettings()
	c.MaxTokens = clampInt(c.MaxTokens, 512, 8000, d.MaxTokens)
	c.TopAnomalies = clampInt(c.TopAnomalies, 1, 10, d.TopAnomalies)
	seen := map[string]bool{}
	out := []string{}
	for _, b := range c.Baselines {
		b = strings.ToLower(strings.TrimSpace(b))
		switch b {
		case BaselinePrevious, BaselineDayBefore, BaselineWeekBefore:
			if !seen[b] {
				seen[b] = true
				out = append(out, b)
			}
		}
	}
	if len(out) == 0 {
		out = d.Baselines
	}
	c.Baselines = out
	return c
}

var currentPack atomic.Pointer[ContextPackSettings]

// CurrentContextPackSettings — sıcak yol (atomic). Yüklenmediyse varsayılan (kapalı).
func CurrentContextPackSettings() ContextPackSettings {
	if c := currentPack.Load(); c != nil {
		return *c
	}
	return NormalizeContextPackSettings(DefaultContextPackSettings())
}

// SetContextPackSettings — normalize edip yayınlar (süreç-içi anlık görüntü).
func SetContextPackSettings(c ContextPackSettings) {
	n := NormalizeContextPackSettings(c)
	currentPack.Store(&n)
}

func parseContextPackSettings(raw []byte) (ContextPackSettings, bool) {
	if len(raw) == 0 {
		return NormalizeContextPackSettings(DefaultContextPackSettings()), true
	}
	c := DefaultContextPackSettings()
	if err := json.Unmarshal(raw, &c); err != nil {
		return NormalizeContextPackSettings(DefaultContextPackSettings()), false
	}
	return NormalizeContextPackSettings(c), true
}

// GetContextPackSettings — saklanan ayar (yoksa varsayılan).
func GetContextPackSettings(ctx context.Context, st SettingsStore) (ContextPackSettings, error) {
	raw, err := st.GetSetting(ctx, ContextPackSettingsKey)
	if err != nil {
		return ContextPackSettings{}, err
	}
	c, _ := parseContextPackSettings(raw)
	return c, nil
}

// LoadContextPackSettings — boot + 30 s tazeleyici. Okuma hatası / bozuk blob
// SON değeri korur.
func LoadContextPackSettings(ctx context.Context, st SettingsStore) error {
	raw, err := st.GetSetting(ctx, ContextPackSettingsKey)
	if err != nil {
		return err
	}
	c, ok := parseContextPackSettings(raw)
	if !ok {
		log.Printf("[sre] sre_agent.contextpack ayarı bozuk — son değer korunuyor")
		return nil
	}
	SetContextPackSettings(c)
	return nil
}

// SaveContextPackSettings — admin PUT.
func SaveContextPackSettings(ctx context.Context, st SettingsStore, c ContextPackSettings) (ContextPackSettings, error) {
	n := NormalizeContextPackSettings(c)
	raw, err := json.Marshal(n)
	if err != nil {
		return ContextPackSettings{}, err
	}
	if err := st.PutSetting(ctx, ContextPackSettingsKey, raw); err != nil {
		return ContextPackSettings{}, err
	}
	SetContextPackSettings(n)
	return n, nil
}
