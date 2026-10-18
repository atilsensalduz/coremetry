package sre

// recommend_test.go — v0.10.1293 — AI SRE Faz 4: yalnız öneri (runbook
// eşleme + geri alma metni).
//
// TestMatchRunbooks (SAF eşleme sırası + tavan + devre dışı), TestRecommendation
// IsTextOnly (öneri tipi yalnız metin/bağlantı; bağlantı /api/ ucu değil; ajan
// hiçbir şey uygulamaz), geri alma METNİ + Argo eki, işaret taşıma, Uygulandı /
// Ele yazımı (kendi kaydı, skor ve hipotezler değişmez), sohbet / anlatım ≤2.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func recHyp(kind string, score float64, band string) Hypothesis {
	return Hypothesis{Kind: kind, Label: HypothesisLabel(kind), Category: HypothesisCategory(kind), Score: score, Band: band,
		Checks: []HypothesisCheck{{Name: "x", Outcome: 1, Weight: 0.3, Reason: "başlangıçtan 12 dk önce rollout"}}}
}

func TestMatchRunbooks(t *testing.T) {
	books := []chstore.Runbook{
		{ID: "rb-svc", Title: "Checkout genel müdahale", Enabled: true, Labels: []string{"service:checkout"}},
		{ID: "rb-deploy", Title: "Kötü sürümü geri al", Enabled: true, Labels: []string{"cause:deploy", "service:checkout"}},
		{ID: "rb-rule", Title: "Anomali triyajı", Enabled: true, Labels: []string{"rule:anomaly:"}},
		{ID: "rb-off", Title: "Kapalı runbook", Enabled: false, Labels: []string{"service:checkout", "cause:deploy"}},
		{ID: "rb-title", Title: "Payments DB latency", Enabled: true},
		{ID: "rb-other", Title: "Kafka lag", Enabled: true, Labels: []string{"service:orders"}},
		{ID: "rb-cap", Title: "Kapasite planı", Enabled: true, Labels: []string{"cause:capacity"}},
	}
	p := chstore.Problem{ID: "p1", Service: "checkout", RuleID: "anomaly:checkout:p99_ms", RuleName: "p99 latency", Metric: "p99_ms"}
	hyps := []Hypothesis{recHyp(HypDeploy, 0.78, BandHigh), recHyp(HypDBSlow, 0.45, BandMedium), recHyp(HypSaturation, 0.2, BandLow)}
	ids := func(ms []RunbookMatch) []string {
		var out []string
		for _, m := range ms {
			out = append(out, firstNonEmpty(m.Runbook.ID, m.Runbook.URL))
		}
		return out
	}
	for _, c := range []struct {
		name  string
		p     chstore.Problem
		hyps  []Hypothesis
		books []chstore.Runbook
		want  []string
	}{
		// Etiket: rb-deploy iki etiket (cause + service) → önce; rb-svc ve rb-rule
		// bir etiket (başlık sırası). Tavan 3 → başlık örtüşmesi düşer.
		{"etiket sırası + tavan", p, hyps, books, []string{"rb-deploy", "rb-rule", "rb-svc"}},
		// Problem bağlantısı (iç) her zaman ilk.
		{"iç RunbookURL ilk", withURL(p, "/runbook?id=rb-title"), hyps, books, []string{"rb-title", "rb-deploy", "rb-rule"}},
		// Dış bağlantı kendisi; /api/ bağlantısı asla.
		{"dış RunbookURL", withURL(p, "https://wiki/x"), hyps, books, []string{"https://wiki/x", "rb-deploy", "rb-rule"}},
		{"/api/ bağlantısı yok sayılır", withURL(p, "/api/runbooks/rb-svc/execute"), hyps, books, []string{"rb-deploy", "rb-rule", "rb-svc"}},
		// Devre dışı iç bağlantı önerilmez.
		{"devre dışı iç bağlantı", withURL(p, "/runbook?id=rb-off"), nil, []chstore.Runbook{books[3]}, nil},
		// Başlık örtüşmesi: "Payments DB latency" — payments/latency bağlamda
		// (servis payments, kural "p99 latency") → 2/2 ≥ 0.5.
		{"başlık örtüşmesi", chstore.Problem{ID: "p2", Service: "payments", RuleName: "p99 latency"}, nil, books, []string{"rb-title"}},
		// Düşük bantlı / elenmiş hipotez cause: etiketini tetiklemez.
		{"düşük bant cause yok", chstore.Problem{ID: "p3", Service: "x"}, []Hypothesis{recHyp(HypSaturation, 0.2, BandLow)}, books, nil},
		{"elenmiş hipotez", chstore.Problem{ID: "p3", Service: "x"}, []Hypothesis{func() Hypothesis {
			h := recHyp(HypSaturation, 0.8, BandHigh)
			h.HumanState = &HumanState{State: MarkDismissed}
			return h
		}()}, books, nil},
		// Kategori de cause: etiketidir (capacity).
		{"kategori etiketi", chstore.Problem{ID: "p4", Service: "x"}, []Hypothesis{recHyp(HypTrafficSurge, 0.6, BandMedium)}, books, []string{"rb-cap"}},
		{"runbook yok", p, hyps, nil, nil},
	} {
		got := matchRunbooks(c.p, c.hyps, c.books)
		if !reflect.DeepEqual(ids(got), c.want) {
			t.Errorf("%s: %v, beklenen %v", c.name, ids(got), c.want)
		}
		for _, m := range got {
			if m.Runbook.ID != "" && m.Runbook.URL != runbookUIPath(m.Runbook.ID, c.p.ID) {
				t.Errorf("%s: iç runbook UI yoluna problemId ile gitmeli: %s", c.name, m.Runbook.URL)
			}
			if m.Reason == "" {
				t.Errorf("%s: gerekçe boş", c.name)
			}
		}
	}
	if got := matchRunbooks(p, hyps, books); got[0].Kind != HypDeploy {
		t.Errorf("cause: eşleşmesi hipotez türünü taşımalı: %+v", got[0])
	}
	if u := runbookUIPath("rb 1&x", "p/1"); u != "/runbook?id=rb+1%26x&problemId=p%2F1" {
		t.Errorf("UI yolu kaçışlı olmalı: %s", u)
	}
}

