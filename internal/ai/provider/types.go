// Package provider is the single LLM transport for Coremetry.
//
// FAZ 1.1 (AI Assistant tasarımı, docs/plans/ai-assistant-design-2026-08-16.md
// §2.1 / §4-Faz1). Bugün 8 ayrı istek üreticisi var (copilot.go ×3,
// chat.go ×2, stream.go ×2, rag.go embed) ve her biri gövde şeklini,
// header'ları ve salvage zincirini KENDİ kopyasıyla taşıyor. Bunun
// bedeli ölçüldü: 1024 max_tokens paritesi ~1000 sürüm boyunca
// anthropic/github yollarında eksik kaldı (v0.9.1120). Tek transport
// o sınıfı kapatır.
//
// FAZ 1.2 — kapsam ÜÇ sağlayıcının BUFFERED explain yolunun tamamı:
// DoOpenAI (JSON merdiveni dahil) + DoAnthropic + DoGitHub. Eski
// buffered üreticiler (explain*WithUsage) silindi; artık tek yazılış
// var. Stream (stream:true) ve tool-calling (chat.go) üreticileri
// bilinçli olarak DIŞARIDA — onlar sonraki dilimde taşınır; bu dilimde
// yalnız salvage/parse zincirini buradan çağırırlar, yani kurtarma
// mantığı da tek yerde.
//
// Tasarım kuralı: provider DURUM TUTMAZ. Config bir SNAPSHOT olarak
// dışarıdan gelir; canlı yapılandırmanın (kilit, 30s refresh, TLS-skip,
// timeout, GitHub oturum jetonu, JSON-yetenek kararları) sahibi
// copilot.Service'tir ve öyle kalır. Merdiven de öyle: hangi basamağın
// DENENECEĞİ ve reddedilince ne yapılacağı Service'in kararı, gövdeye
// hangi response_format'ın basılacağı buranın işi.
package provider

import (
	"fmt"
	"net/http"
	"strings"
)

// JSON zorlama basamakları. DoOpenAI üçünü de basar; hangi basamağın
// deneneceğine ve reddedilince bir alta inileceğine copilot.Service
// karar verir (yetenek kararları uç-başına önbellekli, yani DURUM).
//
// Basamak sınırın DIŞINDAysa ya da JSONSchema şemasız gelirse DoOpenAI
// açık hata döndürür: sessizce kısıtsız çağrıya düşmek, isteyen yüzeyin
// garantisini haber vermeden kaybettirirdi (fail-open-silently-unapplies
// sınıfı).
const (
	JSONPlain  = 0 // response_format yok
	JSONObject = 1 // {"type":"json_object"}
	JSONSchema = 2 // {"type":"json_schema", …}
)

// Request — bir LLM çağrısının tel-üstü girdileri.
//
// Model burada da, Config'te de var: Config kimlik/uç bilgisidir
// (baseURL+key+varsayılan model), Request ise O çağrının modelidir.
// Boşsa Config.Model, o da boşsa sağlayıcı varsayılanı kullanılır.
type Request struct {
	Model     string
	MaxTokens int
	// Temperature nil = gövdeye HİÇ koyma. copilot.Service bugün
	// daima bir değer gönderiyor (tuneTemperature ok=true); nil hâli,
	// bazı reasoning uçlarının temperature'ı reddettiği gelecekteki
	// "hiç gönderme" durumu için ifade edilebilir tutuluyor.
	Temperature *float64
	System      string
	User        string
	// JSONLevel — JSONPlain | JSONObject | JSONSchema. Yalnız
	// openai-compat yolunda uygulanır: anthropic ve github uçlarına
	// response_format bugüne kadar hiç gönderilmedi ve bu dilim
	// DAVRANIŞ TAŞIYOR, değiştirmiyor. O iki taşıyıcı JSONPlain
	// dışındaki basamağı açık hatayla reddeder ki "uyguladım" sanılmasın.
	JSONLevel int
	// JSONSchemaName / JSONSchema — yalnız JSONLevel==JSONSchema'da
	// okunur ve İKİSİ de zorunludur. Boş şemayla json_schema göndermek
	// 400 alır ve o uç için GEREKSİZ bir yetenek kararı yazdırırdı
	// (v0.9.527); Service şemasız isteği zaten bir alt basamağa indirir,
	// buradaki denetim o sözleşmenin ikinci kilidi.
	JSONSchemaName string
	JSONSchema     map[string]any
	// ExtraBody — v0.10.534 (modelcaps): openai-uyumlu gövdeye eklenen ek
	// alanlar (ör. Qwen3 `chat_template_kwargs.enable_thinking`). Çekirdek
	// anahtarları (model/max_tokens/messages/stream/…) EZEMEZ; anthropic ve
	// github gövdeleri yok sayar. nil = gövde değişmez.
	ExtraBody map[string]any
	// Effort — v0.10.253 (prompt audit D3): Anthropic 4.6+ ailesinde
	// output_config.effort ("low" | "medium" | "high" | "max"). Boş =
	// gönderilmez (sağlayıcı varsayılanı). Derinlik prompt'la değil
	// bununla ayarlanır; openai-compat yolu yok sayar.
	Effort string
	// StrictTools — v0.10.1456 (C1, opsiyonel guided decoding): profilin
	// StrictToolArgs anahtarı. Yalnız openai-uyumlu TOOL gövdesi okur
	// (tools[].function.strict=true + additionalProperties:false); explain
	// gövdeleri ve Anthropic yolu yok sayar. false = gövde bayt bayt aynı.
	StrictTools bool
}

