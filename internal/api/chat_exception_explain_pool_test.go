package api

// chat_exception_explain_pool_test.go — v0.10.1411 (operator-reported):
// "Mesela bir operasyonu soruyorum ama bana kafka timeout vs diyor ama
// exceptions açıp explain ettirdiğimde daha mantıklı güzel cevap veriyor."
//
// 1399'un bilinen sınırı: "<OP> neden hata veriyor?" açıklamasının aday havuzu
// yalnız ön-getirmenin gösterdiği en yeni 5 gruptu; daha eski ama daha önemli
// bir P1 grubu 5'in dışında kalınca cevap küçük, yeni bir grubu anlatıyordu.
// Şimdi seçim gösterilen ∪ kapsamın AÇIK grupları (sınırlı state okuması)
// kümesinden; gösterilen liste değişmez; çip ile cevap aynı grubu söyler.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const poolOp = "ORDERS_GET_X"

func poolLine(fp, code, pri, state string, lastHour, occ uint64, lastSeen int64) exGroupLine {
	href := "/problems?exc=" + fp
	return exGroupLine{Fingerprint: fp, Code: code, Oracle: true, Operation: poolOp, Priority: pri, State: state,
		LastHour: lastHour, Occurrences: occ, LastSeen: lastSeen, Href: href, ExplainHref: href + "&ai=exception"}
}

// shownNewest5 — ön-getirmenin gösterdiği en yeni 5 (hepsi küçük, P3).
func shownNewest5() []exGroupLine {
	var out []exGroupLine
	for i, c := range []string{"ORD-101", "ORD-102", "ORD-103", "ORD-104", "ORD-105"} {
		out = append(out, poolLine("ora:000000000000010"+string(rune('1'+i)), c, "P3", "new", 1, 3, int64(1000-i)))
	}
	return out
}

func poolFingerprints(gs []exGroupLine) []string {
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.Fingerprint)
	}
	return out
}

