package api

// copilot_outbound_call_env_test.go — v0.10.1468 (outbound_call ardılları,
// operatör kararı 2026-10-23): (a) yolsuz host özetinde ortam YALNIZ ekrandan
// geldiyse MV'de (tüm ortamlar) kalınır ve cevap süzgecin uygulanmadığını
// söyler; soruda anılan ortam bugünkü ham, ortama süzülmüş okumaya gider.
// (b) belirsiz genel kök ("esb") aday host çiplerini korur (toplama yok).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestOutboundHostEnvScope(t *testing.T) {
	for _, c := range []struct {
		name          string
		in            guidedRoute
		wantEnv       string
		wantScreenEnv string
	}{
		{"ortam yok", guidedRoute{}, "", ""},
		{"yalnız ekran", guidedRoute{Env: "prod-eu-a", EnvInherited: true}, "", "prod-eu-a"},
		{"soruda anıldı", guidedRoute{Env: "prod-eu-a"}, "prod-eu-a", ""},
		{"≥2 ortam (soruda)", guidedRoute{EnvSet: []string{"prod-eu-a", "prod-eu-b"}}, "", ""},
	} {
		got, screen := outboundHostEnvScope(c.in)
		if got.Env != c.wantEnv || screen != c.wantScreenEnv || got.EnvInherited {
			t.Errorf("%s: env=%q screen=%q inherited=%v", c.name, got.Env, screen, got.EnvInherited)
		}
		if strings.Join(got.EnvSet, ",") != strings.Join(c.in.EnvSet, ",") {
			t.Errorf("%s: EnvSet değişti", c.name)
		}
	}
}

func obEnvHostReader() *fakeOutboundReader {
	return &fakeOutboundReader{
		hostCallers: func(time.Time, time.Time) []chstore.ExternalCaller {
			return []chstore.ExternalCaller{{Service: obTestSvc, Calls: 800, Errors: 4, AvgMs: 600, P99Ms: 1500}}
		},
		aggs: []chstore.OutboundAgg{
			{Env: "prod-eu-a", Service: obTestSvc, Peer: obHostESB, Path: "(tüm yollar)", Win: "cur", Calls: 500, Errors: 4, AvgMs: 610, P95Ms: 1400},
			{Env: "prod-eu-a", Service: obTestSvc, Peer: obHostESB, Path: "(tüm yollar)", Win: "prev", Calls: 500, Errors: 4, AvgMs: 600},
		},
	}
}

// Rota/ortam tablosu uçtan uca (guided kademesi, gerçek router + ekran devri).
func TestOutboundHostScreenEnvStaysOnMV(t *testing.T) {
	const screen = "prod-eu-b"
	for _, c := range []struct {
		name, msg   string
		raw         bool   // ham, ortama süzülmüş okuma
		envs        string // ham okumanın deploy_env süzgeci
		mustText    []string
		mustNotText []string
	}{
		{name: "ortam yalnız ekrandan → MV, tüm ortamlar + beyan", msg: "esbprod'a giden çağrılar yavaşladı mı?",
			mustText:    []string{"tüm ortamlar (MV)", "ekran ortamı (" + screen + ") süzgeci uygulanmadı", "ortam adıyla sorarsan", "kaynak: topology_edges_5m (MV)"},
			mustNotText: []string{"ortam " + screen + ",", "ham client span okuması"}},
		{name: "ortam soruda → ham, ortama süzülü", msg: "esbprod prod-eu-a ortamında gecikme arttı mı", raw: true, envs: "prod-eu-a",
			mustText:    []string{"ortam prod-eu-a", "ham client span okuması"},
			mustNotText: []string{"süzgeci uygulanmadı", "tüm ortamlar (MV)"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, llm := obHostServer(t)
			f := obEnvHostReader()
			installOutboundReader(t, f)
			ctx := armOutboundSlot(context.Background(), "", screen, 0, time.Time{})
			run := runOutboundGuidedEnv(t, s, ctx, c.msg, "", screen)
			if len(llm.calls()) != 0 {
				t.Error("LLM'siz olmalı")
			}
			calls := strings.Join(f.calls, " ")
			if gotRaw := strings.Contains(calls, "aggs"); gotRaw != c.raw {
				t.Fatalf("ham okuma=%v, beklenen %v (okumalar: %s)", gotRaw, c.raw, calls)
			}
			if c.raw && strings.Join(f.lastQuery.Envs, ",") != c.envs {
				t.Errorf("ham ortam süzgeci: %+v", f.lastQuery.Envs)
			}
			text, _ := run.answer["text"].(string)
			for _, w := range c.mustText {
				if !strings.Contains(text, w) {
					t.Errorf("cevapta %q yok:\n%s", w, text)
				}
			}
			for _, w := range c.mustNotText {
				if strings.Contains(text, w) {
					t.Errorf("cevapta %q olmamalı:\n%s", w, text)
				}
			}
			if ev := run.evidence[outboundCallsTool]; !c.raw && ev.Env != "" {
				t.Errorf("MV kanıtı ortam iddia etmemeli: %+v", ev)
			}
		})
	}
}

// Karar b (kayıt): belirsiz genel kök ("esb") tahmin/toplama yapmaz — aday
// host çipleri; hiçbir ölçüm okuması yok (ekran ortamı olsa da).
func TestOutboundAmbiguousStemKeepsChips(t *testing.T) {
	s, _ := obHostServer(t)
	f := obEnvHostReader()
	installOutboundReader(t, f)
	ctx := armOutboundSlot(context.Background(), "", "prod-eu-b", 0, time.Time{})
	run := runOutboundGuidedEnv(t, s, ctx, "esb gecikmesi arttı mı", "", "prod-eu-b")
	if len(f.calls) != 0 {
		t.Fatalf("belirsiz kökte okuma (toplama) yapılmamalı: %v", f.calls)
	}
	fu, _ := json.Marshal(run.answer["followups"])
	if !strings.Contains(string(fu), fuKindOutboundHost) || !strings.Contains(string(fu), obHostESB) || !strings.Contains(string(fu), obHostESBT) {
		t.Errorf("aday host çipleri: %s", fu)
	}
}
