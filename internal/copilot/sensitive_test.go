package copilot

// sensitive_test.go — v0.10.1451 (karar 8, R1 localOnly kapısı): işaretli
// istek yerel olmayan profile ASLA gitmez (sağlayıcı 0 istek, ai_calls
// error_class=local_only); beyan doğrulaması; blob gidiş-dönüşü; yönlendirme.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func countingOpenAI(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestLocalOnlyHardFail(t *testing.T) {
	ext, extN := countingOpenAI(t)
	loc, locN := countingOpenAI(t)
	s := New("openai", "", "")
	s.SetProfiles([]ModelProfile{
		{ID: "ext", Provider: ProviderOpenAI, BaseURL: ext.URL + "/v1", Model: "qwen3-32b"},
		{ID: "loc", Provider: ProviderOpenAI, BaseURL: loc.URL + "/v1", Model: "gemma4-31b", LocalOnly: true},
	}, "ext", nil)
	s.SetEnabled(true)
	rec := &captureRecorder{}
	s.SetRecorder(rec)

	marked := WithSensitiveContent(WithMeta(context.Background(), CallMeta{Surface: "explain-trace"}), SensitiveCode)
	calls := map[string]func(context.Context) error{
		"Explain": func(c context.Context) error { _, err := s.Explain(c, "s", "u"); return err },
		"StreamText": func(c context.Context) error {
			_, err := s.StreamText(c, "s", "u", nil)
			return err
		},
		"ChatWithTools": func(c context.Context) error {
			_, err := s.ChatWithTools(c, "s", []ChatMessage{{Role: "user", Text: "q"}}, nil)
			return err
		},
		"ChatWithToolsStream": func(c context.Context) error {
			_, err := s.ChatWithToolsStream(c, "s", []ChatMessage{{Role: "user", Text: "q"}}, nil, nil)
			return err
		},
	}
	for name, call := range calls {
		if err := call(marked); !errors.Is(err, ErrLocalOnly) {
			t.Errorf("%s: işaretli istek yerel olmayan profilde %v döndü, ErrLocalOnly bekleniyordu", name, err)
		}
	}
	if extN.Load() != 0 || locN.Load() != 0 {
		t.Fatalf("hard fail sağlayıcıya istek gönderdi: ext=%d loc=%d", extN.Load(), locN.Load())
	}
	// ai_calls satırı (Explain/StreamText kaydı asenkron)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && rec.last().ErrorClass == "" {
		time.Sleep(10 * time.Millisecond)
	}
	if r := rec.last(); r.Status != "error" || r.ErrorClass != ErrClassLocalOnly {
		t.Fatalf("ai_calls satırı status=%q error_class=%q, error/local_only bekleniyordu", r.Status, r.ErrorClass)
	}
	// Sohbet yolu: handler RecordUsage(err.Error()) → aynı sınıf.
	if c := ClassifyAIErrorText(ErrLocalOnly.Error()); c != ErrClassLocalOnly {
		t.Fatalf("metin sınıfı %q", c)
	}

	// Yerel profil: aynı işaretli istek geçer.
	for name, call := range calls {
		if err := call(WithProfile(marked, "loc")); err != nil {
			t.Errorf("%s: yerel profilde işaretli istek reddedildi: %v", name, err)
		}
	}
	if extN.Load() != 0 || locN.Load() == 0 {
		t.Fatalf("yerel çağrılar yanlış uca: ext=%d loc=%d", extN.Load(), locN.Load())
	}
	// İşaretsiz istek dış profile gider (davranış değişmedi).
	if _, err := s.Explain(context.Background(), "s", "u"); err != nil || extN.Load() != 1 {
		t.Fatalf("işaretsiz istek: err=%v ext=%d", err, extN.Load())
	}
}

