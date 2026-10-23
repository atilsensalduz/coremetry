package api

// chat_local_only_switch_test.go — v0.10.1467 (localOnly ardılları, operatör
// kararı 2026-10-23): (a) kod/wiki turu kullanıcının yerel OLMAYAN profilinden
// işaretli yerel profile taşındığında görünür step; geçiş yoksa step yok; kod
// turu (v0.10.1461) kendi "Kod okuma: …" step'ine ek alır, ikinci step yok.
// (b) yüklenen belgeler karar 8 DIŞINDA: tiksiz kurulumda yüklenen belge RAG'i
// çalışır, anlatım işaretsiz.

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

func switchCount(labels []string) int {
	n := 0
	for _, l := range labels {
		if strings.HasPrefix(l, "Yerel modele geçildi: ") {
			n++
		}
	}
	return n
}

func TestLocalOnlySwitchLabel(t *testing.T) {
	if got := localOnlySwitchLabel("Yerel Gemma"); got != "Yerel modele geçildi: Yerel Gemma (kod/wiki içeriği yalnız yerel modele gider)" {
		t.Fatalf("etiket: %q", got)
	}
	ct := codeTurn{On: true, Label: "Yerel Gemma", Switched: true}
	if l := ct.stepLabel(true); l != "Kod okuma: Yerel Gemma · yerel modele geçildi (kod/wiki içeriği yalnız yerel modele gider) · dış araçlar bu turda kapalı" {
		t.Fatalf("kod turu + geçiş: %q", l)
	}
	ct.Switched = false
	if l := ct.stepLabel(false); l != "Kod okuma: Yerel Gemma" {
		t.Fatalf("geçişsiz kod turu bayt bayt eski olmalı: %q", l)
	}
}

func switchTestService(defaultLocal bool) *copilot.Service {
	cop := copilot.New(copilot.ProviderOpenAI, "k", "qwen3-32b")
	cop.SetProfiles([]copilot.ModelProfile{
		{ID: copilot.DefaultProfileID, Provider: copilot.ProviderOpenAI, APIKey: "k", Model: "qwen3-32b", BaseURL: "http://llm-a.example.test/v1", LocalOnly: defaultLocal},
		{ID: "loc", Label: "Yerel Gemma", Provider: copilot.ProviderOpenAI, APIKey: "k", Model: "gemma4-31b", BaseURL: "http://llm-b.example.test/v1", LocalOnly: true},
	}, copilot.DefaultProfileID, nil)
	return cop
}

// routeSensitiveStep: yerel olmayan çözülen profil → bir step (hedef profil
// etiketiyle); aynı turda ikinci çağrı (ctx artık yerel) → step yok; çözülen
// profil zaten yerel → step yok; yerel profil yok → ok=false, step yok.
func TestRouteSensitiveStepOnlyOnSwitch(t *testing.T) {
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-1", Role: auth.RoleEditor})
	s := &Server{copilot: switchTestService(false)}
	ev, emit := collectEmit()
	rctx, ok := s.routeSensitiveStep(ctx, copilot.SensitiveWiki, emit)
	if !ok || s.copilot.ProfileIDFor(rctx) != "loc" || copilot.SensitiveFromContext(rctx) != copilot.SensitiveWiki {
		t.Fatalf("geçiş: ok=%v profil=%q", ok, s.copilot.ProfileIDFor(rctx))
	}
	if len(*ev) != 1 || (*ev)[0].kind != "step" || (*ev)[0].v.(map[string]string)["label"] != localOnlySwitchLabel("Yerel Gemma") {
		t.Fatalf("geçiş step'i: %+v", *ev)
	}
	if _, ok := s.routeSensitiveStep(rctx, copilot.SensitiveWiki, emit); !ok || len(*ev) != 1 {
		t.Fatalf("aynı turda ikinci çağrı step üretmemeli: %+v", *ev)
	}
	// Kullanıcı profili açıkça seçti (yerel olmayan) → yine geçiş + step.
	ev2, emit2 := collectEmit()
	if _, ok := s.routeSensitiveStep(copilot.WithProfile(ctx, copilot.DefaultProfileID), copilot.SensitiveCode, emit2); !ok || len(*ev2) != 1 {
		t.Fatalf("seçili yerel olmayan profil: %+v", *ev2)
	}
	// Kullanıcı yerel profili seçmiş → geçiş yok.
	ev3, emit3 := collectEmit()
	if _, ok := s.routeSensitiveStep(copilot.WithProfile(ctx, "loc"), copilot.SensitiveCode, emit3); !ok || len(*ev3) != 0 {
		t.Fatalf("seçili yerel profil step üretmemeli: %+v", *ev3)
	}
	// Çözülen varsayılan profil yerel → geçiş yok.
	sl := &Server{copilot: switchTestService(true)}
	ev4, emit4 := collectEmit()
	if _, ok := sl.routeSensitiveStep(ctx, copilot.SensitiveWiki, emit4); !ok || len(*ev4) != 0 {
		t.Fatalf("yerel varsayılan step üretmemeli: %+v", *ev4)
	}
	// Yerel profil yok → ok=false, step yok.
	none := copilot.New(copilot.ProviderOpenAI, "k", "qwen3-32b")
	none.Configure(copilot.ProviderOpenAI, "k", "qwen3-32b", "http://llm-a.example.test/v1", false, true)
	ev5, emit5 := collectEmit()
	if _, ok := (&Server{copilot: none}).routeSensitiveStep(ctx, copilot.SensitiveWiki, emit5); ok || len(*ev5) != 0 {
		t.Fatalf("yerel profil yokken: ok=%v %+v", ok, *ev5)
	}
	// nil emit güvenli.
	if _, ok := s.routeSensitiveStep(ctx, copilot.SensitiveWiki, nil); !ok {
		t.Fatal("nil emit")
	}
}