func withURL(p chstore.Problem, u string) chstore.Problem { p.RunbookURL = u; return p }

func rollbackFacts() *Facts {
	return &Facts{Deploys: &DeployFacts{Rows: []DeployFact{
		{At: start.Add(-50 * time.Minute).UnixNano(), Source: "rollout", Name: "checkout", Version: "rev 40", Prev: "rev 39"},
		{At: start.Add(-12 * time.Minute).UnixNano(), Source: "rollout", Name: "checkout", Version: "rev 42", Prev: "rev 41"},
		{At: start.Add(-3 * time.Hour).UnixNano(), Source: "rollout", Name: "checkout", Version: "rev 9", Prev: "rev 8"},
	}}}
}

func TestBuildRecommendationsRollback(t *testing.T) {
	in := recInput{ProblemID: "p1", Service: "checkout", StartNs: start.UnixNano(), Facts: rollbackFacts(),
		Hyps: []Hypothesis{recHyp(HypDeploy, 0.78, BandHigh), recHyp(HypDependency, 0.1, BandLow)}}
	// Argo okunmadı → ek yok.
	recs := buildRecommendations(in)
	if len(recs) != 1 || recs[0].ID != "rollback" || recs[0].Rollout == nil || recs[0].Argo != nil {
		t.Fatalf("geri alma önerisi: %+v", recs)
	}
	r := recs[0]
	if r.Text != "checkout rev 42 → rev 41 geri alma değerlendirilebilir — kararı ve uygulamayı insan verir" ||
		r.Rollout.From != "rev 42" || r.Rollout.To != "rev 41" || r.State != RecPending || r.HypothesisKind != HypDeploy {
		t.Fatalf("metin: %+v", r)
	}
	if !strings.Contains(r.Rationale, "Deploy hipotezi yüksek 0.78") || !strings.Contains(r.Rationale, "başlangıçtan 12 dk önce rollout") {
		t.Fatalf("gerekçe: %s", r.Rationale)
	}
	// Argo okundu → "(Argo: OutOfSync, Degraded)"; iş yüküne eşlenen uygulama seçilir.
	in.ArgoRead = true
	in.Argo = []mcptools.ArgoAppStatus{
		{App: "orders", Sync: "Synced", Health: "Healthy", Workloads: []string{"orders"}},
		{App: "checkout-prod", Sync: "OutOfSync", Health: "Degraded", Workloads: []string{"checkout"}},
	}
	r = buildRecommendations(in)[0]
	if !strings.Contains(r.Text, "(Argo: OutOfSync, Degraded)") || r.Argo == nil || r.Argo.App != "checkout-prod" || r.Argo.PrevRevision != "rev 41" {
		t.Fatalf("Argo eki: %+v %+v", r, r.Argo)
	}
	// Geri alınmış rollout ya da önceki sürümü bilinmeyen satır aday değil.
	f := rollbackFacts()
	f.Deploys.Rows[1].Status = "rolled_back"
	if rb := rollbackCandidate(f, start.UnixNano()); rb == nil || rb.Version != "rev 40" {
		t.Fatalf("geri alınmış satır atlanmalı: %+v", rb)
	}
	f.Deploys.Rows[0].Prev = ""
	if rb := rollbackCandidate(f, start.UnixNano()); rb != nil {
		t.Fatalf("önceki sürüm yoksa aday yok: %+v", rb)
	}
	// Aday yoksa deploy hipotezi genel cümleye düşer. (v0.10.1310: Argo artık
	// koşu başına tek okumayla Facts.Argo'da — needsArgo kapısı kalktı,
	// TestArgoSingleReadPerRun.)
	in.Facts = &Facts{}
	if recs := buildRecommendations(in); len(recs) != 1 || recs[0].ID != "hyp-deploy" || recs[0].Rollout != nil {
		t.Fatalf("adaysız deploy önerisi: %+v", recs)
	}
}

