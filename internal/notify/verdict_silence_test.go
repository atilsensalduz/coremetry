package notify

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1016 — "problem değil" imzalarının bildirimi: yalnız politika AÇIKSA
// susar (varsayılan kapalı). İmza sunucuda Problem'den yeniden üretilir ve
// FE'nin ürettiğiyle birebir aynı olmalıdır.

func TestVerdictSignature(t *testing.T) {
	cases := []struct {
		name string
		p    chstore.Problem
		want string
	}{
		{"alarm kuralı", chstore.Problem{ID: "abc", RuleID: "rule-1", Service: "payments"}, "p:rule-1|payments"},
		{"servissiz kural", chstore.Problem{ID: "abc", RuleID: "rule-1"}, "p:rule-1|"},
		{"exception grubu (new)", chstore.Problem{ID: exceptionGroupID("fp1", chstore.ExStateNew),
			RuleID: chstore.ExceptionGroupRulePrefix + chstore.ExStateNew, Service: "payments"}, "e:fp1"},
		{"exception grubu (regressed)", chstore.Problem{ID: exceptionGroupID("fp1", chstore.ExStateRegressed),
			RuleID: chstore.ExceptionGroupRulePrefix + chstore.ExStateRegressed, Service: "payments"}, "e:fp1"},
		{"olay öğretilmez", chstore.Problem{ID: "inc-1", RuleID: "incident:inc-1", Kind: chstore.NotifyKindIncident, Service: "payments"}, ""},
		{"kural kimliği yok", chstore.Problem{ID: "x", Service: "payments"}, ""},
	}
	for _, c := range cases {
		if got := verdictSignature(c.p); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
	if exceptionVerdictSignature("") != "" {
		t.Error("boş parmak izi imza üretmemeli")
	}
}

// Sunucu imzası FE'ninkiyle aynı kalıptan çıkmalı — biri değişirse öğretilen
// kararlar sessizce bildirimle eşleşmez olur.
func TestVerdictSignatureMatchesFrontend(t *testing.T) {
	fe, err := os.ReadFile("../../frontend/src/lib/problemVerdict.ts")
	if err != nil {
		t.Fatal(err)
	}
	// v0.10.1032 — kalıplar problemSignature / exceptionSignature'a taşındı
	// (tam sayfa detaylar da aynı imzayı kursun diye); satır imzası onlara
	// satırın KENDİ servisi ve kural kimliğiyle devreder. Korunan sözleşme aynı.
	for _, w := range []string{
		"`p:${p.ruleId}|${p.service ?? ''}`",
		"`e:${g.fingerprint}`",
		"problemSignature({ ruleId: it.problem.ruleId, service: it.service })",
		"exceptionSignature(it.exception)",
	} {
		if !strings.Contains(string(fe), w) {
			t.Errorf("problemVerdict.ts %s kalıbını taşımalı (sunucu: verdict_silence.go verdictSignature)", w)
		}
	}
}

func TestNoiseVerdictIndex(t *testing.T) {
	idx := noiseVerdictIndex([]chstore.ProblemVerdict{
		{Signature: "p:r|s", Verdict: chstore.ProblemVerdictNoise},
		{Signature: "e:fp", Verdict: chstore.ProblemVerdictReal},
		{Signature: "e:fp2", Verdict: chstore.ProblemVerdictNoise},
	})
	if _, ok := idx["p:r|s"]; !ok || len(idx) != 2 {
		t.Fatalf("yalnız noise kararları: %v", idx)
	}
	if _, ok := idx["e:fp"]; ok {
		t.Error("\"gerçek\" karar susturma dizinine girmemeli")
	}
}

// Önbellek sıcakken depo okunmaz; karar (politika açık ∧ imza noise ∧ işaret
// hâlâ susturuyor — v0.10.1531 R3: kalıcı ya da pencere içinde + baypassız).
func TestVerdictSilencedDecision(t *testing.T) {
	ctx := context.Background()
	recent := time.Now().Add(-time.Hour).UnixNano()
	warm := func(mute bool, v chstore.ProblemVerdict) *Notifier {
		v.Signature, v.Verdict = "p:r|s", chstore.ProblemVerdictNoise
		return &Notifier{store: &chstore.Store{}, pvAt: time.Now(), pvMute: mute,
			pvNoise: map[string]chstore.ProblemVerdict{"p:r|s": v}}
	}
	p3 := func() (chstore.ProblemStandard, string) { return chstore.ProblemStandard{}, "P3" }
	if warm(false, chstore.ProblemVerdict{At: recent}).verdictSilenced(ctx, "p:r|s", p3) {
		t.Error("politika KAPALI: noise imzası bile susmamalı (varsayılan davranış değişmez)")
	}
	on := warm(true, chstore.ProblemVerdict{At: recent, Priority: "P3"})
	if !on.verdictSilenced(ctx, "p:r|s", p3) {
		t.Error("politika açık + taze noise imzası → susmalı")
	}
	if on.verdictSilenced(ctx, "p:other|s", p3) {
		t.Error("işaretlenmemiş imza susmamalı")
	}
	if on.verdictSilenced(ctx, "", p3) {
		t.Error("imzasız kayıt (olay) susmamalı")
	}
	old := warm(true, chstore.ProblemVerdict{At: time.Now().Add(-8 * 24 * time.Hour).UnixNano()})
	if old.verdictSilenced(ctx, "p:r|s", p3) {
		t.Error("R3: 7 günden eski işaret bildirimi susturmamalı (zaman sınırlı)")
	}
	perm := warm(true, chstore.ProblemVerdict{At: time.Now().Add(-90 * 24 * time.Hour).UnixNano(), Permanent: true})
	if !perm.verdictSilenced(ctx, "p:r|s", p3) {
		t.Error("R3: yönetici kalıcı gizlemesi her zaman susturur")
	}
	p1 := func() (chstore.ProblemStandard, string) { return chstore.ProblemStandard{}, "P1" }
	if on.verdictSilenced(ctx, "p:r|s", p1) {
		t.Error("R3: öncelik P3 → P1 yükseldi → baypas, bildirim gider")
	}
	var nilN *Notifier
	if nilN.verdictSilenced(ctx, "p:r|s", p3) || (&Notifier{}).verdictSilenced(ctx, "p:r|s", p3) {
		t.Error("notifier / depo yokken susturma yok")
	}
}

// Kablolama: kapı SSE yayınından SONRA (canlı akış güncellenir), üç yolda da
// var; okuma hatası susturmaz.
func TestVerdictSilenceWiring(t *testing.T) {
	n, err := os.ReadFile("notify.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(n)
	gate := strings.Index(src, "n.verdictSilenced(ctx, sig, problemSilenceStd(p))")
	pub := strings.Index(src, `n.Publish("problem.open", p)`)
	if gate < 0 || pub < 0 || gate < pub {
		t.Errorf("SendProblemAlert kapısı SSE yayınından sonra olmalı (gate=%d publish=%d)", gate, pub)
	}
	e, err := os.ReadFile("exception_notifier.go")
	if err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(string(e), "e.n.verdictSilenced(ctx, exceptionVerdictSignature(g.Fingerprint), exceptionSilenceStd(g, "); c != 2 {
		t.Errorf("exception yolları (kanal + P1 anonsu) kapıyı çağırmalı: %d, 2 beklenir", c)
	}
	v, err := os.ReadFile("verdict_silence.go")
	if err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(string(v), "return false, nil"); c != 3 {
		t.Errorf("politika kapalı + iki okuma hatası susturmasız dönmeli: %d, 3 beklenir", c)
	}
}