// Response — çözümlenmiş yanıt. Token sayıları `usage` alanından
// gelir; bazı yerel uçlar (eski Ollama, vLLM) usage yollamaz ve
// 0 kalır — kaydedici satırı yine yazar (gecikme + statü değerli).
type Response struct {
	Text         string
	InputTokens  int
	OutputTokens int
	// CachedTokens — v0.10.807 (dış skill denetimi L2): önek önbelleğinden
	// gelen giriş token'ı. OpenAI/vLLM/llama.cpp `usage.prompt_tokens_details.
	// cached_tokens`, Anthropic `usage.cache_read_input_tokens`. Yollamayan uç
	// (Ollama) 0 bırakır — 0 "önbellek yok" DEĞİL "ölçülmedi" demektir.
	CachedTokens int
}

// Config — çağrı anındaki yapılandırma SNAPSHOT'ı.
//
// HTTPClient zorunlu: timeout (operatör-ayarlı, v0.9.1120) ve
// TLS-skip transport'u onun içinde yaşıyor. Nil'e sessizce
// http.DefaultClient koymak, 180s local-LLM timeout'unu ve
// kurumsal-CA muafiyetini haber vermeden düşürürdü.
type Config struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
	// Headers — v0.10.1238: profilin ek HTTP başlıkları (ör. geçidin
	// yönlendirdiği `x-cluster: test-v2`). Her istekte çekirdek
	// başlıklardan ÖNCE basılır, yani auth/sürüm başlıklarını ezemez;
	// ayrılmış adları ValidateHeaders zaten reddeder. nil = değişmez.
	Headers map[string]string
}

// MaxHeaders — profil başına ek başlık tavanı.
const MaxHeaders = 20

// reservedHeaders — taşıyıcının kendi bastığı ya da net/http'nin yönettiği
// adlar (kanonik biçim). Ek başlık bunları taşıyamaz: Authorization'ı ezmek
// auth'u sessizce kırar, Content-Length/Host'u ezmek isteği bozar.
var reservedHeaders = map[string]bool{
	"Authorization": true, "Api-Key": true, "X-Api-Key": true, "Anthropic-Version": true,
	"Content-Type": true, "Content-Length": true, "Accept": true, "Host": true,
	"Transfer-Encoding": true, "Connection": true,
}

// ValidateHeaders — SAF: ad RFC 7230 token'ı, ayrılmış değil, tekrarsız
// (büyük/küçük harf duyarsız); değer CR/LF/NUL içermez (başlık enjeksiyonu).
func ValidateHeaders(h map[string]string) error {
	if len(h) > MaxHeaders {
		return fmt.Errorf("en fazla %d ek başlık", MaxHeaders)
	}
	seen := map[string]bool{}
	for k, v := range h {
		if k == "" || len(k) > 100 || strings.IndexFunc(k, notTokenRune) >= 0 {
			return fmt.Errorf("geçersiz başlık adı %q", k)
		}
		c := http.CanonicalHeaderKey(k)
		if reservedHeaders[c] {
			return fmt.Errorf("%s başlığı ayrılmış; ek başlık olarak verilemez", c)
		}
		if seen[c] {
			return fmt.Errorf("başlık tekrar ediyor (%s)", c)
		}
		seen[c] = true
		if len(v) > 1024 || strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("%s başlığının değeri geçersiz (≤1024, satır sonu yok)", c)
		}
	}
	return nil
}

func notTokenRune(r rune) bool {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return false
	}
	return !strings.ContainsRune("!#$%&'*+-.^_`|~", r)
}

// applyHeaders — Config.Headers'ı isteğe basar. Çağrı yerleri bunu çekirdek
// Header.Set'lerden ÖNCE yapar: çakışmada çekirdek kazanır (headers_test.go).
func applyHeaders(h http.Header, extra map[string]string) {
	for k, v := range extra {
		h.Set(k, v)
	}
}

// applyExtraBody — Request.ExtraBody'yi gövdeye ekler; var olan (çekirdek)
// anahtarlar korunur. Üç openai-uyumlu gövde (buffered/stream/tools) bunu
// çağırır (extra_body_test.go pinler).
func applyExtraBody(body map[string]any, extra map[string]any) {
	for k, v := range extra {
		if _, taken := body[k]; !taken {
			body[k] = v
		}
	}
}
