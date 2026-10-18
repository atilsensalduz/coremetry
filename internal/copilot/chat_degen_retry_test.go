package copilot

import (
	"context"
	"strings"
	"testing"

	aiprov "github.com/cilcenk/coremetry/internal/ai/provider"
)

// v0.10.1324 — operator-reported: gemma4 31b final cevapta bir önsöz cümlesini
// max_tokens (4096) dolana dek yineledi. Derin kipteki araçsız yeniden deneme
// ~700 token tavanla koşar (WithMaxTokensCap) ve kısa, sicilde kayıtlı bir ek
// taşır; tavan yalnız bu çağrıya iner.
func TestCappedMaxTokens(t *testing.T) {
	cases := []struct {
		name      string
		cap, base int
		want      int
	}{
		{"tavan yok", 0, 4096, 4096},
		{"tavan bütçeden küçük", 700, 4096, 700},
		{"bütçe tavandan küçük", 700, 512, 512},
		{"bütçe 0 (sağlayıcı varsayılanı)", 700, 0, 700},
		{"negatif tavan yok sayılır", -1, 4096, 4096},
	}
	for _, tc := range cases {
		ctx := WithMaxTokensCap(context.Background(), tc.cap)
		if got := cappedMaxTokens(ctx, tc.base); got != tc.want {
			t.Errorf("%s: %d, beklenen %d", tc.name, got, tc.want)
		}
		if got := chatRequest(ctx, aiprov.Request{MaxTokens: tc.base}, "s", nil, nil).MaxTokens; got != tc.want {
			t.Errorf("%s: chatRequest.MaxTokens %d, beklenen %d", tc.name, got, tc.want)
		}
	}
}

func TestChatDegenRetryAddendum(t *testing.T) {
	a := ChatDegenRetryAddendum()
	if !strings.Contains(a, "Yalnız kanıtı özetle; talimat metni yazma; en çok 8 satır.") {
		t.Fatalf("ek metni: %q", a)
	}
	if promptVersionRegistry["chatDegenRetryAddendum"] != a {
		t.Fatal("ek prompt sicilinde değil (ai_calls.prompt_version)")
	}
}
