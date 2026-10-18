package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// tools_stream.go — v0.10.1273 (CoSRE adım 4): tool'lu turun AKAN ikizi.
//
// ChatOpenAITools / ChatAnthropicTools'un stream:true karşılığı. Sözleşme:
//
//   - Dönen ChatResponse, aynı turun BUFFERED yanıtıyla birebir aynıdır:
//     akış biriktirilir, sonunda buffered gövde şeklinde SENTEZLENİR ve
//     buffered çözümleyiciden (parseOpenAIToolsChat / parseAnthropicToolsChat)
//     geçer. Düşünce soyma, kurtarma zinciri, metin-gömülü çağrı geri
//     düşüşü, Gemini ham tool_call tekrarı, Anthropic imzalı thinking
//     blokları — hepsi TEK yazılıştan. İkinci bir çözümleyici yok.
//   - Metin parçaları toolTurnGate'ten geçerek onDelta'ya akar (bkz.
//     tools_stream_gate.go): buffered yolun gizleyeceği hiçbir şey akmaz;
//     tool-call turları metin yayınlamaz.
//   - Akış kurulamazsa *StreamFallbackError (StreamOpenAI ile aynı) —
//     geri düşme KARARI copilot.Service'indir. VerdictParseBuffered'da
//     gövde ParseOpenAIToolsOneShot / ParseAnthropicToolsOneShot ile
//     ikinci faturalı çağrı yapılmadan çözülür.
//   - ctx iptali (istemci koptu, alışveriş tavanı) HTTP isteğini keser:
//     gövde okuması hata döner, akış durur.
//   - v0.10.1324 (operatör-bildirimi) — YİNELEME MUHAFIZI (degen_guard.go):
//     yayınlanan metin aynı satırı döngüyle yazmaya başlarsa kapı yayını
//     keser ve üst akışı KENDİ iptal eder (ctx WithCancel). Bu iptal hata
//     DEĞİLDİR: tur biriktiriciden biter, Text = temiz önek, Degenerated=true,
//     tool çağrısı yok sayılır. Usage parçası gelmediği için o turun token
//     sayıları eksik kalabilir.

// ParseOpenAIToolsOneShot — stream:true isteğine tek atış (SSE olmayan)
// cevap gelmişse gövdeyi buffered çözümleyiciyle çözer.
func ParseOpenAIToolsOneShot(body []byte, req ChatRequest) (ChatResponse, error) {
	return parseOpenAIToolsChat(body, labelOpenAI, offeredTextCalls(req))
}

// ParseAnthropicToolsOneShot — ParseOpenAIToolsOneShot'un Anthropic ikizi.
func ParseAnthropicToolsOneShot(body []byte) (ChatResponse, error) {
	return parseAnthropicToolsChat(body)
}

// doStreamPost — stream:true isteğini yollar ve yanıt BAŞINI sınıflandırır.
// Akış değilse *StreamFallbackError (gövde tavanlı okunmuş, yanıt kapalı).
func doStreamPost(ctx context.Context, cfg Config, url string, hdrs map[string]string, body map[string]any, label string) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	applyHeaders(httpReq.Header, cfg.Headers)
	for k, v := range hdrs {
		httpReq.Header.Set(k, v)
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, &StreamFallbackError{Verdict: VerdictFallbackOnce, Stage: StageConnect, Provider: label, Err: err}
	}
	ct := resp.Header.Get("Content-Type")
	if v := ClassifyStreamResponse(resp.StatusCode, ct); v != VerdictStream {
		limit := int64(4096)
		if v == VerdictParseBuffered {
			limit = maxRespBytes
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
		resp.Body.Close()
		return nil, &StreamFallbackError{
			Verdict: v, Stage: StageHead, Provider: label,
			Status: resp.StatusCode, ContentType: ct, Body: respBody,
		}
	}
	return resp, nil
}

// ─── OpenAI-compat ──────────────────────────────────────────────────

