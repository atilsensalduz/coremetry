package copilot

// sensitive.go — v0.10.1451 (CoSRE araştırma programı karar 8; operasyon kartı
// planı R1 "localOnly kapısı"): kaynak kod ve wiki sayfa gövdesi taşıyan bir
// istek YALNIZ admin'in "Yerel (self-hosted) model" diye işaretlediği profile
// gider; dış sağlayıcıya asla.
//
// Üç parça, üçü de bu dosyada:
//
//   - İŞARET: WithSensitiveContent(ctx, kind) — isteği kuran katman (api:
//     explain kod bloğu, sohbet döngüsünde kod/wiki araçları, wiki anlatımı,
//     RAG wiki bağlamı) ctx'i işaretler. SensitiveFromContext okur.
//   - SAF KAPI: LocalOnlyAllowed(profil) / CheckLocalOnly(kind, profil) —
//     profil beyanı + sağlayıcı/host denetimi; durum yok, döngü (internal/sre
//     PR 2) aynısını çağırır.
//   - HARD FAIL: Service'in dört giriş noktası (explain, StreamText,
//     ChatWithTools, ChatWithToolsStream) sağlayıcı çağrısından ÖNCE
//     localOnlyGate'i çağırır: işaretli ctx + yerel olmayan profil →
//     ErrLocalOnly, sağlayıcıya tek bayt gitmez (ai_calls
//     error_class=local_only). Bu arka kapıdır: asıl yol çağıranın
//     RouteSensitive ile yerel profile geçmesi ya da (yerel profil yoksa)
//     içeriği hiç kurmamasıdır.
//
// Tik ADMIN BEYANIDIR (operatör kararı 1, 2026-10-23): model adından ya da
// hosttan çıkarım YOK; doğrulama yalnız açıkça imkânsız beyanı reddeder
// (anthropic/github sağlayıcısı, boş baseURL, bilinen genel LLM hostu).

import (
	"context"
	"errors"
	"log"
	"net/url"
	"strings"
	"sync"
)

// SensitiveKind — isteğin taşıdığı hassas içerik türü.
type SensitiveKind string

const (
	SensitiveCode SensitiveKind = "code"
	SensitiveWiki SensitiveKind = "wiki"
)

// localOnlyErrMarker — ErrLocalOnly metninin sınıflandırıcı anahtarı
// (ClassifyAIErrorText küçük harfe çevirip arar; sohbet hatası RecordUsage'a
// yalnız metin olarak ulaşır).
const localOnlyErrMarker = "localonly:"

// ErrLocalOnly — işaretli istek yerel olmayan profile gidecekti.
var ErrLocalOnly = errors.New("localOnly: kod/wiki içeriği yalnız yerel model profiline gider")

type sensitiveKey struct{}

// WithSensitiveContent — ctx'i hassas içerik taşıyor diye işaretler. Birden
// çok tür birleşir ("code+wiki"); boş tür ctx'i değiştirmez.
func WithSensitiveContent(ctx context.Context, kind SensitiveKind) context.Context {
	if kind == "" {
		return ctx
	}
	cur := SensitiveFromContext(ctx)
	switch {
	case cur == "" || cur == kind:
		return context.WithValue(ctx, sensitiveKey{}, kind)
	case strings.Contains(string(cur), string(kind)):
		return ctx
	default:
		return context.WithValue(ctx, sensitiveKey{}, cur+"+"+kind)
	}
}

// SensitiveFromContext — işaret ("" = işaretsiz).
func SensitiveFromContext(ctx context.Context) SensitiveKind {
	if ctx == nil {
		return ""
	}
	k, _ := ctx.Value(sensitiveKey{}).(SensitiveKind)
	return k
}

// publicLLMHosts — LocalOnly beyanının REDDEDİLDİĞİ bilinen genel LLM uçları
// (son ek eşleşmesi). Liste kapsayıcı değil: amaç bariz yanlış beyanı
// yakalamak; beyanın doğruluğu admin'in sorumluluğu.
var publicLLMHosts = []string{
	"api.openai.com", "openai.azure.com", "generativelanguage.googleapis.com",
	"aiplatform.googleapis.com", "openrouter.ai", "api.groq.com", "api.mistral.ai",
	"api.together.xyz", "api.deepseek.com", "api.anthropic.com", "api.cohere.ai",
	"api.cohere.com", "api.fireworks.ai", "api.perplexity.ai", "api.x.ai",
	"inference.ai.azure.com", "models.inference.ai.azure.com", "api.githubcopilot.com",
}

