package chstore

// ai_toolsets.go — v0.10.1434 (CoSRE araştırma programı PR 1;
// docs/audits/cosre-investigation-model-audit.md §6.1): operatörün kapattığı
// AI araç setleri (toolset) — system_settings `ai.toolsets` blobu:
//
//	{"disabled":["devops-code"],"updatedBy":"…","updatedAt":"…"}
//
// Değişmez 6: yeni şema yok. Şablon cosre_rules.go: boot'ta yükle + 30 s
// yenileme (cfgRefresh, HER rol — sohbet api'de, dış MCP kapısı api'de, AI
// SRE drain aynı mcpDeps kurucusunu sre/all'da okur), süreç-içi atomic
// CurrentAIToolsets(). Yazma yolu api/ai_toolsets.go (admin PUT + audit).
//
// Bu dosya toolset ADLARINI BİLMEZ (mcptools → chstore import yönü; tersi
// döngü olurdu): normalize yalnız biçimseldir (kırp, küçült, tekrar at, sırala,
// tavan). Bilinmeyen ad / kapatılamaz set kararı api PUT'ta (400) ve
// mcptools.effectiveDisabled'da (savunma derinliği) verilir.
//
// Geri alma: blob okuyansız kalırsa zararsızdır — hiçbir okuma yolu yoksa
// katalog HEAD davranışına döner (süzgeç kodla birlikte gider).

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// AIToolsetsKey — system_settings anahtarı.
const AIToolsetsKey = "ai.toolsets"

const (
	aiToolsetsMaxNames = 32
	aiToolsetNameMax   = 48
)

// AIToolsets — blob.
type AIToolsets struct {
	Disabled  []string  `json:"disabled"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

// DisabledSet — sıcak yol kümesi; boş liste nil (süzgeç hiç çalışmaz).
func (a AIToolsets) DisabledSet() map[string]bool {
	if len(a.Disabled) == 0 {
		return nil
	}
	m := make(map[string]bool, len(a.Disabled))
	for _, n := range a.Disabled {
		m[n] = true
	}
	return m
}

// NormalizeAIToolsets — SAF: ad listesi kırpılır, küçültülür, boş/uzun ad ve
// tekrar düşer, sıralanır, tavan uygulanır. Disabled hiçbir zaman nil değil
// (JSON'da `[]`).
func NormalizeAIToolsets(a AIToolsets) AIToolsets {
	seen := map[string]bool{}
	out := make([]string, 0, len(a.Disabled))
	for _, n := range a.Disabled {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || len(n) > aiToolsetNameMax || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	if len(out) > aiToolsetsMaxNames {
		out = out[:aiToolsetsMaxNames]
	}
	a.Disabled = out
	a.UpdatedBy = strings.TrimSpace(a.UpdatedBy)
	return a
}

var aiToolsetsCur atomic.Pointer[AIToolsets]

// CurrentAIToolsets — süreç-içi son yüklenen ayar (CH okuması değil).
// Yüklenmediyse varsayılan: hepsi açık.
func CurrentAIToolsets() AIToolsets {
	if a := aiToolsetsCur.Load(); a != nil {
		return *a
	}
	return AIToolsets{Disabled: []string{}}
}

// SetAIToolsets — yazımdan sonra bu pod'u hemen günceller (diğer pod'lar
// 30 s yenilemesiyle yakınsar).
func SetAIToolsets(a AIToolsets) {
	n := NormalizeAIToolsets(a)
	aiToolsetsCur.Store(&n)
}

// parseAIToolsets — SAF: boş blob varsayılan, bozuk blob ok=false.
func parseAIToolsets(raw []byte) (AIToolsets, bool) {
	if len(raw) == 0 {
		return NormalizeAIToolsets(AIToolsets{}), true
	}
	var a AIToolsets
	if err := json.Unmarshal(raw, &a); err != nil {
		return AIToolsets{}, false
	}
	return NormalizeAIToolsets(a), true
}

// GetAIToolsets — saklanan ayar (bozuk blob → hata; yazma yolu bozuk blobun
// üstüne sessizce yazmasın).
func (s *Store) GetAIToolsets(ctx context.Context) (AIToolsets, error) {
	raw, err := s.GetSetting(ctx, AIToolsetsKey)
	if err != nil {
		return AIToolsets{}, err
	}
	a, ok := parseAIToolsets(raw)
	if !ok {
		return AIToolsets{}, errAIToolsetsCorrupt
	}
	return a, nil
}

// SaveAIToolsets — normalize edilmiş hâli yazar ve bu pod'a yayınlar.
func (s *Store) SaveAIToolsets(ctx context.Context, a AIToolsets) (AIToolsets, error) {
	n := NormalizeAIToolsets(a)
	raw, err := json.Marshal(n)
	if err != nil {
		return AIToolsets{}, err
	}
	if err := s.PutSetting(ctx, AIToolsetsKey, raw); err != nil {
		return AIToolsets{}, err
	}
	SetAIToolsets(n)
	return n, nil
}

// LoadAIToolsets — boot + 30 s yenileme. Bozuk blob son değeri korur.
func (s *Store) LoadAIToolsets(ctx context.Context) error {
	raw, err := s.GetSetting(ctx, AIToolsetsKey)
	if err != nil {
		return err
	}
	a, ok := parseAIToolsets(raw)
	if !ok {
		log.Printf("[ai-toolsets] ai.toolsets ayarı bozuk — son değer korunuyor")
		return nil
	}
	SetAIToolsets(a)
	return nil
}

type aiToolsetsErr string

func (e aiToolsetsErr) Error() string { return string(e) }

const errAIToolsetsCorrupt = aiToolsetsErr("ai.toolsets ayarı bozuk (JSON çözülemedi) — yazım reddedildi")
