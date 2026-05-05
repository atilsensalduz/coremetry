// Package copilot wraps the Anthropic Messages API to produce
// natural-language explanations of telemetry artifacts — trace flame,
// open Problem, exception group. Optional: when no API key is
// configured the package returns a clean "not configured" error and
// the UI hides its buttons.
//
// Why server-side?
//   - The Anthropic API key never leaves the operator's network.
//   - Heavy context (full trace span list, exception stack) lives in
//     ClickHouse already; building the prompt server-side avoids
//     shipping it to the browser just to ship it back.
//   - One central place to enforce token budgets, retries, redaction.
package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Service is the small surface other packages call into.
type Service struct {
	apiKey string
	model  string
	cli    *http.Client
}

// New returns a Service if an API key is configured, otherwise nil
// so callers can branch and the rest of Coremetry continues to work.
func New(apiKey, model string) *Service {
	if apiKey == "" {
		return nil
	}
	if model == "" {
		// Sonnet is the right default for this kind of analysis —
		// fast, cheap, plenty smart enough to summarize a trace.
		model = "claude-sonnet-4-6"
	}
	return &Service{
		apiKey: apiKey,
		model:  model,
		cli:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Configured reports whether the service has credentials. Used by
// /api/copilot/config so the UI can hide the buttons in deployments
// that haven't enabled it.
func (s *Service) Configured() bool { return s != nil && s.apiKey != "" }

// Explain runs a single Anthropic Messages call with the given system
// prompt + user prompt. Caller decides what to put in `userPrompt` —
// trace JSON, problem details, etc. Returns the assistant's plain-text
// reply.
func (s *Service) Explain(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	if s == nil || s.apiKey == "" {
		return "", errors.New("AI copilot not configured (set COREMETRY_AI_API_KEY)")
	}
	body := map[string]any{
		"model":      s.model,
		"max_tokens": 1024,
		"system":     systemPrompt,
		"messages": []map[string]any{
			{"role": "user", "content": userPrompt},
		},
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", s.apiKey)
	req.Header.Set("Anthropic-Version", "2023-06-01")

	resp, err := s.cli.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic call: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("anthropic %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("decode anthropic response: %w", err)
	}
	var out strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			out.WriteString(c.Text)
		}
	}
	return out.String(), nil
}

// ── Prompt helpers (pre-baked so handlers don't have to compose) ────────────

const systemTrace = `You are a senior SRE assistant inside an APM tool. Given a JSON
representation of a single distributed trace (a list of spans with
service, name, parent, duration, status), explain in 4-8 short bullet
points: (1) the user-facing operation this trace represents, (2) the
slowest span and what fraction of total time it consumed, (3) where
errors are concentrated if any, (4) the most plausible root cause hint
the operator should investigate next.

Be terse and concrete — the operator is reading this on a pager call.
No preamble, no headers — just the bullets.`

const systemProblem = `You are a senior SRE assistant inside an APM tool. The operator
just opened a Problem (an alert that fired). Given the rule + service +
metric value, explain in 3-5 short bullet points: (1) what the alert
actually means in plain language, (2) the most likely causes ranked
by probability for this metric, (3) the first three things the
operator should check.

Be terse — this lands on a pager call. No preamble.`

const systemException = `You are a senior SRE assistant inside an APM tool. Given a code
exception (type, message, stacktrace, service), explain in 3-5
bullets: (1) what the exception class typically means, (2) the most
likely cause given the call site shown in the stacktrace, (3) the
fix hint or first investigation step.

Be terse and direct — the operator is debugging in real time.`

// SystemPromptTrace returns the system prompt to use for trace
// explanations; exposed so callers can prefix domain context.
func SystemPromptTrace() string     { return systemTrace }
func SystemPromptProblem() string   { return systemProblem }
func SystemPromptException() string { return systemException }