func TestExplainPoolPick(t *testing.T) {
	oldP1 := poolLine("ora:00000000000000f1", "ORD-900", "P1", "acknowledged", 400, 90000, 10) // eski, önemli
	oldP2 := poolLine("ora:00000000000000f2", "ORD-800", "P2", "new", 50, 5000, 20)
	for _, c := range []struct {
		name   string
		shown  []exGroupLine
		pool   []exGroupLine
		top    string
		others []string
		ok     bool
	}{
		{"havuz okunmadı/okunamadı (nil) → bugünkü 5'li seçim", shownNewest5(), nil,
			"ora:0000000000000101", []string{"ora:0000000000000102", "ora:0000000000000103", "ora:0000000000000104", "ora:0000000000000105"}, true},
		{"5'in dışındaki eski P1 seçilir", shownNewest5(), append(shownNewest5(), oldP2, oldP1),
			oldP1.Fingerprint, []string{oldP2.Fingerprint, "ora:0000000000000101", "ora:0000000000000102", "ora:0000000000000103", "ora:0000000000000104", "ora:0000000000000105"}, true},
		{"havuzda gösterilenler yok: yine birleşim", shownNewest5()[:2], []exGroupLine{oldP1},
			oldP1.Fingerprint, []string{"ora:0000000000000101", "ora:0000000000000102"}, true},
		{"eşitlik: aynı P1/son saat/toplam/son görülme → parmak izi", []exGroupLine{poolLine("ora:00000000000000e2", "ORD-2", "P1", "new", 5, 5, 5)},
			[]exGroupLine{poolLine("ora:00000000000000e1", "ORD-1", "P1", "new", 5, 5, 5)},
			"ora:00000000000000e1", []string{"ora:00000000000000e2"}, true},
		{"eşitlik: P1'ler arasında son saat kazanır", []exGroupLine{poolLine("ora:00000000000000d1", "ORD-1", "P1", "new", 5, 99999, 900)},
			[]exGroupLine{poolLine("ora:00000000000000d2", "ORD-2", "P1", "new", 60, 10, 1)},
			"ora:00000000000000d2", []string{"ora:00000000000000d1"}, true},
		{"ignored/resolved dışarıda (gösterilende ve havuzda)", []exGroupLine{
			poolLine("ora:00000000000000c1", "ORD-1", "P1", "resolved", 999, 999, 999),
			poolLine("ora:00000000000000c2", "ORD-2", "P3", "new", 1, 1, 998)},
			[]exGroupLine{poolLine("ora:00000000000000c3", "ORD-3", "P1", "ignored", 999, 999, 1), poolLine("ora:00000000000000c4", "ORD-4", "P2", "regressed", 2, 2, 2)},
			"ora:00000000000000c4", []string{"ora:00000000000000c2"}, true},
		{"gösterilen yalnız resolved, havuzda açık grup", []exGroupLine{poolLine("ora:00000000000000b1", "ORD-1", "P1", "resolved", 9, 9, 9)},
			[]exGroupLine{poolLine("ora:00000000000000b2", "ORD-2", "P2", "new", 1, 1, 1)}, "ora:00000000000000b2", nil, true},
		{"havuz boş ama okundu → gösterilenden", shownNewest5()[:1], []exGroupLine{}, "ora:0000000000000101", nil, true},
	} {
		p := &opPrefetch{Operation: poolOp, Exceptions: &exceptionGroupsEvidence{Groups: c.shown}, explainPool: c.pool}
		top, others, ok := pickExplainGroup(p.explainCandidates())
		if ok != c.ok || top.Fingerprint != c.top {
			t.Errorf("%s: top=%q ok=%v, want %q %v", c.name, top.Fingerprint, ok, c.top, c.ok)
			continue
		}
		if got := strings.Join(poolFingerprints(others), ","); got != strings.Join(c.others, ",") {
			t.Errorf("%s: others=%s, want %s", c.name, got, strings.Join(c.others, ","))
		}
		// Çip olguları AYNI sıradan: ilk olgu = seçilen grup, kalanı others.
		facts := p.exceptionFollowupFacts()
		if len(facts) != 1+len(c.others) || facts[0].Fingerprint != c.top {
			t.Errorf("%s: çip olguları seçimle aynı değil: %+v", c.name, facts)
		}
		// Kırılım atlama kararı da aynı kümeden.
		if got := opDrillSkippedForErrorAsk(opTarget{ErrorAsk: true}, p); got != c.ok {
			t.Errorf("%s: opDrillSkippedForErrorAsk=%v", c.name, got)
		}
		// Gösterilen liste (model + adım çipi) yerinde kalır.
		if strings.Join(poolFingerprints(p.Exceptions.Groups), ",") != strings.Join(poolFingerprints(c.shown), ",") {
			t.Errorf("%s: gösterilen liste değişti", c.name)
		}
	}
	// nil havuzda birleşim gösterilen dilimin KENDİSİ (bayt bayt eski).
	shown := shownNewest5()
	if got := mergeExplainPool(shown, nil); &got[0] != &shown[0] || len(got) != len(shown) {
		t.Fatal("nil havuz gösterilen dilimi aynen döndürmeli")
	}
	// Tekil: aynı parmak izi havuzda tekrar gelirse gösterilen satır kazanır (örnek mesajı taşır).
	s0 := shown[0]
	s0.Sample = "örnek satır"
	dup := s0
	dup.Sample = ""
	if m := mergeExplainPool([]exGroupLine{s0}, []exGroupLine{dup}); len(m) != 1 || m[0].Sample != "örnek satır" {
		t.Fatalf("tekilleştirme: %+v", m)
	}
}