// Serbest döngü (trace takibi, kod aracı katalogda): yerel olmayan varsayılan
// → tek geçiş step'i; yerel varsayılan → step yok.
func TestChatFreeLoopSwitchStep(t *testing.T) {
	for _, defLocal := range []bool{false, true} {
		ext := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("dış") })
		loc := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("yerel") })
		s, _ := newLoopTestServer(t, ext, "u-a")
		s.copilot.SetProfiles([]copilot.ModelProfile{
			{ID: copilot.DefaultProfileID, Provider: copilot.ProviderOpenAI, APIKey: "test-key", Model: "qwen3-32b", BaseURL: ext.srv.URL, LocalOnly: defLocal},
			{ID: "loc", Label: "Yerel Gemma", Provider: copilot.ProviderOpenAI, APIKey: "test-key", Model: "gemma4-31b", BaseURL: loc.srv.URL, LocalOnly: true},
		}, copilot.DefaultProfileID, nil)
		_, s.devops = newSCGit(t)
		frames, _ := scPost(t, s, "u-a", auth.RoleEditor, traceDrawerBody("Özetle?", anchoredToMs(), "conv-sw"))
		labels := stepLabels(frames)
		want := 1
		if defLocal {
			want = 0
		}
		if got := switchCount(labels); got != want {
			t.Fatalf("defaultLocal=%v: geçiş step'i %d (beklenen %d): %v", defLocal, got, want, labels)
		}
		if !defLocal && !strings.Contains(strings.Join(labels, "\n"), localOnlySwitchLabel("Yerel Gemma")) {
			t.Fatalf("etiket hedef profili taşımalı: %v", labels)
		}
	}
}

// Kod turu: geçiş kod turunun KENDİ step'ine eklenir; ayrı "Yerel modele
// geçildi:" step'i yok. Kullanıcı yerel profili seçmişse ek de yok.
func TestCodeTurnSwitchMergedIntoCodeStep(t *testing.T) {
	for _, pick := range []string{copilot.DefaultProfileID, "loc"} {
		ext := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("dış") })
		loc := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("yerel") })
		s, _ := newLoopTestServer(t, ext, "u-a")
		s.copilot.SetProfiles([]copilot.ModelProfile{
			{ID: copilot.DefaultProfileID, Provider: copilot.ProviderOpenAI, APIKey: "test-key", Model: "qwen3-32b", BaseURL: ext.srv.URL},
			{ID: "loc", Label: "Yerel Gemma", Provider: copilot.ProviderOpenAI, APIKey: "test-key", Model: "gemma4-31b", BaseURL: loc.srv.URL, LocalOnly: true},
		}, copilot.DefaultProfileID, nil)
		s.copilot.SetIntentClassify(copilot.IntentOff)
		_, s.devops = newSCGit(t)
		frames, _ := scPost(t, s, "u-a", auth.RoleEditor, ctBody("ChargeHandler sınıfına bak", true, map[string]any{"profile": pick}))
		labels := stepLabels(frames)
		joined := strings.Join(labels, "\n")
		if switchCount(labels) != 0 {
			t.Fatalf("%s: kod turunda ayrı geçiş step'i (çift): %v", pick, labels)
		}
		merged := strings.Contains(joined, "Kod okuma: Yerel Gemma · yerel modele geçildi (kod/wiki içeriği yalnız yerel modele gider)")
		if merged != (pick == copilot.DefaultProfileID) {
			t.Fatalf("%s: birleşik geçiş eki=%v: %v", pick, merged, labels)
		}
		if len(ext.calls()) != 0 {
			t.Fatalf("%s: yerel olmayan profile istek", pick)
		}
	}
}

