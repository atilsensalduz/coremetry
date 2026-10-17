package copilot

// v0.10.1299 — operatör 2026-10-17: 5 dk p99 gürültüsü yanlış bulgu üretiyordu.
// MV p99 kova boyutuyla anılır; "sıçrama" yalnız ham span doğrulamasıyla;
// doğrulanmamış fark "olası". Kural metni golden (bilinçsiz değişmesin) ve
// üç anlatım prompt'unda AYNEN.

import (
	"strings"
	"testing"
)

func TestLatencyEvidenceRuleGolden(t *testing.T) {
	const want = `- MV p99 sayısını kova boyutuyla söyle ("5 dk kova p99'u", "1 dk kova"); "sıçrama" ya da "spike" yalnız kanıtta ham span doğrulaması ("ham span: N istek > T ms") varsa yazılır; doğrulanmamış ya da "olası" diye işaretli p99 farkını "olası" diye yaz, kesin bulgu gibi sunma.`
	if latencyEvidenceRule != want {
		t.Fatalf("latencyEvidenceRule değişti:\n got %q\nwant %q", latencyEvidenceRule, want)
	}
	for name, p := range map[string]string{
		"guided":    SystemPromptGuidedChat(),
		"agentLoop": SystemPromptChatAgentLoop(),
		"sre":       SystemPromptSREInvestigation(),
	} {
		if !strings.Contains(p, want) {
			t.Errorf("%s prompt'u gecikme kanıt kuralını taşımıyor", name)
		}
	}
}
