package copilot

// prompt_exception_chain_test.go — v0.10.1379 — operator context: the
// exception explain lacked the downstream HTTP 500 that the trace explain
// (v0.10.1371) now shows. The exception input now carries the same
// server-computed HATA ZİNCİRİ block (internal/tracechain); this pins ONE
// rule in each exception prompt that mirrors the trace prompt's CAUSAL CHAIN
// rule: a failed downstream CLIENT/PRODUCER call beneath the throwing
// service makes the exception a consequence, with TWO next steps. Section
// headers are unchanged and the prompt-version registry carries the new text.

import (
	"strings"
	"testing"
)

func TestExceptionPromptsErrorChainRule(t *testing.T) {
	common := []string{
		"CAUSAL CHAIN OUTRANKS THE SYMPTOM",
		"HATA ZİNCİRİ block",
		"hop 1 is the error origin",
		"CLIENT/PRODUCER call that failed (HTTP ≥ 400, timeout, exception)",
		"is a CONSEQUENCE of",
		"naming the dependency's host and path verbatim from the block",
		"give TWO next steps",
		"investigate the dependency's failure",
		"the local defensive fix",
		"while the block shows a failed downstream call beneath it",
	}
	for name, tc := range map[string]struct {
		body     string
		sections []string
		extra    []string
	}{
		"systemExceptionBody": {systemExceptionBody,
			[]string{"**Hata ve Anlamı**", "**Yayılım ve Bağlam**", "**Şüpheli Değişiklik**", "**Kök Neden ve Sonraki Adım**"},
			[]string{"beneath the service that threw this exception group", "the exception is a CONSEQUENCE of the failed dependency call, not the root cause",
				"in **Kök Neden ve Sonraki Adım** give TWO next steps", "at the throwing frame"}},
		"systemOracleException": {systemOracleException,
			[]string{"**Hata ne demek**", "**Nerede yoğunlaşıyor**", "**Çağıran taraf (Coremetry trace)**", "**Patlama mı, sürekli akış mı**", "**Ne kontrol edilmeli**"},
			[]string{"When the Coremetry TRACE has errors", "the error row is a CONSEQUENCE of that failed dependency call",
				"**Ne kontrol edilmeli** give TWO next steps", "Never present the error code as the cause"}},
	} {
		flat := strings.Join(strings.Fields(tc.body), " ")
		for _, must := range append(common, tc.extra...) {
			if !strings.Contains(flat, must) {
				t.Errorf("%s lost the error-chain clause %q", name, must)
			}
		}
		if n := strings.Count(flat, "CAUSAL CHAIN OUTRANKS THE SYMPTOM"); n != 1 {
			t.Errorf("%s: chain rule must appear exactly once, got %d", name, n)
		}
		for _, sec := range tc.sections {
			if !strings.Contains(tc.body, sec) {
				t.Errorf("%s: section %q renamed or lost", name, sec)
			}
		}
		// The rule is a preamble rule: it precedes the section list.
		if i, j := strings.Index(flat, "CAUSAL CHAIN OUTRANKS THE SYMPTOM"), strings.Index(flat, tc.sections[0]+" —"); i < 0 || j < i {
			t.Errorf("%s: chain rule must precede the sections: rule=%d first-section=%d", name, i, j)
		}
		if promptVersionRegistry[name] != tc.body {
			t.Errorf("%s must be in promptVersionRegistry with its current text", name)
		}
	}
	// The code-variant prompt composes the same body.
	if !strings.Contains(SystemPromptExceptionWithCode(), "CAUSAL CHAIN OUTRANKS THE SYMPTOM") ||
		!strings.Contains(SystemPromptException(), "CAUSAL CHAIN OUTRANKS THE SYMPTOM") ||
		!strings.Contains(SystemPromptOracleException(), "CAUSAL CHAIN OUTRANKS THE SYMPTOM") {
		t.Error("exception prompt accessors lost the chain rule")
	}
}
