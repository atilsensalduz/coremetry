package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// copilot_starters_test.go — v0.10.702. İki sözleşme: (1) saf çip üretici
// hatasız/uygunsuz girdide çip üretmez; (2) üretilen HER cümle (veri
// çipleri + FE'deki statik çipler) guided router'da LLM'siz beklenen
// rotaya düşer — çip bir menü değil, çalışan bir soru.
//
// v0.10.1382 — gruplu başlangıç seçenekleri (services / operations / p1 /
// deploys): her grubun saf üreticisi, tavanları, boş grubun yanıta girmemesi,
// v2 cache anahtarının env'i özetlemesi ve her şablonun LLM'siz yönlenmesi
// (operasyon cümlesi: katalog ön-getirmesinin belirteç + birebir eşleşme şekli).
//
// v0.10.1390 — services / operations grupları kalktı (operatör: "filo geneli
// ve böyle birçok servis yazmasın, search kısmı çıksa yeter"); yerlerinde
// arama kutuları. Kutulardan gönderilen İKİ cümle ("<svc> sağlığı nasıl?",
// "<OP> operasyonu neden hata veriyor?") burada yönlenme pinini korur;
// anahtar v3 (pencere girdisi yok).
//
// v0.10.1402 (operatör: "Son deploy etkisini kaldırabiliriz") — deploys grubu
// kalktı (yalnız p1); anahtar v4. deploy_impact rotası sohbette durur.
//
// v0.10.1404 (operatör, prod ekranı: "P1 yerine en son exception'lara
// yönlendirsin.") — p1 grubu kalktı; yerinde exceptions grubu: en son AÇIK
// exception grupları (new/acknowledged/regressed), son görülme yeni önce, en
// çok 3, takım ya da filo; okuma 3 s'de düşerse grup yok. Öğe = açıklama
// niyeti (explainAsk {exception, <parmak izi>, starter}); "Tüm son
// exception'lar" çipinin iki cümlesi (FE capabilityHints) burada yönlenir.
// Anahtar v5. Sentetik adlar.

// excRow — sentetik exception grubu satırı.
func excRow(fp, typ, msg, svc, state string, lastSeen int64) chstore.ExceptionGroup {
	return chstore.ExceptionGroup{Fingerprint: fp, Type: typ, Message: msg, Service: svc, State: state, LastSeen: lastSeen, Occurrences: 9}
}

// excHooks — Oracle grubunun servis kırılımı (istatistik önbelleği yerine sabit).
var excHooks = exGroupHooks{oracleInfo: func(g chstore.ExceptionGroup) *chstore.OracleGroupInfo {
	return &chstore.OracleGroupInfo{Known: true, Code: g.Type, Operation: g.Message, ServiceCount: 2,
		Services: []chstore.OracleNamedNum{{Name: "orders-api", Count: 5}, {Name: "billing-api", Count: 2}}}
}}

