package api

// v0.10.1380 (operatör bağlamı) — prod'da küme adları "prod-" öneki olmadan ya
// da farklı harf büyüklüğüyle yazılınca ("eu-a", "Kxzone") CoSRE tanımıyordu:
// span deploy_env değerleri "prod-eu-a" / "prod-Kxzone" iken anma kaçıyor,
// küme kıyastan düşüyor ya da yanlış tek ortam seçiliyordu. Adlar SENTETİK.
//
// SÖZLEŞME:
//   - findEnvMentions: tam ad + öneksiz kısa biçim (vmetrics.EnvShortForm, tek
//     tanım), harf duyarsız, SINIRLI ("eu-a", "eu-a-svc" içinde tutmaz);
//     kısa biçim tüm paylaşanlara çözülür (belirsizlik görünür); aynı biçim tam
//     ad ise tam ad kazanır; harf varyantları tek ad (ilk yazım).
//   - extractEnvEntity: tam ad bayt bayt eski; yoksa kısa biçim YALNIZ tek
//     değere çözülüyorsa; belirsizse "".
//   - extractEnvSet: kısa biçim isabetleri canlı değerlere açılır; mesaj
//     sırası, tekil, <2 → nil, ≤6.
//   - guided rota Env / EnvSet ve endpoint küme kıyası opTarget.Envs canlı
//     değerleri taşır; çip "ortam adı çözüldü: eu-a → prod-eu-a".

import (
	"reflect"
	"strings"
	"testing"
)

// ccEnvs — ListEnvironments sırası (en yoğun önce); eu-a iki kümede.
var ccEnvs = []string{"prod-eu-b", "prod-eu-a", "dr-eu-a", "prod-tr-c", "prod-Kxzone"}

func TestFindEnvMentions(t *testing.T) {
	type m = envMention
	for _, c := range []struct {
		name string
		msg  string
		envs []string
		want []envMention
	}{
		{"önek atılmış ad", "eu-b ortamında hata", ccEnvs, []m{{0, "eu-b", []string{"prod-eu-b"}, true}}},
		{"harf duyarsız kısa ad", "KXZONE'da yavaş", ccEnvs, []m{{0, "kxzone", []string{"prod-Kxzone"}, true}}},
		{"harf duyarsız tam ad", "PROD-KXZONE'da yavaş", ccEnvs, []m{{0, "prod-kxzone", []string{"prod-Kxzone"}, false}}},
		{"belirsiz kısa ad iki kümeyi taşır", "eu-a yavaş", ccEnvs, []m{{0, "eu-a", []string{"prod-eu-a", "dr-eu-a"}, true}}},
		{"tam ad kısa ada tercih", "eu-a yavaş", []string{"prod-eu-a", "eu-a"}, []m{{0, "eu-a", []string{"eu-a"}, false}}},
		{"tam ad içinde kısa ad sızmaz", "prod-eu-a yavaş", ccEnvs, []m{{0, "prod-eu-a", []string{"prod-eu-a"}, false}}},
		{"servis adı içinde sızmaz", "eu-a-svc yavaş", ccEnvs, nil},
		{"önek kısa adı sızmaz", "prod yavaş", ccEnvs, nil},
		{"kalan <3 kısa biçim yok", "a ve b", []string{"eu-a", "eu-b"}, nil},
		{"harf varyantları tek ad", "eu-b", []string{"prod-eu-b", "PROD-EU-B"}, []m{{0, "eu-b", []string{"prod-eu-b"}, true}}},
		{"mesaj sırası", "tr-c ve eu-b", ccEnvs, []m{{0, "tr-c", []string{"prod-tr-c"}, true}, {8, "eu-b", []string{"prod-eu-b"}, true}}},
		{"katalog yok", "eu-a", nil, nil},
		// Kademe sözcüğü kısa biçim olamaz (sohbet yolu): canlı "k8s-prod" her "prod"u ortam yapmaz.
		{"kademe sözcüğü: servis soneki", "orders-api-prod yavaş", []string{"k8s-prod"}, nil},
		{"kademe sözcüğü: çıplak prod", "prod'da hata var", []string{"k8s-prod"}, nil},
		{"kademe sözcüğü harf duyarsız", "UAT ortamı", []string{"k8s-UAT", "x-dr"}, nil},
		{"kademe sözcüğü tam adı etkilemez", "k8s-prod'da hata", []string{"k8s-prod"}, []m{{0, "k8s-prod", []string{"k8s-prod"}, false}}},
		{"canlı tam ad prod etkilenmez", "prod'da hata", []string{"prod", "k8s-prod"}, []m{{0, "prod", []string{"prod"}, false}}},
		{"önekli küme hâlâ çözülür", "eu-a yavaş", []string{"prod-eu-a"}, []m{{0, "eu-a", []string{"prod-eu-a"}, true}}},
	} {
		got := findEnvMentions(normalizeGuidedMsg(c.msg), c.envs)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: findEnvMentions(%q) = %+v, want %+v", c.name, c.msg, got, c.want)
		}
	}
}

