package logstore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// v0.10.1477 — /logs `team=` süzgeci: yapılandırılmış takım alan(lar)ı ×
// çözülmüş değerler üzerinde exact-term OR (TeamRecentLogs'un yan tümcesi),
// filtre bağlamında; alan yoksa / CH'de uygulanmaz ve itiraf edilir.

func TestTeamFilterValues(t *testing.T) {
	cases := []struct {
		name string
		f    Filter
		want []string
	}{
		{"takım yok", Filter{}, nil},
		{"değer yok → takım adı (boş IN = filo DEĞİL)", Filter{Team: " orders-team "}, []string{"orders-team"}},
		{"değerler kırpılır + tekil", Filter{Team: "orders-team", TeamValues: []string{" orders-team", "Orders-Team", "orders-team", ""}}, []string{"orders-team", "Orders-Team"}},
		{"yalnız boş değerler → takım adı", Filter{Team: "x", TeamValues: []string{" ", ""}}, []string{"x"}},
	}
	for _, c := range cases {
		if got := TeamFilterValues(c.f); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	many := make([]string, 0, 25)
	for i := 0; i < 25; i++ {
		many = append(many, "t"+strings.Repeat("x", i))
	}
	if got := TeamFilterValues(Filter{Team: "t", TeamValues: many}); len(got) != TeamLogsMaxValues {
		t.Errorf("değer tavanı %d olmalı, got %d", TeamLogsMaxValues, len(got))
	}
}

// Tablo: alanlar × değerler → yan tümce. Her alan için iki biçim (.keyword
// terms + exists-korumalı çıplak terms), hepsi tek should altında.
func TestESTeamFilterClauseTable(t *testing.T) {
	cases := []struct {
		name      string
		teamField string
		f         Filter
		applied   bool
		wantNil   bool
		fields    []string
		values    []string
	}{
		{"takım istenmedi", "kubernetes.labels.team-a", Filter{}, true, true, nil, nil},
		{"alan yapılandırılmamış", "", Filter{Team: "orders-team"}, false, true, nil, nil},
		{"geçersiz alan (wildcard)", "kubernetes.labels.*", Filter{Team: "orders-team"}, false, true, nil, nil},
		{"tek alan × tek değer", "kubernetes.labels.team-a", Filter{Team: "orders-team"}, true, false,
			[]string{"kubernetes.labels.team-a"}, []string{"orders-team"}},
		{"iki alan × iki yazım", "kubernetes.labels.team-a, kubernetes.labels.team-b.keyword",
			Filter{Team: "orders-team", TeamValues: []string{"orders-team", "ORDERS"}}, true, false,
			[]string{"kubernetes.labels.team-a", "kubernetes.labels.team-b"}, []string{"orders-team", "ORDERS"}},
	}
	for _, c := range cases {
		cl, ok := esTeamFilter(c.f, c.teamField)
		if ok != c.applied {
			t.Errorf("%s: applied=%v want %v", c.name, ok, c.applied)
		}
		if (cl == nil) != c.wantNil {
			t.Fatalf("%s: clause nil=%v want %v", c.name, cl == nil, c.wantNil)
		}
		if cl == nil {
			continue
		}
		want := teamValueClause(c.fields, c.values)
		if !reflect.DeepEqual(cl, want) {
			t.Errorf("%s: clause\n got %v\nwant %v", c.name, cl, want)
		}
		raw, _ := json.Marshal(cl)
		q := string(raw)
		for _, bad := range []string{"query_string", "wildcard", "prefix", "regexp", "*"} {
			if strings.Contains(q, bad) {
				t.Errorf("%s: takım yan tümcesi %q taşımamalı: %s", c.name, bad, q)
			}
		}
		should := cl["bool"].(map[string]any)["should"].([]any)
		if len(should) != 2*len(c.fields) {
			t.Errorf("%s: alan başına iki biçim beklenir, got %d", c.name, len(should))
		}
	}
}

// buildQuery: takım yan tümcesi FİLTRE bağlamında (must'ta değil) ve
// TeamRecentLogs gövdesindekiyle bayt-bayt aynı.
func TestBuildQueryTeamFilterContext(t *testing.T) {
	s := newTestESStore(ESFieldMap{Team: "kubernetes.labels.team-a,kubernetes.labels.team-b"})
	f := Filter{Team: "orders-team", TeamValues: []string{"orders-team", "Orders"}}
	q := s.buildQuery(f)
	b := q["bool"].(map[string]any)
	want := teamValueClause([]string{"kubernetes.labels.team-a", "kubernetes.labels.team-b"}, []string{"orders-team", "Orders"})
	found := false
	for _, c := range b["filter"].([]any) {
		if reflect.DeepEqual(c, want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("takım yan tümcesi filtre bağlamında yok: %v", b["filter"])
	}
	if m, ok := b["must"]; ok {
		raw, _ := json.Marshal(m)
		if strings.Contains(string(raw), "team-a") {
			t.Errorf("takım must'ta olmamalı: %s", raw)
		}
	}
	// TeamRecentLogs gövdesiyle aynı yan tümce (iki yüzey tek nüfus).
	body := buildTeamLogsBody(s.fields, []string{"kubernetes.labels.team-a", "kubernetes.labels.team-b"}, []string{"orders-team", "Orders"}, "", time.Time{}, time.Time{})
	raw1, _ := json.Marshal(body["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)[1])
	raw2, _ := json.Marshal(want)
	if string(raw1) != string(raw2) {
		t.Errorf("parite: TeamRecentLogs %s != /logs %s", raw1, raw2)
	}
	// Takımsız sorgu değişmez (bugünkü davranış pinli).
	plain, _ := json.Marshal(s.buildQuery(Filter{Service: "orders-api"}))
	if strings.Contains(string(plain), "team-a") {
		t.Errorf("takımsız sorgu takım yan tümcesi taşımamalı: %s", plain)
	}
}

// Alan yapılandırılmamışsa sorgu takımsızla aynı + UnappliedFilters "team".
func TestTeamFilterUnappliedHonesty(t *testing.T) {
	s := newTestESStore(ESFieldMap{})
	a, _ := json.Marshal(s.buildQuery(Filter{Service: "orders-api", Team: "orders-team"}))
	b, _ := json.Marshal(s.buildQuery(Filter{Service: "orders-api"}))
	if string(a) != string(b) {
		t.Errorf("alan yokken takım sorguyu değiştirmemeli:\n%s\n%s", a, b)
	}
	if got := esSearchUnapplied(Filter{Team: "orders-team"}, ESFieldMap{}); !reflect.DeepEqual(got, []string{FilterTeam}) {
		t.Errorf("ES alan yok → [team], got %v", got)
	}
	if got := esSearchUnapplied(Filter{Team: "orders-team"}, ESFieldMap{Team: "labels.team"}); got != nil {
		t.Errorf("ES alan var → nil, got %v", got)
	}
	if got := esSearchUnapplied(Filter{Team: "orders-team", SeverityMin: 17}, ESFieldMap{}); !reflect.DeepEqual(got, []string{FilterSeverity, FilterTeam}) {
		t.Errorf("ES seviye + takım → iki bayrak, got %v", got)
	}
	// CH: takım hiç uygulanmaz → itiraf.
	if got := chSearchUnapplied(Filter{Team: "orders-team"}); !reflect.DeepEqual(got, []string{FilterTeam}) {
		t.Errorf("CH takım → [team], got %v", got)
	}
	if got := chSearchUnapplied(Filter{Service: "orders-api"}); got != nil {
		t.Errorf("CH takımsız → nil, got %v", got)
	}
}