func TestStarterExceptionChips(t *testing.T) {
	cases := []struct {
		name      string
		rows      []chstore.ExceptionGroup
		max       int
		wantIDs   []string
		wantChips []string
	}{
		{
			name: "son görülme yeni önce, tavan 3",
			rows: []chstore.ExceptionGroup{
				excRow("fp-a", "java.net.SocketTimeoutException", "read timed out", "orders-api", "new", 100),
				excRow("fp-b", "java.lang.IllegalStateException", "x", "billing-api", "acknowledged", 400),
				excRow("ora:00000000000000a1", "ORA-00060", "ORDERS_GET_X", "oracle:core", "regressed", 300),
				excRow("fp-d", "*net.OpError", "dial", "inventory", "new", 200),
			},
			max:       3,
			wantIDs:   []string{"fp-b", "ora:00000000000000a1", "fp-d"},
			wantChips: []string{"IllegalStateException · billing-api", "ORDERS_GET_X · orders-api", "OpError · inventory"},
		},
		{
			name: "yalnız açık durumlar (ignored / resolved / bilinmeyen düşer)",
			rows: []chstore.ExceptionGroup{
				excRow("fp-ign", "E1", "m", "orders-api", "ignored", 900),
				excRow("fp-res", "E2", "m", "orders-api", "resolved", 800),
				excRow("fp-odd", "E3", "m", "orders-api", "", 700),
				excRow("fp-ok", "E4", "m", "orders-api", "regressed", 100),
			},
			max:       3,
			wantIDs:   []string{"fp-ok"},
			wantChips: []string{"E4 · orders-api"},
		},
		{
			name: "aynı parmak izi bir kez; geçersiz parmak izi düşer; eşit zamanda parmak izi sırası",
			rows: []chstore.ExceptionGroup{
				excRow("fp-z", "E", "m", "orders-api", "new", 500),
				excRow("fp-z", "E", "m", "orders-api", "new", 500),
				excRow("fp bad", "E", "m", "orders-api", "new", 900),
				excRow("", "E", "m", "orders-api", "new", 900),
				excRow("fp-y", "E", "m", "billing-api", "new", 500),
			},
			max:       3,
			wantIDs:   []string{"fp-y", "fp-z"},
			wantChips: []string{"E · billing-api", "E · orders-api"},
		},
		{
			name: "boş girdi → boş dilim (null değil)",
			max:  3,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := starterExceptionChips(c.rows, excHooks, c.max)
			if got == nil {
				t.Fatal("nil yerine boş dilim beklenir")
			}
			if len(got) != len(c.wantIDs) {
				t.Fatalf("çip sayısı %d, want %d: %+v", len(got), len(c.wantIDs), got)
			}
			for i, st := range got {
				if st.Kind != "exception" || st.ExplainAsk == nil || st.ExplainAsk.Kind != explainAskException ||
					st.ExplainAsk.ID != c.wantIDs[i] || st.ExplainAsk.Src != "starter" {
					t.Errorf("[%d] niyet %+v (kind %q), want exception/%s/starter", i, st.ExplainAsk, st.Kind, c.wantIDs[i])
				}
				if st.Chip != c.wantChips[i] {
					t.Errorf("[%d] etiket %q, want %q", i, st.Chip, c.wantChips[i])
				}
				if st.Title == "" || st.Question != st.Chip+" grubunu açıkla" {
					t.Errorf("[%d] başlık/metin: %+v", i, st)
				}
				// Açıklama kademesi niyeti kabul eder (router'a düşmez).
				if _, ok := normalizeExplainAsk(st.ExplainAsk); !ok {
					t.Errorf("[%d] niyet açıklama kademesinde reddedildi: %+v", i, st.ExplainAsk)
				}
			}
		})
	}
	// Tam başlık: span → tam tip; Oracle → kod · operasyon · servisler (+N).
	got := starterExceptionChips([]chstore.ExceptionGroup{
		excRow("fp-a", "java.net.SocketTimeoutException", "m", "orders-api", "new", 2),
		excRow("ora:00000000000000a1", "ORA-00060", "ORDERS_GET_X", "oracle:core", "new", 1),
	}, excHooks, 3)
	if got[0].Title != "java.net.SocketTimeoutException · orders-api" {
		t.Errorf("span başlığı: %q", got[0].Title)
	}
	if got[1].Title != "ORA-00060 · ORDERS_GET_X · orders-api, billing-api" {
		t.Errorf("Oracle başlığı: %q", got[1].Title)
	}
	// Uzun etiket tavanlanır, başlık tam kalır.
	long := strings.Repeat("A", 80)
	lg := starterExceptionChips([]chstore.ExceptionGroup{excRow("fp-l", long, "m", "orders-api", "new", 1)}, exGroupHooks{}, 3)
	if r := []rune(lg[0].Chip); len(r) != startersChipRunes || !strings.HasSuffix(lg[0].Chip, "…") {
		t.Errorf("etiket tavanı: %q", lg[0].Chip)
	}
	if !strings.HasPrefix(lg[0].Title, long) {
		t.Errorf("başlık tam kalmalı: %q", lg[0].Title)
	}
}