// isPublicLLMHost — SAF: baseURL'in hostu genel listede mi (alt alan dahil).
// Ayrıştırılamayan URL genel SAYILIR (beyan reddedilir — fail-closed).
func isPublicLLMHost(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Hostname() == "" {
		return true
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	for _, p := range publicLLMHosts {
		if h == p || strings.HasSuffix(h, "."+p) {
			return true
		}
	}
	return false
}

// validateLocalOnly — ValidateProfile'ın LocalOnly kuralı.
func validateLocalOnly(p ModelProfile) error {
	if !p.LocalOnly {
		return nil
	}
	if p.Provider != ProviderOpenAI {
		return errors.New("yerel model işareti yalnız OpenAI-uyumlu (self-hosted) profilde olabilir")
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return errors.New("yerel model işareti için Base URL gerekli (self-hosted uç)")
	}
	if isPublicLLMHost(p.BaseURL) {
		return errors.New("yerel model işareti genel bir LLM sağlayıcısı adresine verilemez")
	}
	return nil
}

// LocalOnlyAllowed — SAF: profil hassas içerik alabilir mi? Admin tiki +
// (blob elle düzenlenmiş olabilir) çalışma anında aynı sağlayıcı/host kuralı.
func LocalOnlyAllowed(p ModelProfile) bool {
	return p.LocalOnly && validateLocalOnly(p) == nil
}

// CheckLocalOnly — SAF kapı: işaretsiz istek her profile gider; işaretli
// istek yalnız LocalOnlyAllowed profile. Sohbet ve araştırma döngüsü
// (internal/sre) aynı kuralı buradan okur.
func CheckLocalOnly(kind SensitiveKind, p ModelProfile) error {
	if kind == "" || LocalOnlyAllowed(p) {
		return nil
	}
	return ErrLocalOnly
}

// ProfileLocalOnly — çağrının ÇÖZÜLEN profili hassas içerik alabilir mi.
func (s *Service) ProfileLocalOnly(ctx context.Context) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return LocalOnlyAllowed(s.resolveProfileLocked(ctx).cfg)
}

// LocalOnlyProfileFor — role açık bir yerel profil: varsayılan profil yerel
// ve role açıksa o; değilse kimliğe göre sıralı ilk uygun profil. Model adına
// bakılmaz (Gemma, Qwen … hepsi yalnız profil).
func (s *Service) LocalOnlyProfileFor(role string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ok := func(p ModelProfile) bool { return LocalOnlyAllowed(p) && ProfileRoleAllowed(p, role) }
	if d := s.profiles[s.defaultID]; d != nil && ok(d.cfg) {
		return d.cfg.ID, true
	}
	best := ""
	for id, rt := range s.profiles {
		if ok(rt.cfg) && (best == "" || id < best) {
			best = id
		}
	}
	return best, best != ""
}

// LocalOnlyAvailable — bu çağrı için hassas içerik bir yerel profile
// gidebilir mi (çözülen profil yerel ya da role açık bir yerel profil var).
// Çağıran, false iken kod/wiki içeriğini HİÇ kurmaz ("kapalı: yerel profil yok").
func (s *Service) LocalOnlyAvailable(ctx context.Context, role string) bool {
	if s.ProfileLocalOnly(ctx) {
		return true
	}
	_, ok := s.LocalOnlyProfileFor(role)
	return ok
}

// RouteSensitive — hassas içerik taşıyacak isteğin ctx'i: çözülen profil
// yerelse O profil WithProfile ile SABİTLENİR (alt çağrılar yüzey haritasıyla
// başka profile kaymasın), değilse role açık yerel profile geçilir; ctx
// işaretlenir. Yerel profil yoksa ctx AYNEN döner, ok=false — çağıran içeriği
// kurmaz (kurarsa kapı ErrLocalOnly döner).
func (s *Service) RouteSensitive(ctx context.Context, role string, kind SensitiveKind) (context.Context, bool) {
	if s == nil {
		return ctx, false
	}
	if s.ProfileLocalOnly(ctx) {
		return WithSensitiveContent(WithProfile(ctx, s.profileID(ctx)), kind), true
	}
	id, ok := s.LocalOnlyProfileFor(role)
	if !ok {
		return ctx, false
	}
	return WithSensitiveContent(WithProfile(ctx, id), kind), true
}

// localOnlyGate — dört giriş noktasının arka kapısı (sağlayıcı çağrısından
// önce). İhlal her zaman loglanır.
func (s *Service) localOnlyGate(ctx context.Context) error {
	k := SensitiveFromContext(ctx)
	if k == "" {
		return nil
	}
	s.mu.RLock()
	p := s.resolveProfileLocked(ctx).cfg
	s.mu.RUnlock()
	if err := CheckLocalOnly(k, p); err != nil {
		log.Printf("[copilot] localOnly ihlali engellendi: surface=%s profile=%s kind=%s", MetaFromContext(ctx).Surface, p.ID, k)
		return err
	}
	return nil
}

var localOnlyHintOnce sync.Once

// noteLocalOnlyHint — boot ipucu: hiçbir profil yerel işaretli değilse BİR
// kez log (otomatik işaret YOK — operatör kararı 1).
func (s *Service) noteLocalOnlyHint() {
	if _, ok := s.LocalOnlyProfileFor("admin"); ok {
		return
	}
	localOnlyHintOnce.Do(func() {
		log.Printf("[copilot] hiçbir model profili 'Yerel (self-hosted) model' işaretli değil — kod/wiki okuma kapalı; Ayarlar › AI › profil › Yerel model")
	})
}
