package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// anthropic.go — Anthropic Messages API gövdesi (buffered).
//
// FAZ 1.2 taşıması: copilot.go:explainAnthropicWithUsage'ın birebir
// aynısı. İki şey bilinçli olarak SABİT:
//
//   - Uç. Config.BaseURL bu yolda OKUNMAZ; Anthropic'in tek bir API
//     host'u var ve operatörün baseURL alanı openai-compat uçları için
//     var. Sessizce baseURL'e uymak, "openai" seçiliyken girilmiş bayat
//     bir uca anahtar göndermek demekti (Configure'un baseURL'i yalnız
//     openai için okuması aynı kararın öteki yarısı).
//   - JSON basamağı (v0.10.253, prompt audit D2): JSONSchema seviyesi
//     structured outputs ile karşılanır — output_config.format
//     {type:"json_schema", schema}. JSONObject seviyesi bu API'de yok;
//     düz çağrı yapılır (çağıran salvage zinciriyle yaşar). Tool-forcing
//     ya da assistant prefill KULLANILMAZ (4.6+'da prefill 400).
//   - Örnekleme parametresi YOK (v0.10.253, prompt audit D1): temperature
//     Opus 4.7+/4.8/5, Sonnet 5, Fable 5/5.1'de kaldırıldı (400).
//     Request.Temperature bu yolda yok sayılır; determinizm istenirse
//     Request.Effort (output_config.effort). Sonnet 4.6 kabul etse de
//     model adı değişince yol düşmesin diye hiç gönderilmez.
const (
	anthropicURL = "https://api.anthropic.com/v1/messages"
	// anthropicVersion — zorunlu sürüm header'ı. Eksikse API 400 döner;
	// yani bu sabit düşerse ANTHROPIC YOLU TAMAMEN kırılır.
	anthropicVersion = "2023-06-01"
	// DefaultAnthropicModel — TEK kaynak (v0.10.253, prompt audit D5):
	// stream.go, tools.go ve /api/settings/ai varsayılan etiketi buradan
	// okur (üç kopya v0.9.1120 sınıfı kayma). Varsayılanı yükseltmek ürün
	// kararı — burada yalnız tek-kaynak.
	DefaultAnthropicModel = "claude-sonnet-4-6"
)