func TestCollectExplainPool(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	sc := exScope{Messages: []string{poolOp}, From: now.Add(-24 * time.Hour), To: now}
	row := chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint("src1", "ORD-900", poolOp), Type: "ORD-900", Message: poolOp,
		Service: "orders-api", State: "acknowledged", Occurrences: 90000, FirstSeen: now.Add(-20 * time.Hour).UnixNano(), LastSeen: now.Add(-3 * time.Hour).UnixNano()}
	fk := &fakeExGroups{rows: []chstore.ExceptionGroup{row}}
	pool, err := collectExplainPool(context.Background(), fk, sc, exHooks())
	if err != nil || len(pool) != 1 {
		t.Fatalf("pool=%+v err=%v", pool, err)
	}
	f := fk.gotFilter
	if f.State != "open" || f.Sort != "occurrences" || f.Dir != "desc" || f.Limit != opExceptionExplainPoolLimit ||
		len(f.Messages) != 1 || f.Messages[0] != poolOp || f.ActiveFromNs != sc.From.UnixNano() || f.ActiveToNs != sc.To.UnixNano() {
		t.Fatalf("süzgeç: %+v", f)
	}
	if opExceptionExplainPoolLimit < 50 || opExceptionExplainPoolLimit > 100 {
		t.Fatalf("havuz tavanı sınırlı olmalı: %d", opExceptionExplainPoolLimit)
	}
	g := pool[0]
	if g.Priority != "P1" || g.Code != "ORD-900" || g.Operation != poolOp || !g.HourKnown || g.LastHour != 900 || g.Href == "" {
		t.Fatalf("satır kancalardan geçmeli: %+v", g)
	}
	if len(fk.sampled) != 0 {
		t.Fatalf("havuz örnek okumamalı: %v", fk.sampled)
	}
	// Okuma hatası → nil + hata (çağıran bugünkü havuza düşer).
	if pool, err := collectExplainPool(context.Background(), &fakeExGroups{err: errors.New("code: 159 timeout")}, sc, exHooks()); err == nil || pool != nil {
		t.Fatalf("hata: pool=%v err=%v", pool, err)
	}
	if _, err := collectExplainPool(context.Background(), fk, exScope{}, exHooks()); err == nil {
		t.Fatal("kapsamsız okuma yapılmamalı")
	}
	if (*Server)(nil).exceptionExplainPool(context.Background(), sc) != nil || (&Server{}).exceptionExplainPool(context.Background(), sc) != nil {
		t.Fatal("deposuz sunucu nil havuz döndürmeli")
	}
}