// StreamOpenAITools — tek bir tool'lu openai-compat turu, stream:true.
func StreamOpenAITools(ctx context.Context, cfg Config, req ChatRequest, onDelta func(TextDelta)) (ChatResponse, error) {
	if cfg.HTTPClient == nil {
		return ChatResponse{}, errors.New("provider: nil HTTPClient — timeout ve TLS-skip ayarları onun içinde yaşıyor")
	}
	url, hdrs, toolChoiceNone := openAIToolsEndpoint(cfg)
	body := buildOpenAIToolsBody(cfg, req, defaultModel, toolChoiceNone)
	body["stream"] = true
	body["stream_options"] = map[string]any{"include_usage": true}
	ctx, cancel := context.WithCancel(ctx) // v0.10.1324 — yineleme muhafızı üst akışı keser
	defer cancel()
	resp, err := doStreamPost(ctx, cfg, url, hdrs, body, labelOpenAI)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()

	offered := offeredTextCalls(req)
	gate := newToolTurnGate(onDelta, !req.NoToolCalls && len(req.Tools) > 0, offered)
	gate.onDegenerate = cancel
	acc := &openAIToolsStreamAccum{}
	scanErr := scanSSE(resp.Body, func(line string) { acc.feed(line, gate) })
	if !acc.sawData {
		return ChatResponse{}, &StreamFallbackError{
			Verdict: VerdictFallbackOnce, Stage: StageEmptyStream, Provider: labelOpenAI, Err: scanErr,
		}
	}
	usage := ChatResponse{InputTokens: acc.inTokens, OutputTokens: acc.outTokens, CachedTokens: acc.cached}
	if gate.Degenerated() {
		return degeneratedResponse(usage, gate), nil
	}
	if scanErr != nil {
		// Veri aktıktan sonra kopma (iptal dâhil) — geri düşüş yok.
		return usage, fmt.Errorf("openai-compat stream read: %w", scanErr)
	}
	out, err := parseOpenAIToolsChat(acc.synthesize(), labelOpenAI, offered)
	if err != nil {
		return out, err
	}
	gate.Finish(out.Text, len(out.ToolCalls) > 0)
	return out, nil
}

// degeneratedResponse — v0.10.1324: muhafızın kestiği tur. Kendi iptalimizin
// okuma hatası yutulur; metin temiz önek, tool çağrısı yok (kesilen turun
// yarım argümanı yürütülmez), RawContent yok (final tur geri beslenmez).
func degeneratedResponse(usage ChatResponse, gate *toolTurnGate) ChatResponse {
	usage.Text = gate.CleanText()
	usage.Degenerated = true
	gate.Finish(usage.Text, false)
	return usage
}

// openAIToolsStreamAccum — content / reasoning / tool_calls parçalarını
// biriktirir; sonunda buffered gövdeyi sentezler.
type openAIToolsStreamAccum struct {
	content   strings.Builder
	reasoning strings.Builder // reasoning_content
	reasonAlt strings.Builder // reasoning (v0.8.384 geçit şekli)
	calls     []*streamToolCall
	sawData   bool
	sawChoice bool
	inTokens  int
	outTokens int
	cached    int
}

type streamToolCall struct {
	index  int
	id     string
	typ    string
	name   string
	args   strings.Builder
	extras map[string]json.RawMessage // tool_call düzeyindeki ek alanlar (Gemini extra_content)
}

func (a *openAIToolsStreamAccum) feed(line string, gate *toolTurnGate) {
	payload, ok := sseDataPayload(line)
	if !ok || payload == "[DONE]" {
		return
	}
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content          string            `json:"content"`
				ReasoningContent string            `json:"reasoning_content"`
				Reasoning        string            `json:"reasoning"`
				ToolCalls        []json.RawMessage `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens        int                `json:"prompt_tokens"`
			CompletionTokens    int                `json:"completion_tokens"`
			PromptTokensDetails promptTokensDetail `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return // bozuk satır — atla (StreamOpenAI ile aynı duruş)
	}
	a.sawData = true
	if u := chunk.Usage; u != nil {
		if u.PromptTokens > 0 {
			a.inTokens = u.PromptTokens
		}
		if u.CompletionTokens > 0 {
			a.outTokens = u.CompletionTokens
		}
		if u.PromptTokensDetails.CachedTokens > 0 {
			a.cached = u.PromptTokensDetails.CachedTokens
		}
	}
	if len(chunk.Choices) == 0 {
		return
	}
	a.sawChoice = true
	d := chunk.Choices[0].Delta
	a.reasoning.WriteString(d.ReasoningContent)
	a.reasonAlt.WriteString(d.Reasoning)
	for _, raw := range d.ToolCalls {
		a.feedToolCall(raw)
		gate.ToolCall()
	}
	if d.Content != "" {
		a.content.WriteString(d.Content)
		gate.Content(d.Content)
	}
}

