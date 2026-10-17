package modelcaps

// toolset.go — v0.10.1302 — CoSRE: modele göre araç seti (küçük modelde
// serbest-sorgu araçları gizli). Operatör kararı 2026-10-17 ("modele göre").
//
// Sohbet kataloğunun iki varyantı var (mcptools.ChatToolListFor):
//   - full   — bugünkü katalog (query_metric / search_logs / search_traces /
//     list_metric_labels dahil);
//   - simple — o dört serbest-sorgu aracı gizli; küçük model yapısal girişleri
//     (query_metrics / query_logs / query_traces) kullanır.
//
// Hiçbir alan parametre sayısını bilmiyordu (For = aile/düşünme,
// ContextWindow = pencere tahmini). Karar profil başına AÇIK bir ayar
// (copilot.ModelProfile.Toolset) + boşken model adından otomatik kural:
// adda okunabilir bir boyut varsa ve ≤ SmallModelMaxParamsB ise simple,
// aksi hâlde (claude-*, gpt-*, boyutsuz/bilinmeyen ad) full. Güvenli yön
// full: bilinmeyen model bugünkü kataloğu görür.

import (
	"regexp"
	"strconv"
	"strings"
)

// Araç seti değerleri — profil ayarı "" (otomatik) | simple | full.
const (
	ToolsetAuto   = ""
	ToolsetSimple = "simple"
	ToolsetFull   = "full"
)

// SmallModelMaxParamsB — otomatik kuralın eşiği (milyar parametre, dahil).
const SmallModelMaxParamsB = 12.0

// ValidToolset — profil doğrulaması (ValidThinking emsali).
func ValidToolset(s string) bool {
	switch s {
	case ToolsetAuto, ToolsetSimple, ToolsetFull:
		return true
	}
	return false
}

// paramsRe — model adındaki boyut son eki: "-2b", ":14b", "_3.8b", "8x7b".
// Sınırlar bilinçli: önünde harf/rakam/nokta olmayan bir sayı ve ardında harf
// olmayan bir "b" ("7bit", "gemma2b" boyut sayılmaz → bilinmiyor → full).
// MoE "NxM b" çarpılır (mixtral-8x7b ≈ 56B → full).
var paramsRe = regexp.MustCompile(`(?:^|[^a-z0-9.])(?:(\d+)x)?(\d+(?:\.\d+)?)b(?:[^a-z]|$)`)

// ParamsB — SAF: model adından parametre sayısı (milyar); okunamıyorsa 0.
// İlk (en soldaki) eşleşme kazanır: "qwen3-30b-a3b" → 30 (aktif değil toplam).
func ParamsB(model string) float64 {
	m := strings.ToLower(strings.TrimSpace(model))
	sm := paramsRe.FindStringSubmatch(m)
	if sm == nil {
		return 0
	}
	n, err := strconv.ParseFloat(sm[2], 64)
	if err != nil || n <= 0 {
		return 0
	}
	if sm[1] != "" {
		if k, err := strconv.ParseFloat(sm[1], 64); err == nil && k > 0 {
			n *= k
		}
	}
	return n
}

// DefaultToolset — SAF: otomatik kural. Boyut okunur ve ≤ 12B → simple;
// aksi hâlde full (bilinmeyen = bugünkü katalog).
func DefaultToolset(model string) string {
	if p := ParamsB(model); p > 0 && p <= SmallModelMaxParamsB {
		return ToolsetSimple
	}
	return ToolsetFull
}

// ResolveToolset — SAF: açık ayar kazanır; boş/geçersiz → otomatik kural.
func ResolveToolset(setting, model string) string {
	switch setting {
	case ToolsetSimple, ToolsetFull:
		return setting
	}
	return DefaultToolset(model)
}