// argoCheck — kayıttaki config kartının argo_sync kontrolü.
func argoCheck(t *testing.T, inv *Investigation) HypothesisCheck {
	t.Helper()
	for _, h := range inv.Hypotheses {
		if h.Kind != HypConfig {
			continue
		}
		for _, c := range h.Checks {
			if c.Name == "argo_sync" {
				return c
			}
		}
	}
	t.Fatalf("argo_sync kontrolü yok: %+v", inv.Hypotheses)
	return HypothesisCheck{}
}

// TestArgoSingleReadPerRun — v0.10.1310: Deps.ArgoApps koşu başına EN ÇOK BİR
// kez okunur; argo_sync (SAF Score) ve geri alma önerisinin Argo eki aynı
// Facts.Argo'yu okur. Taze yeniden koşu okumaz; config doğrulaması zorla
// okur; başka hipotezin doğrulaması önceki parçayı kullanır; skor kayıttan
// (evidenceFrom) aynen yeniden hesaplanır.
func TestArgoSingleReadPerRun(t *testing.T) {
	c := &calls{}
	rd := okReaders(c, &fakeNarrator{active: true, auto: true, answer: "Ne oldu: x"})
	rd.deploys = func(context.Context, string, time.Time, time.Time) (mcptools.DeployCheck, error) {
		return mcptools.DeployCheck{Section: mcptools.SweepSection{Name: "deploys", State: "ok"}, Rows: []mcptools.ChangeRow{
			{Source: "rollout", TimeUnixNs: start.Add(-12 * time.Minute).UnixNano(), Workload: "checkout", Version: "rev 42", PrevVersion: "rev 41"},
		}}, nil
	}
	rd.argo = func(_ context.Context, svc string) ([]mcptools.ArgoAppStatus, error) {
		c.hit("argo:" + svc)
		return []mcptools.ArgoAppStatus{{App: "checkout", Sync: "OutOfSync", Health: "Healthy", Workloads: []string{"checkout"}}}, nil
	}
	ctx := context.Background()
	inv := newInv()
	runInvestigation(ctx, rd, inv, func(*Investigation) {}, func() time.Time { return t0 })
	if c.get("argo:checkout") != 1 {
		t.Fatalf("ilk koşu tek Argo okuması: %v", c.n)
	}
	if a := inv.Facts.Argo; a == nil || a.State != argoStateOK || a.ReadAt != t0.UnixNano() || len(a.Apps) != 1 {
		t.Fatalf("Facts.Argo: %+v", inv.Facts.Argo)
	}
	if ck := argoCheck(t, inv); ck.Outcome != 1 || !strings.Contains(ck.Reason, "OutOfSync") {
		t.Fatalf("argo_sync doğrulamalı: %+v", ck)
	}
	var rb *Recommendation
	for i := range inv.Recommendations {
		if inv.Recommendations[i].ID == "rollback" {
			rb = &inv.Recommendations[i]
		}
	}
	if rb == nil || rb.Argo == nil || !strings.Contains(rb.Text, "(Argo: OutOfSync)") {
		t.Fatalf("öneri eki aynı parçadan: %+v", inv.Recommendations)
	}
	// Kayıt → skor: saf yeniden hesap aynı (gerçekler kayıtta).
	raw, err := Encode(*inv)
	if err != nil {
		t.Fatal(err)
	}
	var back Investigation
	if err := json.Unmarshal([]byte(raw), &back); err != nil {
		t.Fatal(err)
	}
	if got, want := Score(evidenceFrom(&back)), Score(evidenceFrom(inv)); !reflect.DeepEqual(got, want) {
		t.Fatalf("kayıttan yeniden skor farklı:\n%+v\n%+v", got, want)
	}

	// Taze yeniden koşu (10 dk içinde) Argo'yu yeniden okumaz.
	runInvestigation(ctx, rd, inv, func(*Investigation) {}, func() time.Time { return t0.Add(time.Minute) })
	if c.get("argo:checkout") != 1 {
		t.Fatalf("taze parça yeniden okunmamalı: %v", c.n)
	}
	// Başka hipotezin doğrulaması önceki parçayı kullanır; config doğrulaması zorla okur.
	inv.Kind, inv.Hypothesis = KindVerify, HypSaturation
	runInvestigation(ctx, rd, inv, func(*Investigation) {}, func() time.Time { return t0.Add(20 * time.Minute) })
	if c.get("argo:checkout") != 1 {
		t.Fatalf("doygunluk doğrulaması Argo okumamalı: %v", c.n)
	}
	inv.Hypothesis = HypConfig
	runInvestigation(ctx, rd, inv, func(*Investigation) {}, func() time.Time { return t0.Add(21 * time.Minute) })
	if c.get("argo:checkout") != 2 || inv.Facts.Argo.ReadAt != t0.Add(21*time.Minute).UnixNano() {
		t.Fatalf("config doğrulaması Argo'yu yeniden okur (tek kez): %v %+v", c.n, inv.Facts.Argo)
	}

	// Okuma hatası: State=error, hata metni yankılanmaz, kontrol 0.
	rd.argo = func(context.Context, string) ([]mcptools.ArgoAppStatus, error) {
		c.hit("argo-err")
		return nil, errors.New("dial https://hub.internal?token=s3cret")
	}
	inv2 := newInv()
	runInvestigation(ctx, rd, inv2, func(*Investigation) {}, func() time.Time { return t0 })
	if c.get("argo-err") != 1 || inv2.Facts.Argo == nil || inv2.Facts.Argo.State != argoStateError || strings.Contains(inv2.Facts.Argo.Note, "s3cret") {
		t.Fatalf("hata parçası: %v %+v", c.n, inv2.Facts.Argo)
	}
	if ck := argoCheck(t, inv2); ck.Outcome != 0 {
		t.Fatalf("okunamayan Argo bilinmiyor: %+v", ck)
	}
	for _, r := range inv2.Recommendations {
		if r.Argo != nil {
			t.Fatalf("okunamayan Argo öneriye eklenmez: %+v", r)
		}
	}
}