func TestValidateProfileLocalOnly(t *testing.T) {
	cases := []struct {
		p  ModelProfile
		ok bool
	}{
		{ModelProfile{ID: "a", Provider: ProviderAnthropic, LocalOnly: true}, false},
		{ModelProfile{ID: "g", Provider: ProviderGitHub, LocalOnly: true}, false},
		{ModelProfile{ID: "o", Provider: ProviderOpenAI, LocalOnly: true}, false},
		{ModelProfile{ID: "o", Provider: ProviderOpenAI, BaseURL: "https://api.openai.com/v1", LocalOnly: true}, false},
		{ModelProfile{ID: "o", Provider: ProviderOpenAI, BaseURL: "https://kx.openai.azure.com/openai", LocalOnly: true}, false},
		{ModelProfile{ID: "o", Provider: ProviderOpenAI, BaseURL: "https://openrouter.ai/api/v1", LocalOnly: true}, false},
		{ModelProfile{ID: "o", Provider: ProviderOpenAI, BaseURL: "::bad", LocalOnly: true}, false},
		{ModelProfile{ID: "o", Provider: ProviderOpenAI, BaseURL: "http://vllm.ai.svc:8000/v1", LocalOnly: true}, true},
		{ModelProfile{ID: "o", Provider: ProviderOpenAI, BaseURL: "http://10.0.0.7:8000/v1", LocalOnly: true}, true},
		{ModelProfile{ID: "o", Provider: ProviderOpenAI, BaseURL: "https://llm.kxzone.example.test/v1", Model: "qwen3-32b", LocalOnly: true}, true},
		// işaretsiz profil her sağlayıcıda geçerli (eski davranış)
		{ModelProfile{ID: "a", Provider: ProviderAnthropic}, true},
	}
	for _, c := range cases {
		err := ValidateProfile(c.p)
		if (err == nil) != c.ok {
			t.Errorf("%s %q: err=%v, ok=%v bekleniyordu", c.p.Provider, c.p.BaseURL, err, c.ok)
		}
		if LocalOnlyAllowed(c.p) != (c.ok && c.p.LocalOnly) {
			t.Errorf("LocalOnlyAllowed(%s %q) yanlış", c.p.Provider, c.p.BaseURL)
		}
	}
	// Model adından çıkarım YOK: tiksiz "gemma" profili yerel sayılmaz.
	if LocalOnlyAllowed(ModelProfile{Provider: ProviderOpenAI, BaseURL: "http://vllm:8000/v1", Model: "gemma4-31b"}) {
		t.Fatal("tiksiz profil yerel sayıldı")
	}
}

// Ayarlar gidiş-dönüşü: blob (system_settings JSON) LocalOnly'yi taşır,
// anahtar işleme değişmez (boş anahtar mevcut korunur).
func TestLocalOnlyBlobRoundTrip(t *testing.T) {
	store := newMemStore()
	s := New("openai", "", "")
	ctx := context.Background()
	if err := s.UpsertProfile(ctx, store, ModelProfile{ID: "loc", Provider: ProviderOpenAI, BaseURL: "http://vllm:8000/v1", APIKey: "sk-1", Model: "qwen3-32b", LocalOnly: true}); err != nil {
		t.Fatal(err)
	}
	var blob struct {
		Profiles []map[string]any `json:"profiles"`
	}
	if err := json.Unmarshal(store.m[settingsKey], &blob); err != nil || !blobHasLocal(blob.Profiles) {
		t.Fatalf("blob localOnly taşımıyor: %s", store.m[settingsKey])
	}
	s2 := New("anthropic", "", "")
	if err := s2.LoadPersisted(ctx, store); err != nil {
		t.Fatal(err)
	}
	if p := profileByID(s2.Profiles(), "loc"); !p.LocalOnly || p.APIKey != "sk-1" {
		t.Fatalf("yükleme: %+v", p)
	}
	// Boş anahtarlı güncelleme anahtarı korur, tik kaldırılabilir.
	if err := s2.UpsertProfile(ctx, store, ModelProfile{ID: "loc", Provider: ProviderOpenAI, BaseURL: "http://vllm:8000/v1", Model: "qwen3-32b"}); err != nil {
		t.Fatal(err)
	}
	if p := profileByID(s2.Profiles(), "loc"); p.LocalOnly || p.APIKey != "sk-1" {
		t.Fatalf("tik kaldırma/anahtar koruma: %+v", p)
	}
	// Tiksiz blobda alan hiç yazılmaz (eski binary ile bayt uyumu).
	if strings.Contains(string(store.m[settingsKey]), `"localOnly"`) {
		t.Fatalf("tiksiz blob localOnly yazdı: %s", store.m[settingsKey])
	}
}