// DoAnthropic tek bir buffered Messages çağrısı yapar.
//
// max_tokens ve temperature Request'ten gelir. Bu, v0.9.1120'nin
// düzelttiği hatanın taşınmış hâli: bu yol ~1000 sürüm boyunca sabit
// 1024 ve HİÇ temperature göndermişti (openai-compat 4096 alırken).
// Değerlerin sahibi Service; buranın işi onları gövdeye basmak.
func DoAnthropic(ctx context.Context, cfg Config, req Request) (Response, error) {
	if cfg.HTTPClient == nil {
		return Response{}, errors.New("provider: nil HTTPClient — timeout ve TLS-skip ayarları onun içinde yaşıyor")
	}
	model := req.Model
	if model == "" {
		model = cfg.Model
	}
	if model == "" {
		model = DefaultAnthropicModel
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	body := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"system":     req.System,
		"messages": []map[string]any{
			{"role": "user", "content": req.User},
		},
	}
	// temperature bilinçli olarak GÖNDERİLMEZ (dosya başlığı, D1).
	if req.JSONLevel >= JSONSchema && len(req.JSONSchema) > 0 {
		body["output_config"] = map[string]any{"format": map[string]any{
			"type": "json_schema", "schema": req.JSONSchema,
		}}
	}
	if e := strings.TrimSpace(req.Effort); e != "" {
		oc, _ := body["output_config"].(map[string]any)
		if oc == nil {
			oc = map[string]any{}
		}
		oc["effort"] = e
		body["output_config"] = oc
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicURL, bytes.NewReader(raw))
	if err != nil {
		return Response{}, err
	}
	applyHeaders(httpReq.Header, cfg.Headers)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", cfg.APIKey)
	httpReq.Header.Set("Anthropic-Version", anthropicVersion)

	resp, err := cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("anthropic call: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if resp.StatusCode >= 300 {
		return Response{}, &HTTPError{Provider: labelAnthropic, Status: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
	}
	return ParseAnthropic(respBody)
}

// ParseAnthropic, buffered bir Messages gövdesini çözer. Saf: ağ yok,
// durum yok. Streaming yolu da bunu çağırıyor — bir vekil stream:true
// bayrağını yutup tek-atış JSON döndürdüğünde, elde OLAN gövdeyi
// çözümlemek ikinci bir faturalı çağrıdan iyidir (v0.8.404).
//
// content[] birden çok text bloğu taşıyabilir (Anthropic uzun yanıtı
// bölebiliyor); tool_use gibi blokların metni cevaba girmez.
//
// v0.10.1308 — nihai metin ParseOpenAIChat ile AYNI kurtarma zincirinden
// (SalvageAnswer → StripThinking → thinkStripper) geçer. Eskiden text
// blokları ham birleştiriliyordu: Gemma'yı Messages şekliyle sunan bir
// vekilin `<think>…</think>` / `<|channel>thought…<channel|>` bloğu nihai
// cevapta kalıyordu (canlı delta'lar 1304'ten beri temizdi, `answer`
// çerçevesi değil). Sıra openai ile birebir:
//  1. text bloklarının görünür kısmı (düşünce soyulmuş, trim'li);
//  2. boşsa thinking bloklarının metni — İŞARETLİ (reasoning_content'in
//     karşılığı; akış yolunda thinking_delta aynı yere gider);
//  3. son çare: text içindeki ilk düşünce bloğunun İÇİ — İŞARETLİ.
func ParseAnthropic(respBody []byte) (Response, error) {
	var parsed struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"` // type=thinking bloğu
		} `json:"content"`
		StopReason string         `json:"stop_reason"`
		Usage      anthropicUsage `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return Response{}, fmt.Errorf("decode anthropic response: %w", err)
	}
	// v0.10.253 (prompt audit D3): Fable/Opus 5 reddi HTTP 200 + boş
	// metin + stop_reason=refusal döner. Boş panel değil, açık hata —
	// çağıran salvage/yeniden deneme yerine operatöre söyler.
	if parsed.StopReason == "refusal" {
		return Response{InputTokens: parsed.Usage.totalInput(), OutputTokens: parsed.Usage.OutputTokens, CachedTokens: parsed.Usage.CacheReadInputTokens},
			errAnthropicRefusal
	}
	var text, thinking strings.Builder
	for _, c := range parsed.Content {
		switch c.Type {
		case "text":
			text.WriteString(c.Text)
		case "thinking":
			thinking.WriteString(c.Thinking)
		}
	}
	// Not: kurtarılacak bir şey yoksa boş metin burada HATA DEĞİL —
	// openai-compat'ın EmptyAnswerError teşhisinin anthropic karşılığı yok
	// ve olmadı. Bu, taşınan davranış; değiştirmek ayrı bir ürün kararı
	// olurdu (v0.10.1308 yalnız düşünce soymayı hizaladı).
	return Response{
		Text:         SalvageAnswer(text.String(), thinking.String(), ""),
		InputTokens:  parsed.Usage.totalInput(),
		OutputTokens: parsed.Usage.OutputTokens,
		CachedTokens: parsed.Usage.CacheReadInputTokens, // v0.10.807
	}, nil
}

// errAnthropicRefusal — stop_reason=refusal'ın TEK metni (buffered, akış ve
// araç yolu aynı cümleyi döndürür; ClassifyAIErrorText "reddetti"ye bakar).
var errAnthropicRefusal = errors.New("anthropic: model isteği reddetti (stop_reason=refusal)")

// anthropicUsage — Messages usage bloğu. input_tokens YALNIZ son kesme
// noktasından sonraki kuyruktur; önbellekten okunan ve önbelleğe yazılan
// kısım ayrı alanlarda gelir. openai prompt_tokens (önbellekli dâhil TOPLAM
// giriş) ile aynı anlam için üçü toplanır — yoksa önbellek açıldığında /ai
// prompt boyunu eksik, önbellek yüzdesini şişik gösterir.
type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"` // v0.10.807
}

func (u anthropicUsage) totalInput() int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}
