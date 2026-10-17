package api

// v0.10.1278 — operatör 2026-10-17: CoSRE karşılaması tüm hatalı span'leri
// sayıyordu ("Son 1 saatte 445.394 hata · 812 exception grubu"). Pinler:
// önbellek anahtarı ortamla değişir (hash-all-inputs), ''/all aynı evren,
// handler giriş-span okuyucusunu + ortam kapsamını kullanır, eski tüm-span
// okuması geri gelmez.

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestCosreGreetingKey_VariesWithEnvAndMinute(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	all, prod, uat := cosreGreetingKey("", now), cosreGreetingKey("prod", now), cosreGreetingKey("uat", now)
	if all == prod || prod == uat || all == uat {
		t.Fatalf("ortamlar anahtar paylaşmamalı: %s %s %s", all, prod, uat)
	}
	// now dakika başında (1_800_000_000 % 60 == 0): +10 sn aynı kova.
	if cosreGreetingKey("prod", now.Add(10*time.Second)) != prod {
		t.Error("aynı dakika aynı anahtar olmalı")
	}
	if cosreGreetingKey("prod", now.Add(time.Minute)) == prod {
		t.Error("dakika kovası anahtara girmeli")
	}
	if !strings.HasPrefix(prod, "cosre-greeting:v2:") {
		t.Errorf("v2 öneki (yanıt şekli değişti): %s", prod)
	}
}

func TestNormalizeGreetingEnv(t *testing.T) {
	for in, want := range map[string]string{"": "", "all": "", "ALL": "", "  prod ": "prod", "uat": "uat"} {
		if got := normalizeGreetingEnv(in); got != want {
			t.Errorf("%q → %q, beklenen %q", in, got, want)
		}
	}
	if got := normalizeGreetingEnv(strings.Repeat("x", 100)); len(got) != 64 {
		t.Errorf("tavan 64, %d", len(got))
	}
}

func TestCosreGreetingHandler_Wiring(t *testing.T) {
	b, err := os.ReadFile("cosre_greeting.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`r.URL.Query().Get("env")`,
		"cosreGreetingKey(env, now)",
		"s.store.EnvSummaryCovers(ctx, from)", // ortam MV'si geriye dolmaz
		"s.store.FleetFailedRequests(ctx, reqEnv, from, now)",
		"s.store.EnvMemberServices(ctx, env)", // Exceptions sayfasının env kuralı
		"s.store.RecentExceptionGroupCounts(ctx, from, services)",
		`json:"failedRequests1h"`, `json:"openExceptionGroups1h"`, `json:"newExceptionGroups1h"`,
		`json:"env"`, `json:"envApplied"`, `json:"exceptionsEnvApplied"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("handler %q içermiyor", want)
		}
	}
	for _, bad := range []string{"FleetErrorCount", "CountExceptionGroups", `json:"errors1h"`} {
		if strings.Contains(src, bad) {
			t.Errorf("eski tanım geri geldi: %q", bad)
		}
	}
}