func TestExtractEnvEntityShortForm(t *testing.T) {
	for _, c := range []struct {
		msg  string
		envs []string
		want string
	}{
		{"eu-b ortamında hatalar", ccEnvs, "prod-eu-b"},
		{"kxzone ortamı sağlıklı mı", ccEnvs, "prod-Kxzone"},
		{"Kxzone'daki hatalar", ccEnvs, "prod-Kxzone"},
		{"eu-a ortamında hatalar", ccEnvs, ""},                                // belirsiz: tahmin yok
		{"eu-a ortamında hatalar", []string{"prod-eu-a", "eu-a"}, "eu-a"},     // tam ad tercih
		{"prod-eu-a ortamında hatalar", ccEnvs, "prod-eu-a"},                  // tam ad bayt bayt eski
		{"uat ortamında, eu-b'de", []string{"uat", "prod-eu-b"}, "uat"},       // tam ad kısa biçimi ezer
		{"eu-a-svc yavaş", ccEnvs, ""},                                        // sızma yok
		{"uat ortamındaki hatalar", []string{"UAT"}, "UAT"},                   // eski satır
		{"eu-b eu-b-x", []string{"prod-eu-b", "PROD-EU-B"}, "prod-eu-b"},      // harf varyantı tek ad
		{"staging ortamındaki hatalar", []string{"prod-eu-a", "dr-eu-a"}, ""}, // katalogda yok
		{"orders-api-prod yavaş", []string{"k8s-prod"}, ""},                   // kademe sözcüğü kısa biçim değil
		{"prod'da hata var", []string{"k8s-prod"}, ""},                        // kademe sözcüğü kısa biçim değil
		{"eu-a ortamında hata", []string{"prod-eu-a"}, "prod-eu-a"},           // önekli küme hâlâ çözülür
	} {
		if got := extractEnvEntity(normalizeGuidedMsg(c.msg), c.envs); got != c.want {
			t.Errorf("extractEnvEntity(%q, %v) = %q, want %q", c.msg, c.envs, got, c.want)
		}
	}
}