// Karar b (v0.10.1467, operatör 2026-10-23): yüklenen belgeler karar 8
// kapsamı DIŞINDA. Hiçbir profil yerel işaretli değilken yüklenen belge RAG'i
// cevap verir; anlatım işaretsiz (yerel profile zorlanmaz) ve geçiş step'i yok.
func TestUploadedDocRAGWorksWithoutLocalProfile(t *testing.T) {
	const docText = "# kxzone envanteri\n\nkxzone-gw01 sunucusu orders-api uygulamasını çalıştırır; bakım penceresi pazar 02:00."
	st := &docIndexStore{fakeWikiStore: newFakeWikiStore(), text: docText}
	w := wiki.New(st, func() wiki.API { return &fakeWikiAPI{pages: map[string]string{}} }, nil)
	w.Configure(wiki.Config{Enabled: true})
	prevW := wikiKB()
	SetWiki(w)
	t.Cleanup(func() { SetWiki(prevW) })
	if err := w.IndexLocalDoc(context.Background(), "kxzone-envanter.md", docText, ""); err != nil {
		t.Fatal(err)
	}
	var narrated []context.Context
	prev := wikiNarrateFn
	wikiNarrateFn = func(_ *Server, ctx context.Context, _, user string) (string, error) {
		narrated = append(narrated, ctx)
		return "kxzone-gw01 orders-api'yi çalıştırır.", nil
	}
	t.Cleanup(func() { wikiNarrateFn = prev })
	cop := copilot.New(copilot.ProviderOpenAI, "k", "qwen3-32b")
	cop.Configure(copilot.ProviderOpenAI, "k", "qwen3-32b", "http://llm-a.example.test/v1", false, true)
	s := &Server{copilot: cop}
	if s.localOnlyAvailable(sessionCtx()) {
		t.Fatal("fikstür: yerel profil olmamalı")
	}
	ev, emit := collectEmit()
	h, ok := s.ragChatAnswer(sessionCtx(), emit, []copilot.ChatMessage{{Role: "user", Text: "kxzone-gw01 sunucusunda hangi uygulama var?"}}, "")
	if !h || !ok {
		t.Fatalf("yüklenen belge RAG'i cevaplamadı: handled=%v ok=%v ev=%+v", h, ok, *ev)
	}
	if len(narrated) == 0 {
		t.Fatal("anlatım çağrılmadı")
	}
	for _, c := range narrated {
		if k := copilot.SensitiveFromContext(c); k != "" {
			t.Fatalf("yüklenen belge anlatımı işaretli (%q) — karar 8 dışı olmalı", k)
		}
	}
	for _, e := range *ev {
		if m, _ := e.v.(map[string]string); e.kind == "step" && strings.HasPrefix(m["label"], "Yerel modele geçildi") {
			t.Fatalf("yüklenen belgede geçiş step'i: %+v", *ev)
		}
	}
}

// docIndexStore — tek yüklenen belgeyi her terimde aday döndüren bellek-içi
// indeks (BM25 girdileri: her jeton TF=1, DF=1, N=1).
type docIndexStore struct {
	*fakeWikiStore
	text string
}

func (d *docIndexStore) WikiTermStats(_ context.Context, terms []string, _ string) (wiki.Stats, error) {
	df := make([]uint64, len(terms))
	for i := range df {
		df[i] = 1
	}
	return wiki.Stats{N: 1, AvgDL: 20, DF: df}, nil
}

func (d *docIndexStore) WikiCandidates(_ context.Context, q wiki.CandidateQuery, project string, _ int) ([]wiki.Candidate, error) {
	if project != "" && !wiki.IsLocalProject(project) {
		return nil, nil
	}
	tf := make([]uint32, len(q.Tokens))
	for i, tok := range q.Tokens {
		if strings.Contains(wiki.Fold(d.text), wiki.Fold(tok)) {
			tf[i] = 1
		}
	}
	return []wiki.Candidate{{
		ChunkRef: wiki.ChunkRef{Project: wiki.LocalProject, WikiID: wiki.LocalWikiID, WikiName: wiki.LocalWikiName,
			Path: wiki.LocalDocPath("kxzone-envanter.md"), Title: "kxzone-envanter", Text: d.text},
		TF: tf, HTF: make([]uint32, len(q.Tokens)), DL: 20,
	}}, nil
}

// Yapışkan kod konuşması (v0.10.1465): geçiş "Kod okunan konuşma" step'ine
// ek olarak girer, ayrı "Yerel modele geçildi:" step'i yok; yerel varsayılanda ek yok.
func TestCodeStickySwitchMergedIntoStickyStep(t *testing.T) {
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-1", Role: auth.RoleEditor})
	for _, defLocal := range []bool{false, true} {
		s := &Server{copilot: switchTestService(defLocal)}
		ev, emit := collectEmit()
		if _, sticky, closed := s.applyCodeSticky(ctx, emit, nil, true); !sticky || closed {
			t.Fatalf("yapışkan tur: sticky=%v closed=%v", sticky, closed)
		}
		var labels []string
		for _, e := range *ev {
			if m, ok := e.v.(map[string]string); ok && e.kind == "step" {
				labels = append(labels, m["label"])
			}
		}
		if len(labels) != 1 || switchCount(labels) != 0 {
			t.Fatalf("defaultLocal=%v: tek yapışkan step beklenir: %v", defLocal, labels)
		}
		if got := strings.Contains(labels[0], "yerel modele geçildi"); got == defLocal || !strings.HasPrefix(labels[0], codeStickyStepPrefix+": ") {
			t.Fatalf("defaultLocal=%v: etiket %q", defLocal, labels[0])
		}
	}
}