func TestStarterExcShortType(t *testing.T) {
	for in, want := range map[string]string{
		"java.net.SocketTimeoutException": "SocketTimeoutException",
		"*net.OpError":                    "OpError",
		"*errors.errorString":             "errorString",
		"503":                             "503",
		"PlainError":                      "PlainError",
		"trailing.":                       "trailing.",
		"":                                "",
	} {
		if got := starterExcShortType(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestStarterExceptionScope(t *testing.T) {
	team := []string{"orders-api", "billing-api"}
	cases := []struct {
		name      string
		scope     []string
		env       string
		members   []string
		membersOK bool
		want      []string
		wantNil   bool
		skip      bool
	}{
		{name: "takım, env yok", scope: team, want: team},
		{name: "filo, env yok → nil (kısıt yok)", scope: nil, wantNil: true},
		{name: "takım ∩ env", scope: team, env: "prod", members: []string{"billing-api", "inventory"}, membersOK: true, want: []string{"billing-api"}},
		{name: "filo + env → env üyeleri", scope: nil, env: "prod", members: []string{"inventory"}, membersOK: true, want: []string{"inventory"}},
		{name: "kesişim boş → grup yok (filoya kaçmaz)", scope: team, env: "uat", members: []string{"inventory"}, membersOK: true, skip: true},
		{name: "env üyesi yok → grup yok", scope: nil, env: "uat", members: nil, membersOK: true, skip: true},
		{name: "üyelik okunamadı → daraltma yok", scope: team, env: "prod", membersOK: false, want: team},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, skip := starterExceptionScope(c.scope, c.env, c.members, c.membersOK)
			if skip != c.skip {
				t.Fatalf("skip %v, want %v (%v)", skip, c.skip, got)
			}
			if c.skip {
				return
			}
			if c.wantNil != (got == nil) || strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("küme %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestStarterExceptionFilter(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	team := []string{"orders-api"}
	f := starterExceptionFilter(team, 5, []string{"fp-multi"}, now)
	if f.State != "open" || f.Sort != "lastSeen" || f.Dir != "desc" || f.Limit != startersExcReadLimit {
		t.Errorf("durum/sıra/tavan: %+v", f)
	}
	if strings.Join(f.Services, ",") != "orders-api" {
		t.Errorf("takım kapsamı: %v", f.Services)
	}
	if f.ActiveFromNs != now.Add(-24*time.Hour).UnixNano() || f.ActiveToNs != now.UnixNano() {
		t.Errorf("pencere 24 sa olmalı: %d..%d", f.ActiveFromNs, f.ActiveToNs)
	}
	if f.MinOccurrences != 5 || !f.FloorExemptRegressed || len(f.FloorExempt) != 1 {
		t.Errorf("taban + muaflar (readTeamExceptions ile aynı): %+v", f)
	}
	// State "open" = chstore buildExceptionGroupWhere'ın new/acknowledged/regressed
	// kovası (ignored/resolved dışarıda; o SQL chstore testlerinde pinli).
	fleet := starterExceptionFilter(nil, 0, []string{"x"}, now)
	if fleet.Services != nil || fleet.FloorExempt != nil || fleet.FloorExemptRegressed {
		t.Errorf("filo: servis kısıtı yok; taban yoksa muaf yok: %+v", fleet)
	}
}

// fakeExcReader — sahte okuyucu: satırlar, hata ya da bağlam bitene dek bekleme.
type fakeExcReader struct {
	rows  []chstore.ExceptionGroup
	err   error
	block bool
	got   chstore.ExceptionGroupFilter
	calls int
}

func (f *fakeExcReader) ListExceptionGroups(ctx context.Context, fl chstore.ExceptionGroupFilter) ([]chstore.ExceptionGroup, error) {
	f.calls++
	f.got = fl
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.rows, f.err
}

func TestReadStarterExceptionsSoftFail(t *testing.T) {
	f := starterExceptionFilter([]string{"orders-api"}, 0, nil, time.Now())
	ok := &fakeExcReader{rows: []chstore.ExceptionGroup{
		excRow("fp-1", "E1", "m", "orders-api", "new", 1),
		excRow("fp-2", "E2", "m", "orders-api", "new", 2),
		excRow("fp-3", "E3", "m", "orders-api", "new", 3),
		excRow("fp-4", "E4", "m", "orders-api", "new", 4),
	}}
	got := readStarterExceptions(context.Background(), ok, f, exGroupHooks{}, time.Second)
	if len(got) != startersExcMax || got[0].ExplainAsk.ID != "fp-4" || ok.calls != 1 || ok.got.State != "open" {
		t.Fatalf("tavan 3, en yeni önce, tek okuma: %+v (calls %d)", got, ok.calls)
	}
	if got := readStarterExceptions(context.Background(), &fakeExcReader{err: errors.New("ch down")}, f, exGroupHooks{}, time.Second); got != nil {
		t.Errorf("okuma hatası → grup yok: %+v", got)
	}
	start := time.Now()
	if got := readStarterExceptions(context.Background(), &fakeExcReader{block: true}, f, exGroupHooks{}, 20*time.Millisecond); got != nil {
		t.Errorf("zaman aşımı → grup yok: %+v", got)
	}
	if time.Since(start) > time.Second {
		t.Error("zaman aşımı bütçesi uygulanmadı")
	}
	if got := readStarterExceptions(context.Background(), &fakeExcReader{}, f, exGroupHooks{}, time.Second); got != nil {
		t.Errorf("boş okuma → grup yok: %+v", got)
	}
	// Grup: boşsa yanıta girmez; fleet bayrağı takım çözülmediğini söyler.
	if gs := starterGroups(true, nil); gs == nil || len(gs) != 0 {
		t.Fatal("boş → boş dizi (null değil), grup yok")
	}
	one := []copilotStarter{{Chip: "E · orders-api", Kind: "exception"}}
	if gs := starterGroups(true, one); len(gs) != 1 || gs[0].Kind != "exceptions" || !gs[0].Fleet {
		t.Fatalf("filo grubu: %+v", gs)
	}
	if gs := starterGroups(false, one); len(gs) != 1 || gs[0].Fleet {
		t.Fatalf("takım çözüldü → fleet yok: %+v", gs)
	}
}

func TestStartersKey(t *testing.T) {
	a := startersKey("u1", "")
	if !strings.HasPrefix(a, "copilot:starters:v5:") {
		t.Fatalf("v5 öneki (v0.10.1404 — p1 grubu yerine exceptions, v4 kayıtları okunmasın): %q", a)
	}
	seen := map[string]string{}
	for _, c := range []struct {
		u   string
		env string
	}{{"u1", ""}, {"u1", "prod"}, {"u1", "uat"}, {"u2", "prod"}, {"u2", ""}} {
		k := startersKey(c.u, c.env)
		if prev, dup := seen[k]; dup {
			t.Fatalf("anahtar çakışması: %+v ile %s", c, prev)
		}
		seen[k] = c.u + "/" + c.env
	}
	if k1, k2 := startersKey("u1", "prod"), startersKey("u1", "prod"+""); k1 != k2 {
		t.Fatal("anahtar kararlı olmalı")
	}
}

// Üretilen cümleler router'da beklenen rotaya düşer. FE'deki statik çipler
// de burada pinli (copilotStarters.test.ts listeyi, bu test yönlenmeyi).
// v0.10.1382 — P1 kök neden ve deploy etkisi şablonları LLM'siz yönlenir.
// v0.10.1404 — P1 şablonu kalktı. Exception grubu çipi router'a GİTMEZ
// (açıklama niyeti — TestStarterExceptionChips normalizeExplainAsk'i pinler);
// "Tüm son exception'lar" çipinin iki cümlesi takım exception cevabına yönlenir.
// v0.10.1390 — başlangıç kartının arama kutuları: servis seçimi "<svc> sağlığı
// nasıl?" (service_health), operasyon seçimi "<OP> operasyonu neden hata
// veriyor?" — guided'dan ÖNCE katalog ön-getirmesinde birebir TEK eşleşme;
// hem Oracle kodu hem span operasyonu (aramanın döndüğü ad şekilleri).
func TestStarterQuestionsRoute(t *testing.T) {
	// v0.10.1404 — "Tüm son exception'lar" (capabilityHints allExceptionsQuestion):
	// takım biliniyorsa adı geçen takım, değilse iyelik.
	for _, c := range []struct {
		q      string
		intent guidedIntent
		team   string
	}{
		{"orders-team takımının exception'ları?", guidedTeamExceptions, "orders-team"},
		{"Takımımın exception'ları?", guidedMyExceptions, ""},
	} {
		// Adı geçen takım dalı takım kataloğuyla açılır — Takımım kartının
		// varsayılan (sunucu) takımla gönderdiği cümleyle AYNI koşul (copilot_team_card_test).
		if r := routeGuidedIntent(c.q, feServices, feEnvs, []string{"orders-team", "platform"}, ""); r.Intent != c.intent || r.Team != c.team {
			t.Errorf("tüm son exception'lar %q → %s/%q; want %s/%q", c.q, r.Intent, r.Team, c.intent, c.team)
		}
	}
	// Servis arama kutusu (capabilityHints serviceHealthQuestion) aynı cümleyi kurar.
	for _, svc := range []string{"inventory", "checkout-service"} {
		q := svc + " sağlığı nasıl?"
		if r := routeGuidedIntent(q, feServices, feEnvs, nil, ""); r.Intent != guidedServiceHealth || r.Service != svc {
			t.Errorf("servis arama seçimi %q → %s/%q", q, r.Intent, r.Service)
		}
	}
	// Operasyon arama kutusu (capabilityHints operationErrorQuestion): aramanın
	// döndüğü her ad (opSearchRoutable) ön-getirme belirteçlerinde kendisi olarak
	// çıkar ve katalogla BİREBİR tek operasyon eşleşir (çoklu seçim / bulanık değil).
	cat := []chstore.CatalogOperation{
		{Operation: "INSURANCE_DOC_PDF", Source: "oracle", CallCount: 9},
		{Operation: "ORDERS_LIST_GET", Source: "oracle", CallCount: 40},
		{Operation: "OrderService.PlaceOrder", Source: "span", Service: "checkout-service", CallCount: 120},
		{Operation: "OrderService.PlaceOrderV2", Source: "span", Service: "checkout-service", CallCount: 80},
		{Operation: "payments/authorize", Source: "span", Service: "payment-service", CallCount: 30},
	}
	for _, op := range []string{"INSURANCE_DOC_PDF", "OrderService.PlaceOrder", "payments/authorize"} {
		if !opSearchRoutable(op) {
			t.Fatalf("%q aramada önerilebilmeli", op)
		}
		q := op + " operasyonu neden hata veriyor?"
		toks := opTokens(q)
		m := matchOperations(toks, cat)
		if m.Kind != "exact" || len(m.Ops) != 1 || m.Ops[0] != op {
			t.Errorf("%q → belirteçler %v, eşleşme %+v; want exact %s", q, toks, m, op)
		}
		if topologyVetoesOpPrefetch(q) {
			t.Errorf("%q ön-getirmeden vetolanmamalı", q)
		}
		if _, _, ok := endpointMention(q); ok {
			t.Errorf("%q endpoint anması sayılmamalı (ön-getirme belirteç yoluna düşer)", q)
		}
	}
	static := []struct {
		q      string
		intent guidedIntent
	}{
		{"Takımımın servisleri nasıl?", guidedMyServices},
		{"Takımımın açık problemleri?", guidedMyProblems},
		{"Takımımın exception'ları?", guidedMyExceptions},
		{"En yavaş trace'ler?", guidedSlowTraces},
		{"Son 1 saatteki log hataları?", guidedLogErrors},
	}
	for _, c := range static {
		if got := routeGuidedIntent(c.q, feServices, feEnvs, nil, ""); got.Intent != c.intent {
			t.Errorf("statik çip %q → %s, beklenen %s", c.q, got.Intent, c.intent)
		}
	}
}