// TestReplayArgoUnknown — v0.10.1310: tekrar (Kind=replay) Deps.ArgoApps'i
// HİÇ çağırmaz — Argo durumu "şimdi"dir, as-of değil; geçmiş vaka canlı
// durumla puanlanmaz. Parça State=replay + not, argo_sync 0.
func TestReplayArgoUnknown(t *testing.T) {
	c := &calls{}
	rd := okReaders(c, &fakeNarrator{active: true, auto: true, answer: "x"})
	rd.argo = func(context.Context, string) ([]mcptools.ArgoAppStatus, error) {
		c.hit("argo")
		return []mcptools.ArgoAppStatus{{App: "checkout", Sync: "OutOfSync", Health: "Degraded"}}, nil
	}
	old := time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC)
	inv := replayInvestigation(ReplayRequest{RunID: "e1", CaseID: "p:p1", ProblemID: "p1", Service: "checkout", ProblemStartedAt: old.UnixNano(), Now: t0})
	runInvestigation(context.Background(), rd, &inv, func(*Investigation) {}, func() time.Time { return t0 })
	if c.get("argo") != 0 {
		t.Fatalf("tekrar canlı Argo okumamalı: %v", c.n)
	}
	if a := inv.Facts.Argo; a == nil || a.State != argoStateReplay || a.Note != ReplayArgoNote || len(a.Apps) != 0 {
		t.Fatalf("tekrar parçası: %+v", inv.Facts.Argo)
	}
	if ck := argoCheck(t, &inv); ck.Outcome != 0 || ck.Reason != ReplayArgoNote {
		t.Fatalf("tekrarda argo_sync bilinmiyor + not: %+v", ck)
	}
}

