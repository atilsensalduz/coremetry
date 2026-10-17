package mcptools

// v0.10.1274 — CoSRE adım 5: sayfa bağlamı + env araçlara.
//
// Env-kör listesinden çıkan araçların env yolu (servis-üyeliği kapsamı,
// log backend env alanı) ve kalanların "env uygulanmadı" kayıt defteri.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestScopeServices(t *testing.T) {
	applied := envScope{Env: "uat", Applied: true, Members: []string{"a", "b"}}
	cases := []struct {
		name    string
		service string
		es      envScope
		want    []string
	}{
		{"env yok, servis yok → kısıt yok", "", envScope{}, nil},
		{"env yok, servis var", "a", envScope{}, []string{"a"}},
		{"env uygulanamadı → yalnız servis", "a", envScope{Env: "uat"}, []string{"a"}},
		{"env, servis yok → üyeler", "", applied, []string{"a", "b"}},
		{"env ∩ servis (üye)", "b", applied, []string{"b"}},
		{"env ∩ servis (üye değil) → boş (hiçbir şey)", "z", applied, []string{}},
		{"env boş üyelik → boş", "", envScope{Env: "x", Applied: true, Members: []string{}}, []string{}},
	}
	for _, c := range cases {
		got := scopeServices(c.service, c.es)
		if !reflect.DeepEqual(got, c.want) || (got == nil) != (c.want == nil) {
			t.Errorf("%s: got %#v want %#v", c.name, got, c.want)
		}
	}
}

func TestResolveEnvMembers(t *testing.T) {
	ctx := context.Background()
	if es := resolveEnvMembers(ctx, Deps{}, "  "); es.Env != "" || es.Applied {
		t.Fatalf("boş env çözülmemeli: %+v", es)
	}
	// Store yok, dikiş yok → uygulanamadı (soft-fail, süzgeçsiz) + not.
	es := resolveEnvMembers(ctx, Deps{}, "uat")
	if es.Applied || !strings.Contains(es.Note, "uygulanamadı") {
		t.Fatalf("haritasız env uygulanmış sayıldı: %+v", es)
	}
	d := Deps{EnvMembers: func(_ context.Context, env string) ([]string, error) {
		if env == "bad" {
			return nil, errors.New("ch blip")
		}
		return nil, nil // çözüldü ama boş
	}}
	if es := resolveEnvMembers(ctx, d, "bad"); es.Applied {
		t.Fatal("hata yolunda env uygulanmış sayıldı")
	}
	es = resolveEnvMembers(ctx, d, "uat")
	if !es.Applied || es.Members == nil || len(es.Members) != 0 {
		t.Fatalf("çözüldü-ama-boş ≠ kısıt yok: %+v", es)
	}
	body := map[string]any{}
	es.stamp(body)
	if body["env"] != "uat" || body["env_scope"] != EnvScopeServiceMembership || body["env_note"] == "" {
		t.Fatalf("künye eksik: %v", body)
	}
	body2 := map[string]any{}
	envScope{}.stamp(body2)
	if len(body2) != 0 {
		t.Fatalf("env yokken künye yazıldı: %v", body2)
	}
}

// list_anomalies — env SQL'in Services yuvasına (ListAnomalyEvents onu
// `service IN (…)` olarak derler; anomaly_silence_exclude_test pinli).
func TestAnomalyToolFilterEnv(t *testing.T) {
	es := envScope{Env: "prod", Applied: true, Members: []string{"checkout", "cart"}}
	f := anomalyToolFilter(42, 25, "", es)
	if !reflect.DeepEqual(f.Services, []string{"checkout", "cart"}) || f.Limit != 25 || f.SinceNs != 42 {
		t.Fatalf("env üyeliği Services'e inmedi: %+v", f)
	}
	f2 := anomalyToolFilter(42, 25, "", envScope{})
	if f2.Services != nil {
		t.Fatalf("env yokken kısıt eklendi: %+v", f2)
	}
	// Farklı env → farklı filtre (ve farklı tekrar-muhafızı anahtarı).
	f3 := anomalyToolFilter(42, 25, "", envScope{Env: "uat", Applied: true, Members: []string{"cart"}})
	if reflect.DeepEqual(f.Services, f3.Services) {
		t.Fatal("env değişti ama filtre aynı")
	}
}

