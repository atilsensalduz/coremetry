package api

// rollouts_sort_test.go — v0.10.1267 — operatör 2026-10-17: Rollouts
// sunucu-tarafı sıralama (Namespace/Workload/Cluster). Pinler:
//   - önbellek anahtarı sort+dir taşır (hash-all-inputs; aynı süzgeç farklı
//     ORDER BY + LIMIT ile farklı 200 satır döndürür);
//   - bozuk ?sort/?dir anahtardan ÖNCE normalize olur (varsayılanla aynı
//     girdi — ayrı önbellek girdisi basılamaz);
//   - handler normalize çifti v1 ve v2 okumasına aynen geçirir, öteki
//     süzgeçlere (platform namespace dışlaması dahil) dokunmaz.
// SQL eşlemesi + ORDER BY/LIMIT sırası: chstore/rollout_sort_test.go.

import (
	"net/http"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
)

func TestRolloutsListKeyVariesWithSort(t *testing.T) {
	from := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	key := func(sort, dir string) string {
		return rolloutsListKey("v2", chstore.RolloutFilter{Namespace: "pay", Sort: sort, Dir: dir}, 200, from, to)
	}
	def := key("", "")
	seen := map[string]string{def: "default"}
	for _, c := range [][2]string{{"started", "asc"}, {"namespace", "asc"}, {"namespace", "desc"}, {"workload", "asc"}, {"workload", "desc"}, {"cluster", "asc"}, {"cluster", "desc"}} {
		k := key(c[0], c[1])
		if prev, dup := seen[k]; dup {
			t.Fatalf("%s.%s anahtarı %s ile çakıştı: %s", c[0], c[1], prev, k)
		}
		seen[k] = c[0] + "." + c[1]
	}
	for _, c := range [][2]string{{"started", "desc"}, {"started", ""}, {"bogus", "asc"}, {"namespace;--", "desc"}} {
		if k := key(c[0], c[1]); k != def {
			t.Errorf("%q.%q varsayılan anahtara düşmeli: %s ≠ %s", c[0], c[1], k, def)
		}
	}
}

func TestRolloutListSortParamsReachReader(t *testing.T) {
	cases := []struct{ query, wantSort, wantDir string }{
		{"", "started", "desc"},
		{"&sort=namespace&dir=asc", "namespace", "asc"},
		{"&sort=workload&dir=desc", "workload", "desc"},
		{"&sort=cluster", "cluster", "asc"},
		{"&sort=started&dir=asc", "started", "asc"},
		{"&sort=evil%27%3B&dir=asc", "started", "desc"},
	}
	for _, src := range []string{rollout.SourceV1, rollout.SourceV2} {
		for _, c := range cases {
			fr := &fakeRolloutReader{}
			w := rv2Get(newRolloutReadEnv(t, src, fr), "/api/rollouts?"+rv2Window+c.query)
			if w.Code != http.StatusOK {
				t.Fatalf("%s %q: %d %s", src, c.query, w.Code, w.Body)
			}
			if fr.filter.Sort != c.wantSort || fr.filter.Dir != c.wantDir {
				t.Errorf("%s %q: okuyucuya %s.%s gitti, want %s.%s", src, c.query, fr.filter.Sort, fr.filter.Dir, c.wantSort, c.wantDir)
			}
			if len(fr.filter.ExcludeNamespaces) == 0 {
				t.Errorf("%s %q: platform namespace dışlaması sıralamayla düşmemeli", src, c.query)
			}
		}
	}
}