func TestOpExplainPoolWanted(t *testing.T) {
	g := []exGroupLine{{Fingerprint: "ora:00000000000000a1"}}
	for _, c := range []struct {
		name     string
		errorAsk bool
		ev       exceptionGroupsEvidence
		want     bool
	}{
		{"hata-neden + gösterilmeyen grup var", true, exceptionGroupsEvidence{Groups: g, More: 4}, true},
		{"hepsi gösterildi (More 0) → ek okuma yok", true, exceptionGroupsEvidence{Groups: g}, false},
		{"hata-neden değil", false, exceptionGroupsEvidence{Groups: g, More: 4}, false},
		{"liste okunamadı", true, exceptionGroupsEvidence{Err: "timeout", Groups: g, More: 4}, false},
		{"grup yok", true, exceptionGroupsEvidence{Groups: []exGroupLine{}, More: 4}, false},
	} {
		if got := opExplainPoolWanted(c.errorAsk, c.ev); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	// Ön-getirme havuzu yalnız bu kapıdan ve AYNI kapsamla okur.
	body := funcBody(readRepoFile(t, "chat_operation_prefetch.go"), "opPrefetchForTarget")
	if !strings.Contains(body, "if opExplainPoolWanted(t.ErrorAsk, ev) {") || !strings.Contains(body, "p.explainPool = s.exceptionExplainPool(pctx, esc)") ||
		!strings.Contains(body, "ev := s.exceptionGroupsEvidence(pctx, esc)") {
		t.Fatal("havuz ön-getirmenin kapsamıyla, yalnız opExplainPoolWanted kapısından okunmalı")
	}
}

// Sohbet içi açıklama: 5'in dışındaki eski P1 açıklanır; çipler aynı sıradan
// ≤3 diğer grup; havuz yokken adım argümanları bayt bayt eski.
func TestOpExceptionExplainUsesPool(t *testing.T) {
	prov := &opExplainProvider{deltas: []string{"ORD-900 açıklaması."}}
	s := newOpExplainServer(t, prov)
	s.cache = newMemCache()
	p, asked := opExplainFixture(t, nil)
	p.Exceptions.Groups = shownNewest5()
	p.Exceptions.More = 3
	oldP1 := poolLine("ora:00000000000000f1", "ORD-900", "P1", "acknowledged", 400, 90000, 10)
	oldP2 := poolLine("ora:00000000000000f2", "ORD-800", "P2", "new", 50, 5000, 20)
	p.explainPool = append(shownNewest5(), oldP2, oldP1)
	var evs []opExplainEvent
	emit := withStepIDs(func(k string, v any) { evs = append(evs, opExplainEvent{k, v}) })
	if h, ok := s.opExceptionExplainInChat(context.Background(), emit, p, time.UTC); !h || !ok {
		t.Fatalf("handled=%v ok=%v", h, ok)
	}
	if len(*asked) != 1 || (*asked)[0] != oldP1.Fingerprint {
		t.Fatalf("5'in dışındaki eski P1 açıklanmalı: %v", *asked)
	}
	st, _ := json.Marshal(evs[0].data)
	if !strings.Contains(string(st), `\"group\":\"ORD-900\"`) || !strings.Contains(string(st), `\"candidates\":7`) {
		t.Fatalf("adım çipi seçilen grubu ve aday sayısını söylemeli: %s", st)
	}
	ans := evs[len(evs)-1].data.(map[string]any)
	if text := ans["text"].(string); !strings.HasSuffix(text, "Kaynak: Exceptions grubu ORD-900 · ORDERS_GET_X · Exceptions açıklamasıyla aynı kanıt") {
		t.Fatalf("künye seçilen grubu söylemeli: %q", text)
	}
	if links, _ := ans["links"].([]guidedAnswerLink); len(links) == 0 || links[0].Href != oldP1.Href {
		t.Fatalf("grup linki seçilen grup: %+v", ans["links"])
	}
	fus, _ := ans["followups"].([]chatFollowup)
	var groupChips []string
	for _, c := range fus {
		if c.Kind == fuKindExceptionGroup {
			groupChips = append(groupChips, c.Text)
		}
	}
	// Aynı sıralama: P2 (ORD-800), ardından P3'ler son görülme azalan.
	if want := "ORD-800 grubunu açıkla,ORD-101 grubunu açıkla,ORD-102 grubunu açıkla"; strings.Join(groupChips, ",") != want {
		t.Fatalf("çipler %v, want %s", groupChips, want)
	}
	// Serbest döngü çipi (takip olguları) da aynı grubu önce söyler.
	if f := p.exceptionFollowupFacts(); len(f) == 0 || f[0].Fingerprint != oldP1.Fingerprint {
		t.Fatalf("takip çipi açıklamayla aynı grubu söylemeli: %+v", f)
	}

	// Havuz okunamadı (nil) → bugünkü seçim + adım argümanları bayt bayt eski.
	q, asked2 := opExplainFixture(t, nil)
	evs = nil
	if h, ok := s.opExceptionExplainInChat(context.Background(), emit, q, time.UTC); !h || !ok {
		t.Fatal("havuzsuz tur işlenmedi")
	}
	if len(*asked2) != 1 || (*asked2)[0] != "ora:00000000000000b2" {
		t.Fatalf("havuzsuz seçim 1399 ile aynı olmalı: %v", *asked2)
	}
	want, _ := json.Marshal(map[string]any{"operation": "ORDERS_GET_X", "group": "ORD-200", "fingerprint": "ora:00000000000000b2"})
	st, _ = json.Marshal(evs[0].data)
	wantArgs, _ := json.Marshal(string(want))
	if !strings.Contains(string(st), string(wantArgs)) || strings.Contains(string(st), "candidates") {
		t.Fatalf("havuzsuz adım argümanları değişti: %s", st)
	}
}
