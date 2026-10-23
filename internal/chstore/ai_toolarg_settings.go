package chstore

// ai_toolarg_settings.go — v0.10.1456 (CoSRE araştırma programı C1, karar 6):
// araç argümanı şema doğrulamasının yüzey başına kipi — system_settings
// `ai.toolArgValidation` blobu:
//
//	{"chat":"warn","sreLoop":"enforce","mcp":"off","updatedBy":"…","updatedAt":"…"}
//
// Varsayılan (blob yok): chat=warn (operatör kararı 3, 2026-10-23: bir hafta
// ölç, sonra enforce), sreLoop=enforce (karar 6), mcp=off (dış MCP teli
// doğrulanmaz; alan ileride). Şablon ai_toolsets.go: boot'ta yükle + 30 s
// cfgRefresh (HER rol — sohbet api'de, AI SRE döngüsü sre/all'da), süreç-içi
// atomic. Yazma yolu api/ai_toolarg_settings.go (admin PUT + audit).
//
// Kip adları agent/tools sabitleriyle aynı dizgiler (off|warn|enforce); bu
// paket onu içe aktarmaz — eşitlik api testinde pinli.
//
// Geri alma: `{"chat":"off","sreLoop":"off"}` yaz (kod değişmeden bugünkü
// davranış) ya da revert — blob okuyansız kalırsa zararsız.

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

// AIToolArgValidationKey — system_settings anahtarı.
const AIToolArgValidationKey = "ai.toolArgValidation"

// Kip değerleri (agent/tools.ArgMode* ile aynı).
const (
	ToolArgModeOff     = "off"
	ToolArgModeWarn    = "warn"
	ToolArgModeEnforce = "enforce"
)

// AIToolArgValidation — blob.
type AIToolArgValidation struct {
	Chat      string    `json:"chat"`
	SreLoop   string    `json:"sreLoop"`
	MCP       string    `json:"mcp"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

// DefaultAIToolArgValidation — blob yokken geçerli kipler.
func DefaultAIToolArgValidation() AIToolArgValidation {
	return AIToolArgValidation{Chat: ToolArgModeWarn, SreLoop: ToolArgModeEnforce, MCP: ToolArgModeOff}
}

// ValidToolArgMode — kip adı tanınıyor mu.
func ValidToolArgMode(m string) bool {
	return m == ToolArgModeOff || m == ToolArgModeWarn || m == ToolArgModeEnforce
}

// NormalizeAIToolArgValidation — SAF: kırp + küçült; boş ya da tanınmayan
// alan VARSAYILANINA döner (bozuk tek alan diğerlerini sıfırlamaz). mcp
// bugün yalnız off (dış tel doğrulanmıyor; PUT farklısını 400'ler).
func NormalizeAIToolArgValidation(a AIToolArgValidation) AIToolArgValidation {
	d := DefaultAIToolArgValidation()
	norm := func(v, def string) string {
		v = strings.ToLower(strings.TrimSpace(v))
		if !ValidToolArgMode(v) {
			return def
		}
		return v
	}
	a.Chat = norm(a.Chat, d.Chat)
	a.SreLoop = norm(a.SreLoop, d.SreLoop)
	a.MCP = ToolArgModeOff
	a.UpdatedBy = strings.TrimSpace(a.UpdatedBy)
	return a
}

var aiToolArgCur atomic.Pointer[AIToolArgValidation]

// CurrentAIToolArgValidation — süreç-içi son yüklenen ayar (CH okuması değil).
func CurrentAIToolArgValidation() AIToolArgValidation {
	if a := aiToolArgCur.Load(); a != nil {
		return *a
	}
	return DefaultAIToolArgValidation()
}

// SetAIToolArgValidation — yazımdan sonra bu pod'u hemen günceller.
func SetAIToolArgValidation(a AIToolArgValidation) {
	n := NormalizeAIToolArgValidation(a)
	aiToolArgCur.Store(&n)
}

// parseAIToolArgValidation — SAF: boş blob varsayılan, bozuk blob ok=false.
func parseAIToolArgValidation(raw []byte) (AIToolArgValidation, bool) {
	if len(raw) == 0 {
		return DefaultAIToolArgValidation(), true
	}
	var a AIToolArgValidation
	if err := json.Unmarshal(raw, &a); err != nil {
		return AIToolArgValidation{}, false
	}
	return NormalizeAIToolArgValidation(a), true
}

// SaveAIToolArgValidation — normalize edilmiş hâli yazar ve bu pod'a yayınlar.
func (s *Store) SaveAIToolArgValidation(ctx context.Context, a AIToolArgValidation) (AIToolArgValidation, error) {
	n := NormalizeAIToolArgValidation(a)
	raw, err := json.Marshal(n)
	if err != nil {
		return AIToolArgValidation{}, err
	}
	if err := s.PutSetting(ctx, AIToolArgValidationKey, raw); err != nil {
		return AIToolArgValidation{}, err
	}
	SetAIToolArgValidation(n)
	return n, nil
}

// LoadAIToolArgValidation — boot + 30 s yenileme. Bozuk blob son değeri korur.
func (s *Store) LoadAIToolArgValidation(ctx context.Context) error {
	raw, err := s.GetSetting(ctx, AIToolArgValidationKey)
	if err != nil {
		return err
	}
	a, ok := parseAIToolArgValidation(raw)
	if !ok {
		log.Printf("[tool-args] ai.toolArgValidation ayarı bozuk — son değer korunuyor")
		return nil
	}
	SetAIToolArgValidation(a)
	return nil
}
