package rollout

// nsfilter_test.go — v0.10.1262 — operatör 2026-10-17: "OpenShift'e ait
// projeler rollout listelemesin" (metrics-server · openshift-monitoring,
// controller-manager · openshift-controller-manager, ocs-metrics-exporter ·
// openshift-storage canlı listeyi dolduruyordu). Saf yardımcıların tablo
// testleri: glob eşleyici (SQL ile aynı RE2), normalize, doğrulama, ayar
// çözümü (nil = varsayılan, [] = dışlama yok), açık süzgecin önceliği.

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"
)

func TestNamespaceExcludedDefaults(t *testing.T) {
	def := DefaultExcludeNamespaces
	for _, tc := range []struct {
		ns   string
		want bool
	}{
		{"openshift", true},
		{"openshift-monitoring", true},
		{"openshift-controller-manager", true},
		{"openshift-route-controller-manager", true},
		{"openshift-storage", true},
		{"openshift-console", true},
		{"openshift-authentication", true}, // oauth-openshift burada koşar
		{"kube-system", true},
		{"kube-public", true},
		{"kube-node-lease", true},
		{"default", false},          // bilerek gizlenmez
		{"payments", false},         // uygulama
		{"my-openshift-app", false}, // önek değil — tam eşleşme
		{"openshiftx", false},       // "openshift" tam ad, "openshift-*" tireli
		{"kube", false},             // "kube-*" tire ister
		{"kubernetes-dashboard", false},
		{"", false},
	} {
		if got := NamespaceExcluded(def, tc.ns); got != tc.want {
			t.Errorf("NamespaceExcluded(default, %q) = %v, want %v", tc.ns, got, tc.want)
		}
	}
}

func TestNamespaceGlobSemantics(t *testing.T) {
	for _, tc := range []struct {
		patterns []string
		ns       string
		want     bool
	}{
		{[]string{"team-?"}, "team-a", true},
		{[]string{"team-?"}, "team-ab", false},
		{[]string{"*-infra"}, "dc1-infra", true},
		{[]string{"*-infra"}, "dc1-infra-x", false},
		{[]string{"a.b"}, "a.b", true},
		{[]string{"a.b"}, "axb", false}, // nokta RE'ye kaçırılır (QuoteMeta)
		{[]string{"*"}, "anything", true},
		{[]string{" OpenShift-* "}, "openshift-x", true}, // kırp + küçült
		{nil, "openshift", false},
		{[]string{}, "kube-system", false},
		{[]string{"", "  "}, "kube-system", false},
	} {
		if got := NamespaceExcluded(tc.patterns, tc.ns); got != tc.want {
			t.Errorf("NamespaceExcluded(%q, %q) = %v, want %v", tc.patterns, tc.ns, got, tc.want)
		}
	}
}

func TestNamespaceExcludeRegex(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"  "}, ""},
		{[]string{"openshift-*", "openshift", "kube-*"}, `^(?:kube-.*|openshift|openshift-.*)$`},
		{[]string{"kube-*", "KUBE-*"}, `^(?:kube-.*)$`}, // tekil
		{[]string{"a.b?"}, `^(?:a\.b.)$`},
	} {
		got := NamespaceExcludeRegex(tc.in)
		if got != tc.want {
			t.Errorf("NamespaceExcludeRegex(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got != "" {
			if _, err := regexp.Compile(got); err != nil {
				t.Errorf("RE derlenmiyor %q: %v", got, err)
			}
		}
	}
}

func TestValidateNamespacePatterns(t *testing.T) {
	for _, tc := range []struct {
		in []string
		ok bool
	}{
		{[]string{"openshift", "openshift-*", "kube-*"}, true},
		{[]string{"team-?", "*.infra", ""}, true},
		{[]string{"Openshift-*"}, true}, // küçültülerek değerlendirilir
		{[]string{"ns with space"}, false},
		{[]string{"ns|other"}, false}, // RE enjeksiyonu
		{[]string{"ns[0-9]"}, false},
		{[]string{"x/y"}, false},
		{[]string{"a234567890123456789012345678901234567890123456789012345678901234"}, false}, // 64 > 63
	} {
		err := ValidateNamespacePatterns(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("ValidateNamespacePatterns(%q) err=%v, ok beklenen %v", tc.in, err, tc.ok)
		}
	}
	many := make([]string, excludeNamespacesMax+1)
	for i := range many {
		many[i] = "ns"
	}
	if ValidateNamespacePatterns(many) == nil {
		t.Error("desen tavanı aşıldı ama hata yok")
	}
	bad := []string{"bad pattern"}
	if ValidateSettings(Settings{ExcludeNamespaces: &bad}) == nil {
		t.Error("ValidateSettings geçersiz deseni geçirdi")
	}
}

func TestResolvedExcludeNamespaces(t *testing.T) {
	empty := []string{}
	custom := []string{"kube-*", " team-x "}
	for _, tc := range []struct {
		name string
		s    Settings
		want []string
	}{
		{"alan yok → varsayılan", Settings{}, []string{"kube-*", "openshift", "openshift-*"}},
		{"DefaultSettings", DefaultSettings(), []string{"kube-*", "openshift", "openshift-*"}},
		{"boş liste → dışlama yok", Settings{ExcludeNamespaces: &empty}, []string{}},
		{"özel", Settings{ExcludeNamespaces: &custom}, []string{"kube-*", "team-x"}},
	} {
		if got := tc.s.ResolvedExcludeNamespaces(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	// JSON gidiş-dönüşü: [] korunur (omitempty pointer'ı düşürmez), alan yok = nil.
	raw, _ := json.Marshal(Settings{ExcludeNamespaces: &empty})
	var back Settings
	_ = json.Unmarshal(raw, &back)
	if back.ExcludeNamespaces == nil || len(*back.ExcludeNamespaces) != 0 {
		t.Fatalf("[] gidiş-dönüşte kayboldu: %s", raw)
	}
	var old Settings
	_ = json.Unmarshal([]byte(`{"enabled":true}`), &old)
	if old.ExcludeNamespaces != nil {
		t.Fatal("eski blob (alan yok) nil kalmalı → varsayılan")
	}
	// Varsayılan dilim paylaşılmaz: DefaultSettings kopyası değişse de paket varsayılanı aynı.
	d := DefaultSettings()
	(*d.ExcludeNamespaces)[0] = "mutated"
	if DefaultExcludeNamespaces[0] != "openshift" {
		t.Fatal("DefaultSettings paket varsayılanını paylaşıyor")
	}
}

func TestEffectiveNamespaceExclude(t *testing.T) {
	p := []string{"openshift-*"}
	for _, tc := range []struct {
		explicit string
		want     []string
	}{
		{"", p},
		{"   ", p},
		{"openshift-monitoring", nil}, // açık süzgeç kazanır
		{"payments", nil},
	} {
		if got := EffectiveNamespaceExclude(p, tc.explicit); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("EffectiveNamespaceExclude(%q) = %q, want %q", tc.explicit, got, tc.want)
		}
	}
}
