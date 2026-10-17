package chstore

// cosre_rules.go — v0.10.1289 (CoSRE kurallar / hafıza: "bunu hatırla").
//
// Operatör kuralları system_settings `cosre_rules` JSON blobunda
// ({rules, rev}; şekil ve saf kurallar internal/ai/cosrerules). Değişmez 6:
// yeni şema yok. Şablon ai_chat_retention.go: boot'ta yükle + 30 s yenileme
// (cfgRefresh, HER rol — sohbet api'de, AI SRE anlatımı sre/all'da okur),
// süreç-içi atomic CurrentCosreRules(). Yazma yolu api/cosre_rules.go: Redis
// kilidi + If-Match rev altında oku-değiştir-yaz; bu dosya yalnız depolar.

import (
	"context"
	"encoding/json"
	"log"
	"sync/atomic"

	"github.com/cilcenk/coremetry/internal/ai/cosrerules"
)

// CosreRulesKey — system_settings anahtarı.
const CosreRulesKey = "cosre_rules"

// DefaultCosreRules — kural yok (prompt'lar bayt bayt eski).
func DefaultCosreRules() cosrerules.Doc { return cosrerules.Doc{Rules: []cosrerules.Rule{}} }

var cosreRulesCur atomic.Pointer[cosrerules.Doc]

// CurrentCosreRules — süreç-içi son yüklenen kurallar (model çağrısının sıcak
// yolu; CH okuması değil).
func CurrentCosreRules() cosrerules.Doc {
	if d := cosreRulesCur.Load(); d != nil {
		return *d
	}
	return DefaultCosreRules()
}

// SetCosreRules — yazımdan sonra bu pod'u hemen günceller (diğer pod'lar
// 30 s yenilemesiyle yakınsar).
func SetCosreRules(d cosrerules.Doc) {
	n := cosrerules.NormalizeDoc(d)
	if n.Rules == nil {
		n.Rules = []cosrerules.Rule{}
	}
	cosreRulesCur.Store(&n)
}

// parseCosreRules — SAF: blob → belge; boş blob varsayılan, bozuk blob ok=false.
func parseCosreRules(raw []byte) (cosrerules.Doc, bool) {
	if len(raw) == 0 {
		return DefaultCosreRules(), true
	}
	var d cosrerules.Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		return cosrerules.Doc{}, false
	}
	d = cosrerules.NormalizeDoc(d)
	if d.Rules == nil {
		d.Rules = []cosrerules.Rule{}
	}
	return d, true
}

// GetCosreRules — saklanan belge (bozuk blob → hata; yazma yolu bozuk blobun
// üstüne sessizce boş belge yazmasın).
func (s *Store) GetCosreRules(ctx context.Context) (cosrerules.Doc, error) {
	raw, err := s.GetSetting(ctx, CosreRulesKey)
	if err != nil {
		return cosrerules.Doc{}, err
	}
	d, ok := parseCosreRules(raw)
	if !ok {
		return cosrerules.Doc{}, errCosreRulesCorrupt
	}
	return d, nil
}

// SaveCosreRules — belgeyi yazar (normalize edilmiş hâli).
func (s *Store) SaveCosreRules(ctx context.Context, d cosrerules.Doc) error {
	n := cosrerules.NormalizeDoc(d)
	if n.Rules == nil {
		n.Rules = []cosrerules.Rule{}
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, CosreRulesKey, raw)
}

// LoadCosreRules — boot + 30 s yenileme. Bozuk blob son değeri korur.
func (s *Store) LoadCosreRules(ctx context.Context) error {
	raw, err := s.GetSetting(ctx, CosreRulesKey)
	if err != nil {
		return err
	}
	d, ok := parseCosreRules(raw)
	if !ok {
		log.Printf("[cosre-rules] cosre_rules ayarı bozuk — son değer korunuyor")
		return nil
	}
	SetCosreRules(d)
	return nil
}

type cosreRulesErr string

func (e cosreRulesErr) Error() string { return string(e) }

const errCosreRulesCorrupt = cosreRulesErr("cosre_rules ayarı bozuk (JSON çözülemedi) — yazım reddedildi")
