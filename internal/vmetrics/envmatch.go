package vmetrics

// envmatch.go — v0.10.1376 (CoSRE küme kıyası, VM yarısı): span'in deploy_env
// değeri ile VictoriaMetrics'teki ortam etiketinin (varsayılan
// deployment_environment_name) değeri her kurulumda AYNI yazılmıyor. Prod'da
// metrik etiketi küme önekini taşıyor ("prod-eu-a"), span tarafı taşıyabilir de
// taşımayabilir de ("eu-a"). Bu dosya istenen ortam adını CANLI etiket
// değerlerine karşı SAF olarak çözer.
//
// Kademe (istenen her ad için, ilk tutan kademe kazanır):
//
//  1. exact  — birebir aynı değer;
//  2. ci     — büyük/küçük harf duyarsız (EqualFold);
//  3. prefix — TEK TARAFLI tek önek: canlı değerin ya da istenen adın başındaki
//     TEK `^[A-Za-z0-9]+-` parçası atılınca öbürüyle EqualFold; kalan ≥3
//     karakter. İki taraf birden soyulmaz: "prod-eu-a" ile "dr-eu-a" EŞLEŞMEZ
//     (farklı kümeler); "eu-a" ile "eu-b" de (kalan "b" çok kısa).
//
// Kazanan kademenin TÜM değerleri döner — belirsizlik çağırana görünür, hiçbiri
// sessizce seçilmez ("eu-a" → {"dr-eu-a", "prod-eu-a"} ikisi birlikte).

import (
	"regexp"
	"sort"
	"strings"
)

// Eşleşme kademeleri — v0.10.1376.
const (
	EnvTierExact  = "exact"
	EnvTierCI     = "ci"
	EnvTierPrefix = "prefix"
	EnvTierNone   = "none"
)

// v0.10.1376 — girdi tavanları: istenen ≤6 ortam (sohbet envSetMax), canlı
// değer ≤1000 (VM etiket-değeri ucunun limiti).
const (
	envMatchWantMax  = 6
	envMatchLiveMax  = 1000
	envPrefixMinRest = 3
)

// EnvMatch — v0.10.1376: tek istenen adın çözümü. Values sıralı, tekil.
type EnvMatch struct {
	Want   string   `json:"want"`
	Values []string `json:"values,omitempty"`
	Tier   string   `json:"tier"`
}

// envPrefixRe — v0.10.1376: TEK önek parçası ("prod-", "dr-").
var envPrefixRe = regexp.MustCompile(`^[A-Za-z0-9]+-`)

// stripEnvPrefix — SAF (v0.10.1376): baştaki tek önek atılmış kalan; önek yoksa
// ya da kalan <3 karakterse ok=false.
func stripEnvPrefix(s string) (string, bool) {
	loc := envPrefixRe.FindStringIndex(s)
	if loc == nil {
		return "", false
	}
	rest := s[loc[1]:]
	if len([]rune(rest)) < envPrefixMinRest {
		return "", false
	}
	return rest, true
}

// envPrefixMatch — SAF (v0.10.1376): tek taraflı önek toleransı.
func envPrefixMatch(want, live string) bool {
	if r, ok := stripEnvPrefix(live); ok && strings.EqualFold(want, r) {
		return true
	}
	if r, ok := stripEnvPrefix(want); ok && strings.EqualFold(r, live) {
		return true
	}
	return false
}

// MatchEnvValue — SAF (v0.10.1376): tek adın kademeli çözümü.
func MatchEnvValue(want string, live []string) EnvMatch {
	m := EnvMatch{Want: want, Tier: EnvTierNone}
	w := strings.TrimSpace(want)
	if w == "" {
		return m
	}
	if len(live) > envMatchLiveMax {
		live = live[:envMatchLiveMax]
	}
	tiers := []struct {
		name string
		ok   func(v string) bool
	}{
		{EnvTierExact, func(v string) bool { return v == w }},
		{EnvTierCI, func(v string) bool { return strings.EqualFold(v, w) }},
		{EnvTierPrefix, func(v string) bool { return envPrefixMatch(w, v) }},
	}
	for _, t := range tiers {
		var vals []string
		for _, v := range live {
			if v != "" && t.ok(v) {
				vals = append(vals, v)
			}
		}
		if len(vals) > 0 {
			m.Tier, m.Values = t.name, sortedUnique(vals)
			return m
		}
	}
	return m
}

// MatchEnvValues — SAF (v0.10.1376): istenen her ad için MatchEnvValue, girdi
// sırasıyla; ≤6 ad.
func MatchEnvValues(want, live []string) []EnvMatch {
	if len(want) > envMatchWantMax {
		want = want[:envMatchWantMax]
	}
	out := make([]EnvMatch, 0, len(want))
	for _, w := range want {
		out = append(out, MatchEnvValue(w, live))
	}
	return out
}

// EnvMatcher — SAF (v0.10.1376): PromQL seçici parçası. Tek değer →
// `label="v"`; birden çok → `label=~"v1|v2"` (her değer regexp.QuoteMeta,
// sıralı, tekil; VM regex'i tam çapalı); değer yoksa "". Dize kaçışı frontend
// lib/httpMetrics.ts promStr ile aynı (yalnız \ ve ") — altın dosya pinler.
func EnvMatcher(label string, values []string) string {
	vals := sortedUnique(values)
	switch len(vals) {
	case 0:
		return ""
	case 1:
		return label + "=" + PromStrTS(vals[0])
	}
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = regexp.QuoteMeta(v)
	}
	return label + "=~" + PromStrTS(strings.Join(q, "|"))
}

// PromStrTS — SAF (v0.10.1376): frontend promStr'ın Go ikizi — `"` ile sarar,
// yalnız \ ve " kaçışlanır (quotePromString'den farklı olarak \n'e dokunmaz:
// iki dilin ürettiği sorgu bayt bayt aynı olmalı).
func PromStrTS(v string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
}

// sortedUnique — SAF: boşları atar, sıralar, tekilleştirir.
func sortedUnique(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}