func TestEnvScopeProblems(t *testing.T) {
	probs := []chstore.Problem{
		{ID: "1", Service: "checkout"},
		{ID: "2", Service: "other"},
		{ID: "3", Service: ""},                                         // global kural
		{ID: "4", Service: "db:oracle@x", Kind: chstore.ProblemKindDB}, // db öznesi
	}
	got := envScopeProblems(probs, envScope{Env: "prod", Applied: true, Members: []string{"checkout"}})
	var ids []string
	for _, p := range got {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "1,3,4" {
		t.Fatalf("servis-üyeliği kuralı /problems'tan ayrıştı: %v", ids)
	}
	if len(envScopeProblems(probs, envScope{})) != 4 {
		t.Fatal("env yokken satır düştü")
	}
	if len(probs) != 4 || probs[1].ID != "2" {
		t.Fatal("girdi dilimi değiştirildi")
	}
}

func TestDeployFetchLimit(t *testing.T) {
	for _, c := range []struct {
		limit   int
		applied bool
		want    int
	}{{20, false, 20}, {20, true, 80}, {100, true, deployEnvFetchCap}, {100, false, 100}} {
		if got := deployFetchLimit(c.limit, c.applied); got != c.want {
			t.Errorf("deployFetchLimit(%d,%v)=%d want %d", c.limit, c.applied, got, c.want)
		}
	}
}

func TestLogHistEnvStamp(t *testing.T) {
	for _, c := range []struct {
		name string
		r    logstore.FieldResolution
		want any
		note bool
	}{
		{"alan bulundu", logstore.FieldResolution{Source: "configured", Field: "env"}, true, false},
		{"alan yok", logstore.FieldResolution{Source: logstore.FieldNone}, false, true},
		{"doğrulanamadı", logstore.FieldResolution{Source: logstore.FieldUnverified}, "unverified", true},
	} {
		b := map[string]any{}
		logHistEnvStamp(b, "uat", c.r)
		if b["env"] != "uat" || b["env_applied"] != c.want {
			t.Errorf("%s: %v", c.name, b)
		}
		if _, has := b["env_note"]; has != c.note {
			t.Errorf("%s: not beklentisi %v, gövde %v", c.name, c.note, b)
		}
	}
}

// Kayıt defteri ↔ katalog: her env-kör araç gerçekten var ve env
// property'si YOK; env'li araç kayıt defterinde değil.
func TestEnvBlindRegistryMatchesCatalogue(t *testing.T) {
	tools := ToolList(Deps{})
	for _, name := range EnvBlindTools() {
		props := schemaProps(t, toolByName(t, tools, name))
		if _, has := props["env"]; has {
			t.Errorf("%s: env property var ama env-kör kayıt defterinde", name)
		}
	}
	for _, tool := range tools {
		props, _ := tool.InputSchema["properties"].(map[string]any)
		if _, has := props["env"]; has {
			if _, blind := EnvBlindReason(tool.Name); blind {
				t.Errorf("%s: env'li araç env-kör sayılıyor", tool.Name)
			}
		}
	}
}

// ScopeApplied — omitempty: boş künye `{}`; eski istemci şekli değişmez.
func TestScopeAppliedJSONOmitEmpty(t *testing.T) {
	b, _ := json.Marshal(ScopeApplied{})
	if string(b) != "{}" {
		t.Fatalf("boş künye %s", b)
	}
	if !(ScopeApplied{}).Empty() || (ScopeApplied{Env: "x"}).Empty() {
		t.Fatal("Empty yanlış")
	}
	b, _ = json.Marshal(ChatEvidence{Tool: "query_logs"})
	if strings.Contains(string(b), "ctxApplied") {
		t.Fatalf("künyesiz kanıtta ctxApplied yazıldı: %s", b)
	}
}