// TestRecommendationIsTextOnly — öneri YALNIZ METİNDİR: tip ağacında işlev /
// kanal / arayüz / harita yok (yürütülebilir yük taşıyamaz); kurulan her
// önerinin metni + gerekçesi dolu, durumu "öneri — uygulanmadı", bağlantısı
// UI yolu ya da problemin dış bağlantısı — asla /api/ ucu; kaynakta yürütme /
// geri alma / senkron çağrısı yok.
func TestRecommendationIsTextOnly(t *testing.T) {
	var walk func(rt reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		switch rt.Kind() {
		case reflect.String, reflect.Int64, reflect.Bool:
			return
		case reflect.Pointer:
			walk(rt.Elem(), path)
		case reflect.Struct:
			for i := 0; i < rt.NumField(); i++ {
				walk(rt.Field(i).Type, path+"."+rt.Field(i).Name)
			}
		default:
			t.Errorf("%s: %s — öneri yalnız metin/bağlantı taşır", path, rt.Kind())
		}
	}
	walk(reflect.TypeOf(Recommendation{}), "Recommendation")

	books := []chstore.Runbook{{ID: "rb-1", Title: "Checkout geri alma", Enabled: true, Labels: []string{"service:checkout"}}}
	in := recInput{ProblemID: "p1", Problem: chstore.Problem{ID: "p1", Service: "checkout", RunbookURL: "https://wiki/runbook"}, Service: "checkout",
		StartNs: start.UnixNano(), Facts: rollbackFacts(), Books: books,
		Hyps: []Hypothesis{recHyp(HypDeploy, 0.8, BandHigh), recHyp(HypSaturation, 0.5, BandMedium), recHyp(HypConfig, 0.45, BandMedium)}}
	recs := buildRecommendations(in)
	if len(recs) < 4 || len(recs) > RecommendationsMax {
		t.Fatalf("öneri sayısı %d: %+v", len(recs), recs)
	}
	for _, r := range recs {
		if r.ID == "" || strings.TrimSpace(r.Text) == "" || strings.TrimSpace(r.Rationale) == "" || r.State != RecPending || r.AckBy != "" || r.AckAt != 0 {
			t.Errorf("öneri: %+v", r)
		}
		if r.ID != recIDSafe(r.ID) {
			t.Errorf("kimlik URL'ye güvenli değil: %q", r.ID)
		}
		if r.Runbook != nil && (strings.HasPrefix(r.Runbook.URL, "/api/") || r.Runbook.URL == "") {
			t.Errorf("runbook bağlantısı UI yolu olmalı: %+v", r.Runbook)
		}
		low := strings.ToLower(r.Text)
		for _, verb := range []string{"uygulandı", "geri alındı", "çalıştırıldı", "senkronlandı", "yeniden başlatıldı"} {
			if strings.Contains(low, verb) {
				t.Errorf("öneri metni eylem yapılmış gibi konuşamaz (%q): %s", verb, r.Text)
			}
		}
	}
	raw, _ := json.Marshal(recs)
	if strings.Contains(string(raw), "/execute") {
		t.Fatalf("öneri yürütme ucuna bağlantı taşımamalı: %s", raw)
	}
	b, err := os.ReadFile("recommend.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, needle := range []string{"ExecuteRunbook", "StartExecution", "RolloutUndo", "Rollback(", "Sync(", "TriggerSync", "http."} {
		if strings.Contains(src, needle) {
			t.Errorf("recommend.go %q içermemeli — ajan yürütmez", needle)
		}
	}
}

func TestCarryRecState(t *testing.T) {
	prev := []Recommendation{{ID: "rollback", State: RecAck, AckBy: "a@x", AckAt: 5}, {ID: "rb-1", State: RecDismissed, AckBy: "b@x", AckAt: 6}, {ID: "hyp-db_slow", State: RecPending}}
	next := []Recommendation{{ID: "rollback", State: RecPending, Text: "yeni"}, {ID: "hyp-db_slow", State: RecPending}, {ID: "rb-2", State: RecPending}}
	got := carryRecState(next, prev)
	if got[0].State != RecAck || got[0].AckBy != "a@x" || got[0].Text != "yeni" || got[1].State != RecPending || got[2].State != RecPending {
		t.Fatalf("taşıma: %+v", got)
	}
}

func TestApplyRecommendationAck(t *testing.T) {
	st := newFakeStore()
	ctx := context.Background()
	hs := []Hypothesis{recHyp(HypDeploy, 0.78, BandHigh)}
	inv := Investigation{ProblemID: "p1", Service: "checkout", Kind: KindAuto, State: StateDone, Run: 1, EnqueuedAt: 1, Hypotheses: hs,
		Recommendations: []Recommendation{{ID: "rollback", Text: "checkout rev 42 → rev 41 geri alma değerlendirilebilir", Rationale: "r", State: RecPending}}}
	if err := save(ctx, st, inv); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	for _, c := range []struct {
		req  RecAckRequest
		want error
	}{
		{RecAckRequest{ID: "rollback", State: "maybe"}, ErrMarkInvalid},
		{RecAckRequest{ID: "", State: RecAck}, ErrMarkInvalid},
		{RecAckRequest{ID: "nope", State: RecAck}, ErrMarkNotFound},
	} {
		if _, err := ApplyRecommendationAck(ctx, st, "p1", c.req); !errors.Is(err, c.want) {
			t.Errorf("%+v: %v, beklenen %v", c.req, err, c.want)
		}
	}
	if _, err := ApplyRecommendationAck(ctx, st, "zz", RecAckRequest{ID: "rollback", State: RecAck}); !errors.Is(err, ErrMarkNotFound) {
		t.Errorf("kayıt yok: %v", err)
	}
	r, err := ApplyRecommendationAck(ctx, st, "p1", RecAckRequest{ID: "rollback", State: RecAck, By: "ed@x", Now: now})
	if err != nil || r.State != RecAck || r.AckBy != "ed@x" || r.AckAt != now.UnixNano() {
		t.Fatalf("ack: %+v %v", r, err)
	}
	got := st.get(t, "p1")
	if got.Recommendations[0].State != RecAck || got.Hypotheses[0].Score != 0.78 || got.State != StateDone || got.Hypotheses[0].HumanState != nil {
		t.Fatalf("kayıt: %+v", got)
	}
	// Koşan kayıt reddedilir; yeniden kuyruk işareti taşır.
	got.State = StateRunning
	_ = save(ctx, st, got)
	if _, err := ApplyRecommendationAck(ctx, st, "p1", RecAckRequest{ID: "rollback", State: RecDismissed}); !errors.Is(err, ErrMarkBusy) {
		t.Fatalf("koşan kayıt: %v", err)
	}
	got.State = StateDone
	_ = save(ctx, st, got)
	if _, err := Enqueue(ctx, st, EnqueueRequest{ProblemID: "p1", Service: "checkout", Kind: KindManual, Now: now}, NormalizeSettings(DefaultSettings())); err != nil {
		t.Fatal(err)
	}
	if re := st.get(t, "p1"); len(re.Recommendations) != 1 || re.Recommendations[0].State != RecAck {
		t.Fatalf("yeniden koşu işareti taşımalı: %+v", re.Recommendations)
	}
}

// TestRecommendationsInChatAndNarration — get_problem özeti ve anlatım girdisi
// elenmemiş ilk ≤2 öneriyi taşır; bulgular yine EN SONDA.
func TestRecommendationsInChatAndNarration(t *testing.T) {
	inv := *newInv()
	inv.State = StateDone
	inv.Recommendations = []Recommendation{
		{ID: "a", Text: "A önerisi", Rationale: "ra", State: RecDismissed},
		{ID: "b", Text: "B önerisi", Rationale: "rb", State: RecPending, Runbook: &RecRunbook{ID: "rb-1", Title: "RB", URL: "/runbook?id=rb-1"}},
		{ID: "c", Text: "C önerisi", Rationale: "rc", State: RecAck},
		{ID: "d", Text: "D önerisi", Rationale: "rd", State: RecPending},
	}
	inv.Findings = []Finding{{Kind: "operation", Name: "POST /pay", Level: "crit"}}
	sum := SummaryOf(inv)
	if len(sum.Recommendations) != 2 || sum.Recommendations[0].Text != "B önerisi" || sum.Recommendations[0].Runbook != "RB" || sum.Recommendations[1].State != RecAck {
		t.Fatalf("sohbet özeti: %+v", sum.Recommendations)
	}
	txt := renderInvestigationTR(inv, chstore.Problem{}, time.UTC)
	i, j := strings.Index(txt, "AJAN ÖNERİLERİ"), strings.Index(txt, "BULGULAR")
	if i < 0 || j < i || !strings.Contains(txt, "- B önerisi\n") || !strings.Contains(txt, "- C önerisi (operatör uyguladığını işaretledi)") ||
		strings.Contains(txt, "A önerisi") || strings.Contains(txt, "D önerisi") {
		t.Fatalf("anlatım girdisi:\n%s", txt)
	}
	if renderRecommendationsTR(nil) != "" {
		t.Fatal("önerisiz kayıt satır basmaz (golden bayt bayt aynı)")
	}
}

// TestRecommendInRun — orkestratör hipotezlerden sonra önerileri kurar
// (runbook listesi + problemin bağlantısı + tembel Argo), anlatımdan ÖNCE.
func TestRecommendInRun(t *testing.T) {
	c := &calls{}
	n := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: x"}
	rd := okReaders(c, n)
	rd.deploys = func(context.Context, string, time.Time, time.Time) (mcptools.DeployCheck, error) {
		return mcptools.DeployCheck{Section: mcptools.SweepSection{Name: "deploys", State: "ok"}, Rows: []mcptools.ChangeRow{
			{Source: "rollout", TimeUnixNs: start.Add(-12 * time.Minute).UnixNano(), Workload: "checkout", Version: "rev 42", PrevVersion: "rev 41"},
		}}, nil
	}
	rd.runbooks = func(context.Context) ([]chstore.Runbook, error) {
		c.hit("runbooks")
		return []chstore.Runbook{{ID: "rb-1", Title: "Checkout sürüm geri alma", Enabled: true, Labels: []string{"service:checkout"}}}, nil
	}
	rd.runbookURL = func(context.Context, chstore.Problem) string { c.hit("runbookURL"); return "" }
	rd.argo = func(_ context.Context, svc string) ([]mcptools.ArgoAppStatus, error) {
		c.hit("argo:" + svc)
		return []mcptools.ArgoAppStatus{{App: "checkout", Sync: "OutOfSync", Health: "Healthy", Workloads: []string{"checkout"}}}, nil
	}
	inv := newInv()
	runInvestigation(context.Background(), rd, inv, func(*Investigation) {}, func() time.Time { return t0 })
	var rb, book *Recommendation
	for i := range inv.Recommendations {
		switch inv.Recommendations[i].ID {
		case "rollback":
			rb = &inv.Recommendations[i]
		case "rb-rb-1": // kimlik "rb-" + runbook kimliği
			book = &inv.Recommendations[i]
		}
	}
	if rb == nil || !strings.Contains(rb.Text, "checkout rev 42 → rev 41 geri alma değerlendirilebilir (Argo: OutOfSync)") || book == nil ||
		book.Runbook.URL != "/runbook?id=rb-1&problemId=p1" {
		t.Fatalf("öneriler: %+v", inv.Recommendations)
	}
	if c.get("runbooks") != 1 || c.get("runbookURL") != 1 || c.get("argo:checkout") != 1 {
		t.Fatalf("okumalar: %v", c.n)
	}
	if !strings.Contains(n.user, "AJAN ÖNERİLERİ") {
		t.Fatalf("anlatım girdisi öneri taşımalı:\n%s", n.user)
	}
	if _, err := Encode(*inv); err != nil {
		t.Fatal(err)
	}
}