// feedToolCall — bir tool_calls delta öğesi. index varsa ona eklenir; yoksa
// (bazı sunucular çağrıyı tek parçada, indekssiz yollar) yeni kimlik yeni
// çağrı, kimliksiz parça son çağrının devamıdır.
func (a *openAIToolsStreamAccum) feedToolCall(raw json.RawMessage) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return
	}
	var tc struct {
		Index    *int   `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &tc) != nil {
		return
	}
	var cur *streamToolCall
	switch {
	case tc.Index != nil:
		for _, c := range a.calls {
			if c.index == *tc.Index {
				cur = c
			}
		}
		if cur == nil {
			cur = &streamToolCall{index: *tc.Index}
			a.calls = append(a.calls, cur)
		}
	case tc.ID != "" || len(a.calls) == 0:
		for _, c := range a.calls {
			if tc.ID != "" && c.id == tc.ID {
				cur = c
			}
		}
		if cur == nil {
			cur = &streamToolCall{index: len(a.calls)}
			a.calls = append(a.calls, cur)
		}
	default:
		cur = a.calls[len(a.calls)-1]
	}
	if tc.ID != "" {
		cur.id = tc.ID
	}
	if tc.Type != "" {
		cur.typ = tc.Type
	}
	if tc.Function.Name != "" {
		cur.name = tc.Function.Name
	}
	if args := tc.Function.Arguments; len(args) > 0 && string(args) != "null" {
		var s string
		if json.Unmarshal(args, &s) == nil {
			cur.args.WriteString(s) // parça parça gelen JSON metni
		} else {
			cur.args.Write(args) // bazı sunucular nesne yollar
		}
	}
	for k, v := range fields {
		switch k {
		case "index", "id", "type", "function":
			continue
		}
		if cur.extras == nil {
			cur.extras = map[string]json.RawMessage{}
		}
		cur.extras[k] = v
	}
}

// synthesize — biriktirileni buffered chat.completion gövdesine çevirir.
func (a *openAIToolsStreamAccum) synthesize() []byte {
	type usageT struct {
		PromptTokens        int                `json:"prompt_tokens"`
		CompletionTokens    int                `json:"completion_tokens"`
		PromptTokensDetails promptTokensDetail `json:"prompt_tokens_details"`
	}
	body := map[string]any{
		"usage": usageT{a.inTokens, a.outTokens, promptTokensDetail{CachedTokens: a.cached}},
	}
	if !a.sawChoice {
		body["choices"] = []any{}
	} else {
		sort.SliceStable(a.calls, func(i, j int) bool { return a.calls[i].index < a.calls[j].index })
		calls := make([]any, 0, len(a.calls))
		for _, c := range a.calls {
			typ := c.typ
			if typ == "" {
				typ = "function"
			}
			m := map[string]any{
				"type":     typ,
				"function": map[string]any{"name": c.name, "arguments": c.args.String()},
			}
			if c.id != "" {
				m["id"] = c.id
			}
			for k, v := range c.extras {
				m[k] = v
			}
			calls = append(calls, m)
		}
		msg := map[string]any{
			"content":           a.content.String(),
			"reasoning_content": a.reasoning.String(),
			"reasoning":         a.reasonAlt.String(),
		}
		if len(calls) > 0 {
			msg["tool_calls"] = calls
		}
		body["choices"] = []any{map[string]any{"message": msg}}
	}
	raw, _ := json.Marshal(body)
	return raw
}

// ─── Anthropic ──────────────────────────────────────────────────────

// StreamAnthropicTools — tek bir tool'lu Messages turu, stream:true.
func StreamAnthropicTools(ctx context.Context, cfg Config, req ChatRequest, onDelta func(TextDelta)) (ChatResponse, error) {
	if cfg.HTTPClient == nil {
		return ChatResponse{}, errors.New("provider: nil HTTPClient — timeout ve TLS-skip ayarları onun içinde yaşıyor")
	}
	body := buildAnthropicToolsBody(cfg, req)
	body["stream"] = true
	ctx, cancel := context.WithCancel(ctx) // v0.10.1324 — yineleme muhafızı üst akışı keser
	defer cancel()
	resp, err := doStreamPost(ctx, cfg, anthropicURL, map[string]string{
		"Content-Type":      "application/json",
		"X-API-Key":         cfg.APIKey,
		"Anthropic-Version": anthropicVersion,
	}, body, labelAnthropic)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()

	// Anthropic yolunda metin-gömülü çağrı ayrıştırması yok (buffered da yapmıyor).
	gate := newToolTurnGate(onDelta, !req.NoToolCalls && len(req.Tools) > 0, nil)
	gate.onDegenerate = cancel
	acc := &anthropicToolsStreamAccum{}
	scanErr := scanSSE(resp.Body, func(line string) { acc.feed(line, gate) })
	if !acc.sawData {
		return ChatResponse{}, &StreamFallbackError{
			Verdict: VerdictFallbackOnce, Stage: StageEmptyStream, Provider: labelAnthropic, Err: scanErr,
		}
	}
	usage := ChatResponse{InputTokens: acc.usage.totalInput(), OutputTokens: acc.usage.OutputTokens,
		CachedTokens: acc.usage.CacheReadInputTokens}
	if gate.Degenerated() {
		return degeneratedResponse(usage, gate), nil
	}
	if scanErr != nil {
		return usage, fmt.Errorf("anthropic stream read: %w", scanErr)
	}
	if acc.errMsg != "" {
		return usage, fmt.Errorf("anthropic stream error: %s", acc.errMsg)
	}
	out, err := parseAnthropicToolsChat(acc.synthesize())
	if err != nil {
		return out, err
	}
	gate.Finish(out.Text, len(out.ToolCalls) > 0)
	return out, nil
}

type anthropicStreamBlock struct {
	start     map[string]json.RawMessage // content_block_start.content_block
	text      strings.Builder
	thinking  strings.Builder
	signature strings.Builder
	input     strings.Builder // input_json_delta.partial_json
}

type anthropicToolsStreamAccum struct {
	blocks  map[int]*anthropicStreamBlock
	order   []int
	usage   anthropicUsage
	stop    string
	errMsg  string
	sawData bool
}

func (a *anthropicToolsStreamAccum) block(i int) *anthropicStreamBlock {
	if a.blocks == nil {
		a.blocks = map[int]*anthropicStreamBlock{}
	}
	b := a.blocks[i]
	if b == nil {
		b = &anthropicStreamBlock{}
		a.blocks[i] = b
		a.order = append(a.order, i)
	}
	return b
}

func (a *anthropicToolsStreamAccum) feed(line string, gate *toolTurnGate) {
	payload, ok := sseDataPayload(line)
	if !ok {
		return
	}
	var ev struct {
		Type         string                     `json:"type"`
		Index        int                        `json:"index"`
		ContentBlock map[string]json.RawMessage `json:"content_block"`
		Message      *struct {
			Usage anthropicUsage `json:"usage"`
		} `json:"message"`
		Delta *struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			Signature   string `json:"signature"`
			PartialJSON string `json:"partial_json"`
			StopReason  string `json:"stop_reason"`
		} `json:"delta"`
		Usage *struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(payload), &ev) != nil {
		return
	}
	a.sawData = true
	switch ev.Type {
	case "message_start":
		if ev.Message != nil {
			a.usage = ev.Message.Usage
		}
	case "content_block_start":
		b := a.block(ev.Index)
		b.start = ev.ContentBlock
		var typ string
		_ = json.Unmarshal(ev.ContentBlock["type"], &typ)
		if typ == "tool_use" {
			gate.ToolCall()
		}
	case "content_block_delta":
		if ev.Delta == nil {
			return
		}
		b := a.block(ev.Index)
		switch ev.Delta.Type {
		case "text_delta":
			b.text.WriteString(ev.Delta.Text)
			gate.Content(ev.Delta.Text)
		case "thinking_delta":
			b.thinking.WriteString(ev.Delta.Thinking)
		case "signature_delta":
			b.signature.WriteString(ev.Delta.Signature)
		case "input_json_delta":
			b.input.WriteString(ev.Delta.PartialJSON)
		}
	case "message_delta":
		if ev.Usage != nil && ev.Usage.OutputTokens > 0 {
			a.usage.OutputTokens = ev.Usage.OutputTokens
		}
		if ev.Delta != nil && ev.Delta.StopReason != "" {
			a.stop = ev.Delta.StopReason
		}
	case "error":
		if ev.Error != nil {
			a.errMsg = ev.Error.Message
		}
	}
}

// synthesize — blokları buffered Messages gövdesine çevirir. Her blok
// content_block_start'taki HAM nesneden kurulur (bilinmeyen alanlar —
// redacted_thinking.data, citations — korunur); akan alanlar üstüne yazılır.
func (a *anthropicToolsStreamAccum) synthesize() []byte {
	sort.Ints(a.order)
	content := make([]map[string]json.RawMessage, 0, len(a.order))
	for _, i := range a.order {
		b := a.blocks[i]
		m := map[string]json.RawMessage{}
		for k, v := range b.start {
			m[k] = v
		}
		var typ string
		_ = json.Unmarshal(m["type"], &typ)
		switch typ {
		case "text":
			m["text"] = mustJSON(b.text.String())
		case "thinking":
			m["thinking"] = mustJSON(b.thinking.String())
			if b.signature.Len() > 0 {
				m["signature"] = mustJSON(b.signature.String())
			}
		case "tool_use":
			in := strings.TrimSpace(b.input.String())
			if in == "" || !json.Valid([]byte(in)) {
				in = "{}"
			}
			m["input"] = json.RawMessage(in)
		}
		content = append(content, m)
	}
	raw, _ := json.Marshal(map[string]any{
		"content": content, "stop_reason": a.stop, "usage": a.usage,
	})
	return raw
}

func mustJSON(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