func TestExtractEnvSetShortForm(t *testing.T) {
	for _, c := range []struct {
		msg  string
		envs []string
		want []string
	}{
		{"hem eu-b hem tr-c kümelerinde", ccEnvs, []string{"prod-eu-b", "prod-tr-c"}},
		{"eu-a yavaş", ccEnvs, []string{"prod-eu-a", "dr-eu-a"}}, // belirsizlik görünür, sessizce tek değil
		{"tr-c, eu-a ve prod-eu-b", ccEnvs, []string{"prod-tr-c", "prod-eu-a", "dr-eu-a", "prod-eu-b"}},
		{"KXZONE ve eu-b", ccEnvs, []string{"prod-Kxzone", "prod-eu-b"}},
		{"eu-b ve prod-eu-b", ccEnvs, nil}, // aynı küme iki yazım → tek ad
		{"eu-a-svc ve tr-c", ccEnvs, nil},
		{"yalnız eu-b", ccEnvs, nil},
		{"prod ve eu-b", []string{"k8s-prod", "prod-eu-b"}, nil},           // "prod" kısa biçim değil → tek ortam
		{"eu-a, eu-b ve tr-c", wrapEnvs, []string{"eu-a", "eu-b", "tr-c"}}, // tam adlar bayt bayt eski
	} {
		if got := extractEnvSet(normalizeGuidedMsg(c.msg), c.envs); !reflect.DeepEqual(got, c.want) {
			t.Errorf("extractEnvSet(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

// Guided rota: kısa ad süzgece canlı değer olarak gider; belirsiz kısa ad tek
// ortama daralmaz (EnvSet).
func TestGuidedRouteShortEnvName(t *testing.T) {
	r := routeGuidedIntent("orders-api-prod servisinin eu-b ortamındaki hataları", wrapServices, ccEnvs, nil, "")
	if r.Service != "orders-api-prod" || r.Env != "prod-eu-b" || r.EnvSet != nil {
		t.Fatalf("kısa ad: %+v", r)
	}
	r2 := routeGuidedIntent("orders-api-prod servisinin prod-eu-b ortamındaki hataları", wrapServices, ccEnvs, nil, "")
	if r2.Intent != r.Intent || r2.Env != "prod-eu-b" {
		t.Fatalf("kısa ad tam adla aynı rotaya gitmeli: %+v vs %+v", r, r2)
	}
	r = routeGuidedIntent("orders-api-prod servisinin eu-a ortamındaki hataları", wrapServices, ccEnvs, nil, "")
	if r.Service != "orders-api-prod" || r.Env != "" || !reflect.DeepEqual(r.EnvSet, []string{"prod-eu-a", "dr-eu-a"}) {
		t.Fatalf("belirsiz kısa ad: %+v", r)
	}
	// Çip: çözülen canlı değerler görünür; tam adla anmada çip yok.
	norm := normalizeGuidedMsg("eu-a ve kxzone")
	if got := envAliasNoteTR(findEnvMentions(norm, ccEnvs), []string{"", "prod-eu-a", "dr-eu-a", "prod-Kxzone"}); got != "ortam adı çözüldü: eu-a → prod-eu-a, dr-eu-a; kxzone → prod-Kxzone" {
		t.Errorf("çip %q", got)
	}
	if got := envAliasNoteTR(findEnvMentions(normalizeGuidedMsg("kxzone"), ccEnvs), []string{"prod-eu-b"}); got != "" {
		t.Errorf("uygulanmayan değer çipe yazılmamalı: %q", got)
	}
	if got := envAliasNoteTR(findEnvMentions(normalizeGuidedMsg("prod-eu-b"), ccEnvs), []string{"prod-eu-b"}); got != "" {
		t.Errorf("tam adla anmada çip olmamalı: %q", got)
	}
	src := readRepoFile(t, "copilot_guided.go")
	if !strings.Contains(src, "envAliasNoteTR(findEnvMentions(norm, envNames), append([]string{route.Env}, route.EnvSet...))") {
		t.Fatal("guided akış çözüm çipini yaymıyor")
	}
}

// Endpoint küme kıyası: opTarget.Envs canlı TAM değerleri alır.
func TestClusterCompareShortEnvNames(t *testing.T) {
	const msg = "*/shop/.../detail* endpointi hem kuzey(eu-a, eu-b) hem güney(tr-c) kümelerinde çağrılıyor, tekrar eden DB çağrılarına bak"
	envs := []string{"prod-eu-b", "prod-eu-a", "prod-tr-c"}
	want := []string{"prod-eu-a", "prod-eu-b", "prod-tr-c"}
	if got := endpointEnvSet(msg, "/shop/.../detail", envs); !reflect.DeepEqual(got, want) {
		t.Fatalf("endpointEnvSet = %v, want %v", got, want)
	}
	if !wantsClusterCompare(opTarget{Envs: want, Endpoint: true}) {
		t.Fatal("kısa adlarla anılan kümeler kıyası tetiklemeli")
	}
	if got := endpointEnvSet("*/eu-a/orders* hem eu-b hem tr-c'de yavaş", "/eu-a/orders", envs); !reflect.DeepEqual(got, []string{"prod-eu-b", "prod-tr-c"}) {
		t.Errorf("yol parçası ortam sayılmamalı: %v", got)
	}
	if got := endpointEnvAliasNote(msg, "/shop/.../detail", envs, want); got != "ortam adı çözüldü: eu-a → prod-eu-a; eu-b → prod-eu-b; tr-c → prod-tr-c" {
		t.Errorf("küme çipi %q", got)
	}
	if got := endpointEnvAliasNote(wrapMsg2, "/shop/.../detail", wrapEnvs, []string{"eu-a", "eu-b", "tr-c"}); got != "" {
		t.Errorf("tam adlarla çip olmamalı: %q", got)
	}
	src := readRepoFile(t, "chat_operation_prefetch.go")
	if strings.Count(src, "emitEnvAliasStep(emit, endpointEnvAliasNote(raw, path, envs, ") != 2 {
		t.Fatal("endpoint küme kıyası çözüm çipi iki dalda da yayılmalı")
	}
}

// Diğer tüketiciler: analiz ipucu, servis öneki dışlaması, niyet kademesi ve
// açık @env kapsamı kısa adı tanır; belirsiz/uydurma ad yine reddedilir.
func TestShortEnvNameConsumers(t *testing.T) {
	toks := func(s string) []string { return guidedTokens(normalizeGuidedMsg(s)) }
	if !hasAnalysisCue(toks("/orders isteklerini eu-b'de getir"), ccEnvs) {
		t.Error("kısa ortam adı analiz ipucu sayılmalı")
	}
	if hasAnalysisCue(toks("eu-b-svc isteklerini getir"), ccEnvs) {
		t.Error("servis adı içindeki kısa ad ipucu değil")
	}
	if got := extractServiceEntity(normalizeGuidedMsg("eu-b hataları"), []string{"eu-b-gateway"}, ccEnvs); got != "" {
		t.Errorf("kısa ortam adı servis öneki sayılmamalı: %q", got)
	}
	for _, c := range []struct{ v, want string }{
		{"eu-b", "prod-eu-b"}, {"KXZONE", "prod-Kxzone"}, {"eu-a", ""}, {"eu-b ve tr-c", ""}, {"staging", ""},
	} {
		if got := resolveEnvShortName(c.v, ccEnvs); got != c.want {
			t.Errorf("resolveEnvShortName(%q) = %q, want %q", c.v, got, c.want)
		}
	}
	if route, _, ok := parseIntentJSON(`{"intent":"problems","env":"eu-b"}`, wrapServices, ccEnvs, nil, ""); !ok || route.Env != "prod-eu-b" {
		t.Errorf("niyet kademesi kısa ad: ok=%v env=%q", ok, route.Env)
	}
	if _, _, ok := parseIntentJSON(`{"intent":"problems","env":"eu-a"}`, wrapServices, ccEnvs, nil, ""); ok {
		t.Error("belirsiz kısa ad niyet kademesinde reddedilmeli")
	}
	if got, issue := validateChatScope(chatScope{Env: "eu-b"}, nil, ccEnvs, nil); issue != nil || got.Env != "prod-eu-b" {
		t.Errorf("@env kısa ad: %+v %+v", got, issue)
	}
	if _, issue := validateChatScope(chatScope{Env: "eu-a"}, nil, ccEnvs, nil); issue == nil || !reflect.DeepEqual(issue.Options, []string{"dr-eu-a", "prod-eu-a"}) {
		t.Errorf("@env belirsiz kısa ad seçenek sormalı: %+v", issue)
	}
}