func blobHasLocal(ps []map[string]any) bool {
	for _, bp := range ps {
		if bp["id"] == "loc" && bp["localOnly"] == true {
			return true
		}
	}
	return false
}

func profileByID(ps []ModelProfile, id string) ModelProfile {
	for _, p := range ps {
		if p.ID == id {
			return p
		}
	}
	return ModelProfile{}
}

func TestRouteSensitive(t *testing.T) {
	s := New("openai", "", "")
	s.SetProfiles([]ModelProfile{
		{ID: "ext", Provider: ProviderOpenAI, BaseURL: "http://ext:8000/v1"},
		{ID: "zz-loc", Provider: ProviderOpenAI, BaseURL: "http://b:8000/v1", LocalOnly: true},
		{ID: "a-loc", Provider: ProviderOpenAI, BaseURL: "http://a:8000/v1", LocalOnly: true, Roles: []string{"admin"}},
	}, "ext", map[string]string{"chat-intent": "ext"})
	// viewer: a-loc rolüne kapalı → zz-loc
	ctx, ok := s.RouteSensitive(context.Background(), "viewer", SensitiveCode)
	if !ok || profileFromContext(ctx) != "zz-loc" || SensitiveFromContext(ctx) != SensitiveCode {
		t.Fatalf("viewer yönlendirmesi: ok=%v profile=%q kind=%q", ok, profileFromContext(ctx), SensitiveFromContext(ctx))
	}
	// admin: kimliğe göre ilk → a-loc
	if ctx, _ := s.RouteSensitive(context.Background(), "admin", SensitiveWiki); profileFromContext(ctx) != "a-loc" {
		t.Fatalf("admin yönlendirmesi: %q", profileFromContext(ctx))
	}
	// Çözülen profil zaten yerelse o profil SABİTLENİR (yüzey haritası alt çağrıyı kaydırmasın).
	ctx, ok = s.RouteSensitive(WithProfile(context.Background(), "zz-loc"), "viewer", SensitiveCode)
	ctx = WithMeta(ctx, CallMeta{Surface: "chat-intent"})
	if !ok || !s.ProfileLocalOnly(ctx) {
		t.Fatal("sabitlenen yerel profil yüzey haritasıyla kaydı")
	}
	// İşaret birleşir.
	if k := SensitiveFromContext(WithSensitiveContent(ctx, SensitiveWiki)); k != "code+wiki" {
		t.Fatalf("birleşik işaret %q", k)
	}
	// Yerel profil yok → ctx aynen, ok=false.
	s.SetProfiles([]ModelProfile{{ID: "ext", Provider: ProviderOpenAI, BaseURL: "http://ext:8000/v1"}}, "ext", nil)
	base := context.Background()
	if ctx, ok := s.RouteSensitive(base, "admin", SensitiveCode); ok || ctx != base {
		t.Fatal("yerel profil yokken yönlendirdi")
	}
	if s.LocalOnlyAvailable(base, "admin") {
		t.Fatal("LocalOnlyAvailable yanlış")
	}
	if err := CheckLocalOnly("", ModelProfile{}); err != nil {
		t.Fatal("işaretsiz istek reddedildi")
	}
}

func TestSameProfileSeesLocalOnly(t *testing.T) {
	a := ModelProfile{ID: "x", Provider: ProviderOpenAI, BaseURL: "http://v/v1"}
	b := a
	b.LocalOnly = true
	if sameProfile(a, b) {
		t.Fatal("sameProfile LocalOnly farkını görmüyor (refresh tiki yutardı)")
	}
}
