package rollout

// nsfilter.go — v0.10.1262 — operatör 2026-10-17: "OpenShift'e ait projeler
// rollout listelemesin; console, metrics-server, oauth-openshift gibi
// cluster'ın kendi deployment'larını görmem gerekmiyor."
//
// Platform namespace dışlaması: system_settings["rollouts"].excludeNamespaces
// glob listesi (yalnız `*` = herhangi dizi, `?` = tek karakter). nil (alan
// yok) = DefaultExcludeNamespaces; boş liste [] = bilinçli "hiçbirini gizleme".
// Liste / Toplu uçları SUNUCU tarafında uygular (api/rollouts.go): CH LIMIT'i
// (200) platform gürültüsüne harcanmasın. Tek kaynak: SQL'e giden RE2 deseni
// ile Go eşleyicisi aynı NamespaceExcludeRegex'ten derlenir.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// DefaultExcludeNamespaces — OpenShift platform projeleri + Kubernetes
// sistem namespace'leri (kube-system, kube-public, kube-node-lease).
// `default` BİLEREK yok: uygulama ekipleri oraya da deploy ediyor.
var DefaultExcludeNamespaces = []string{"openshift", "openshift-*", "kube-*"}

// excludeNamespacesMax — desen tavanı (regex boyu + UI okunurluğu).
const excludeNamespacesMax = 64

// nsGlobRE — k8s namespace alfabesi (RFC 1123 etiketi, küçük harf) + glob
// jokerleri. Nokta da kabul (bazı dağıtımlar; zararsız — QuoteMeta kaçırır).
var nsGlobRE = regexp.MustCompile(`^[a-z0-9*?.-]{1,63}$`)

// NormalizeNamespacePatterns — SAF: kırp, küçült, boşları at, tekilleştir,
// sırala (önbellek anahtarı ve SQL deseni girdi sırasından bağımsız).
func NormalizeNamespacePatterns(in []string) []string {
	set := map[string]bool{}
	for _, p := range in {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ValidateNamespacePatterns — PUT doğrulaması: anlaşılmaz desen 400.
func ValidateNamespacePatterns(in []string) error {
	if len(in) > excludeNamespacesMax {
		return fmt.Errorf("excludeNamespaces en çok %d desen olabilir (%d verildi)", excludeNamespacesMax, len(in))
	}
	for i, p := range in {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if !nsGlobRE.MatchString(p) {
			return fmt.Errorf("excludeNamespaces[%d] %q geçersiz: küçük harf, rakam, '-', '.' ve glob jokerleri '*' / '?' (≤63 karakter)", i, p)
		}
	}
	return nil
}

// globToRE — SAF: tek glob → çapasız RE2 parçası.
func globToRE(p string) string {
	var b strings.Builder
	lit := strings.Builder{}
	flush := func() {
		if lit.Len() > 0 {
			b.WriteString(regexp.QuoteMeta(lit.String()))
			lit.Reset()
		}
	}
	for _, r := range p {
		switch r {
		case '*':
			flush()
			b.WriteString(".*")
		case '?':
			flush()
			b.WriteString(".")
		default:
			lit.WriteRune(r)
		}
	}
	flush()
	return b.String()
}

// NamespaceExcludeRegex — SAF: desen listesi → tam-eşleşmeli tek RE2
// (`^(?:a|b.*)$`); boş liste "" (dışlama yok). CH `match()` ve Go aynı
// sözdizimini (RE2) konuşur.
func NamespaceExcludeRegex(patterns []string) string {
	ps := NormalizeNamespacePatterns(patterns)
	if len(ps) == 0 {
		return ""
	}
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = globToRE(p)
	}
	return "^(?:" + strings.Join(parts, "|") + ")$"
}

// NamespaceExcluded — SAF: ns desenlerden birine uyuyor mu? (Go tarafı
// eşleyici; SQL ile aynı RE'den.)
func NamespaceExcluded(patterns []string, ns string) bool {
	re := NamespaceExcludeRegex(patterns)
	if re == "" {
		return false
	}
	ok, _ := regexp.MatchString(re, ns)
	return ok
}

// ResolvedExcludeNamespaces — uygulanan desenler: alan yok (nil) →
// varsayılan; [] → dışlama yok. Normalize edilmiş, sıralı.
func (s Settings) ResolvedExcludeNamespaces() []string {
	if s.ExcludeNamespaces == nil {
		return NormalizeNamespacePatterns(DefaultExcludeNamespaces)
	}
	return NormalizeNamespacePatterns(*s.ExcludeNamespaces)
}

// EffectiveNamespaceExclude — SAF: açık namespace süzgeci yazıldıysa
// dışlama UYGULANMAZ (açık süzgeç kazanır — operatör "openshift-monitoring"
// yazdıysa onu görmek istiyor); aksi hâlde uygulanan desenler.
func EffectiveNamespaceExclude(patterns []string, explicitNamespace string) []string {
	if strings.TrimSpace(explicitNamespace) != "" {
		return nil
	}
	return patterns
}
