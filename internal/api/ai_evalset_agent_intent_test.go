package api

// ai_evalset_agent_intent_test.go — v0.10.1321 (operatör; DECISIONS
// "v0.10.1321 — Ajan eval vakalarında intent katmanı kapalı"). Ajan kipi
// evalset vakasında niyet kademesi (copilotChatIntent) koşmaz: prod
// varsayılanı on_no_loop'ta guided'ın eşlemediği düz soru niyet kademesinde
// (sınıflandırıcı + genel cevap) biter ve vaka "missing tool" ile kalırdı.
//
// Pinlenenler: aynı soru üretimde (işaret nil) niyet kademesinde biter, ajan
// vakasında döngüye ulaşır ve sınıflandırıcı hiç çağrılmaz; expectTier
// "intent" sabitleyen vaka kademeyi açık tutar; SAF karar tablosu + nil
// işaretin kararı değiştirmemesi.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// intentLoopScript — sınıflandırıcı (response_format) eşleyemez; araçsız
// çağrı (on_no_loop genel cevabı) düz metin; araçlı ilk döngü turu
// compare_periods ister, araç sonucundan sonra cevap.
func intentLoopScript(_ int, body map[string]any) map[string]any {
	if _, ok := body["response_format"]; ok {
		return answerMsg(`{"intent":"none","service":"","env":"","rangeS":0}`)
	}
	if _, ok := body["tools"]; !ok {
		return answerMsg("Genel bilgiyle cevap: telemetriyle eşleşmedi.")
	}
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		if mm, _ := m.(map[string]any); mm["role"] == "tool" {
			return answerMsg("checkout p95 1830 ms; dünden yüksek.")
		}
	}
	return toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
}

func countIntentCalls(calls []map[string]any) (classifier, withTools int) {
	for _, b := range calls {
		if _, ok := b["response_format"]; ok {
			classifier++
		}
		if _, ok := b["tools"]; ok {
			withTools++
		}
	}
	return classifier, withTools
}

// TestEvalAgentSkipsIntentTier — on_no_loop (prod varsayılanı): üretimde soru
// niyet kademesinde biter (araç yok, döngü yok); aynı soru ajan vakasında
// sınıflandırıcıyı hiç çağırmadan döngüye ulaşır ve araç beklentisi tutar.
func TestEvalAgentSkipsIntentTier(t *testing.T) {
	// Üretim yolu: işaret nil → niyet kademesi koşar ve cevaplar.
	llmProd := newLoopLLM(t, intentLoopScript)
	sProd, _ := newLoopTestServer(t, llmProd, "u1")
	sProd.copilot.SetIntentClassify(copilot.IntentOnNoLoop)
	rows := captureCosreRows(t)
	ftProd := &fakeTools{}
	ftProd.install(t)
	postLoopChat(t, sProd, fullCtx(), "u1", map[string]any{"messages": []map[string]any{{"role": "user", "text": "Dünle kıyasla?"}}})
	cls, withTools := countIntentCalls(llmProd.calls())
	if cls != 1 || withTools != 0 || len(ftProd.snapshot()) != 0 {
		t.Fatalf("üretim: sınıflandırıcı=%d araçlı=%d araç=%+v — niyet kademesinde bitmeliydi", cls, withTools, ftProd.snapshot())
	}
	if rs := rows(); len(rs) != 1 || rs[0].Tier != aisurface.TierIntent {
		t.Fatalf("üretim kademesi: %+v", rs)
	}

	// Ajan vakası: niyet kademesi atlanır → serbest döngü.
	llm := newLoopLLM(t, intentLoopScript)
	s, _ := newLoopTestServer(t, llm, "u1")
	s.copilot.SetIntentClassify(copilot.IntentOnNoLoop)
	ft := &fakeTools{}
	ft.install(t)
	out := runEvalsetCase(fullCtx(), nil, agentServerFn(s), agentCase("a-skip-intent",
		evalExpect{ExpectedTools: []string{"compare_periods"}, ExpectTier: aisurface.TierLoop, MustContain: []string{"1830"}}))
	if out.Agent == nil || out.Agent.Tier != aisurface.TierLoop || len(out.Fails) != 0 {
		t.Fatalf("ajan vakası döngüye ulaşmadı: iz=%+v fails=%v", out.Agent, out.Fails)
	}
	cls, withTools = countIntentCalls(llm.calls())
	if cls != 0 || withTools != 2 {
		t.Fatalf("ajan vakası: sınıflandırıcı=%d (0 olmalı) araçlı=%d", cls, withTools)
	}
	if got := ft.snapshot(); len(got) != 1 || got[0].name != "compare_periods" {
		t.Fatalf("araç koşucusu: %+v", got)
	}
}

// TestEvalAgentExpectTierIntentKeepsIntent — vaka expectTier "intent"
// sabitlediyse kademeyi ölçmek istiyordur: sınıflandırıcı koşar, cevap niyet
// kademesinden gelir, döngü koşmaz.
func TestEvalAgentExpectTierIntentKeepsIntent(t *testing.T) {
	llm := newLoopLLM(t, intentLoopScript)
	s, _ := newLoopTestServer(t, llm, "u1")
	s.copilot.SetIntentClassify(copilot.IntentOnNoLoop)
	ft := &fakeTools{}
	ft.install(t)
	out := runEvalsetCase(fullCtx(), nil, agentServerFn(s), agentCase("a-intent-pinned",
		evalExpect{ExpectTier: aisurface.TierIntent}))
	if out.Agent == nil || out.Agent.Tier != aisurface.TierIntent || out.Agent.Rounds != 0 {
		t.Fatalf("niyet sabitli vaka: iz=%+v fails=%v", out.Agent, out.Fails)
	}
	for _, f := range out.Fails {
		if strings.HasPrefix(f, "tier ") {
			t.Fatalf("kademe ihlali: %v", out.Fails)
		}
	}
	cls, withTools := countIntentCalls(llm.calls())
	if cls != 1 || withTools != 0 || len(ft.snapshot()) != 0 {
		t.Fatalf("sınıflandırıcı=%d araçlı=%d araç=%+v", cls, withTools, ft.snapshot())
	}
}

// TestEvalAgentSkipIntentDecision — SAF karar + nil işaret (üretim) kararı
// değiştirmez.
func TestEvalAgentSkipIntentDecision(t *testing.T) {
	for _, tc := range []struct {
		tier string
		want bool
	}{
		{"", true},
		{aisurface.TierLoop, true},
		{aisurface.TierGuided, true},
		{aisurface.TierIntent, false},
	} {
		if got := evalAgentSkipIntent(agentCase("x", evalExpect{ExpectTier: tc.tier})); got != tc.want {
			t.Errorf("expectTier %q: skip=%v, want %v", tc.tier, got, tc.want)
		}
	}
	var nilMode *chatEvalMode
	if nilMode.skipIntent() {
		t.Fatal("nil işaret niyet kademesini atlamamalı (üretim aynen)")
	}
	if (&chatEvalMode{}).skipIntent() {
		t.Fatal("SkipIntent false iken kademe atlanmamalı")
	}
}
