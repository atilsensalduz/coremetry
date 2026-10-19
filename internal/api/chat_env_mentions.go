package api

// chat_env_mentions.go — v0.10.1380 (operatör bağlamı: küme adları "prod-"
// öneki olmadan ya da farklı harf büyüklüğüyle yazılınca CoSRE tanımıyordu).
// Prod'da ortam/küme değeri sinyaller arasında tutarsız: kullanıcı "eu-a"
// yazıyor, span deploy_env değeri "prod-eu-a" (ya da "prod-EU-A") — operatör
// Collector transform'uyla normalize edene dek anma kaçıyor, küme kıyastan
// düşüyor ya da yanlış tek ortam seçiliyordu.
//
// Bu dosya mesajdaki ortam anmalarını CANLI span ortam kataloğuna
// (guidedEnvNames → ListEnvironments) karşı SAF olarak bulur; asla katalogda
// olmayan bir değer üretmez:
//
//   - her canlı değer için iki biçim: TAM değer ve öneksiz KISA biçim
//     (vmetrics.EnvShortForm — v0.10.1376 metrik eşleştiricisiyle TEK tanım:
//     baştaki tek `^[A-Za-z0-9]+-` parçası atılır, kalan ≥3); kısa biçim
//     çıplak ortam kademesi sözcüğüyse (prod, uat, test… — envTierWords)
//     sohbette anma sayılmaz;
//   - eşleşme harf duyarsız ve SINIRLI (indexBounded): "eu-a", "eu-a-svc"
//     içinde tutmaz ('-' ad karakteri);
//   - aynı biçim hem bir değerin TAM adı hem başka değerin kısa biçimiyse TAM ad
//     kazanır (canlı "eu-a" varken "eu-a" yalnız "eu-a"dır);
//   - kısa biçim isabeti o kısa biçimi paylaşan TÜM canlı değerlere çözülür
//     ("eu-a" → prod-eu-a VE dr-eu-a; hiçbiri sessizce seçilmez);
//   - harf varyantları (prod-eu-a / PROD-EU-A) TEK ad sayılır, katalog
//     sırasındaki ilk yazım (en yoğun) temsil eder — v0.10.1373 tam-ad
//     yolunun tekilleştirmesiyle aynı.
//
// Tüketiciler: extractEnvEntity / extractEnvSet (guided rota süzgeci,
// endpoint küme kıyası opTarget.Envs), hasAnalysisCue, extractServiceEntity'nin
// ortam-jetonu dışlaması, niyet kademesi env doğrulaması ve "eu-a → prod-eu-a"
// bağlam çipi (envAliasNoteTR).

import (
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// envMention — v0.10.1380: mesajda sınırlı geçen tek ortam anması.
type envMention struct {
	At     int      // normalize mesajdaki ilk sınırlı geçişin bayt konumu
	Said   string   // mesajda tutan biçim (küçük harf)
	Values []string // canlı değerler: katalog sırası, harf-katlamalı tekil
	Short  bool     // öneksiz kısa biçimle tuttu (tam ad değil)
}

// envForm — bir biçimin (küçük harf) tam ve kısa biçim sahipleri.
type envForm struct {
	key         string
	full, short []string
}

// envTierWords — v0.10.1380: çıplak ortam KADEMESİ sözcükleri. Kısa biçim
// bunlardan biriyse sohbette ortam anması sayılmaz: servis adları sık sık
// "-prod" ile biter, mesajlar "prod" der durur; canlı "k8s-prod" her "prod"u
// ortam anmasına çevirmemeli. Yalnız SOHBET yolu — vmetrics.EnvShortForm /
// MatchEnvValues (VM çözücüsü) değişmez; tam ad eşleşmesi etkilenmez.
// Anahtarlar küçük harf (biçimler küçük harfle kurulur → harf duyarsız).
var envTierWords = map[string]bool{
	"prod": true, "production": true, "prd": true, "test": true, "tst": true,
	"dev": true, "development": true, "uat": true, "qa": true, "stage": true,
	"staging": true, "stg": true, "preprod": true, "prep": true, "sit": true,
	"perf": true, "dr": true, "local": true,
}

// envMentionForms — SAF (v0.10.1380): canlı değerlerden biçim tablosu,
// katalog sırasıyla. Tam ad <2 karakter atlanır (extractEnvEntity tabanı).
func envMentionForms(envs []string) []*envForm {
	idx := map[string]*envForm{}
	var out []*envForm
	get := func(k string) *envForm {
		f, ok := idx[k]
		if !ok {
			f = &envForm{key: k}
			idx[k] = f
			out = append(out, f)
		}
		return f
	}
	for _, env := range envs {
		le := strings.ToLower(env)
		if len(le) < 2 {
			continue
		}
		f := get(le)
		f.full = append(f.full, env)
		if rest, ok := vmetrics.EnvShortForm(le); ok && !envTierWords[rest] {
			s := get(rest)
			s.short = append(s.short, env)
		}
	}
	return out
}

// foldUnique — SAF: harf-katlamalı tekil, ilk yazım kalır, sıra korunur.
func foldUnique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		lv := strings.ToLower(v)
		if seen[lv] {
			continue
		}
		seen[lv] = true
		out = append(out, v)
	}
	return out
}

// findEnvMentions — SAF (v0.10.1380): normalize mesajdaki (normalizeGuidedMsg)
// ortam anmaları, ilk geçiş sırasıyla. Tam ad isabeti kısa biçim isabetine
// tercih edilir (aynı biçim için); kısa biçim isabeti o biçimi paylaşan tüm
// canlı değerleri taşır.
func findEnvMentions(msg string, envs []string) []envMention {
	if msg == "" || len(envs) == 0 {
		return nil
	}
	var out []envMention
	for _, f := range envMentionForms(envs) {
		vals, short := f.full, false
		if len(vals) == 0 {
			vals, short = f.short, true
		}
		if i := indexBounded(msg, f.key); i >= 0 {
			out = append(out, envMention{At: i, Said: f.key, Values: foldUnique(vals), Short: short})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// envShortResolve — SAF (v0.10.1380): yalnız kısa biçim isabetlerinin tek
// canlı değere çözümü (extractEnvEntity'nin tam-ad yolu boşken). Kısa
// isabetler birden çok değere çözülüyorsa "" — tahmin yok; çok ortamlı küme
// extractEnvSet'in işi.
func envShortResolve(ms []envMention) string {
	one := ""
	for _, m := range ms {
		if !m.Short {
			continue
		}
		for _, v := range m.Values {
			switch {
			case one == "":
				one = v
			case !strings.EqualFold(one, v):
				return ""
			}
		}
	}
	return one
}

// envMentionValues — SAF (v0.10.1380): anmaların canlı değerleri düz liste,
// anma sırası, harf-katlamalı tekil.
func envMentionValues(ms []envMention) []string {
	var all []string
	for _, m := range ms {
		all = append(all, m.Values...)
	}
	if len(all) == 0 {
		return nil
	}
	return foldUnique(all)
}

// envNameTokens — SAF (v0.10.1380): ortamı adlandıran jetonlar (küçük harf):
// her canlı değerin tam adı ve kısa biçimi. Jeton-eşitliğiyle bakan
// tüketiciler (hasAnalysisCue, extractServiceEntity) için.
func envNameTokens(envs []string) map[string]bool {
	out := map[string]bool{}
	for _, f := range envMentionForms(envs) {
		out[f.key] = true
	}
	return out
}

// envAliasNoteTR — SAF (v0.10.1380): kısa biçimle anılıp canlı değere çözülen
// ortamların bağlam çipi: "ortam adı çözüldü: eu-a → prod-eu-a, dr-eu-a".
// Yalnız rotanın gerçekten kullandığı değerler (applied: Env ya da EnvSet)
// yazılır; tam adla anılan ortam için çip yok (bayt bayt eski akış).
func envAliasNoteTR(ms []envMention, applied []string) string {
	use := map[string]bool{}
	for _, v := range applied {
		if v != "" {
			use[strings.ToLower(v)] = true
		}
	}
	var parts []string
	for _, m := range ms {
		if !m.Short {
			continue
		}
		var vals []string
		for _, v := range m.Values {
			if use[strings.ToLower(v)] {
				vals = append(vals, v)
			}
		}
		if len(vals) > 0 {
			parts = append(parts, m.Said+" → "+strings.Join(vals, ", "))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "ortam adı çözüldü: " + strings.Join(parts, "; ")
}

// resolveEnvShortName — SAF (v0.10.1380): niyet kademesinin döndürdüğü TEK
// ortam adı (matchLiveName tutmadığında) kısa biçimse ve tek canlı değere
// çözülüyorsa o değer; değilse "" (uydurma/belirsiz ad yine reddedilir).
func resolveEnvShortName(v string, envs []string) string {
	norm := normalizeGuidedMsg(strings.TrimSpace(v))
	ms := findEnvMentions(norm, envs)
	if len(ms) != 1 || ms[0].At != 0 || len(ms[0].Said) != len(norm) {
		return ""
	}
	return envShortResolve(ms)
}

// endpointEnvMsg — SAF (v0.10.1380, endpointEnvSet'ten ayrıldı): yol metinden
// çıkarılmış, sarmalayıcısı atılmış normalize mesaj.
func endpointEnvMsg(raw, path string) string {
	rest := raw
	if path != "" {
		rest = strings.Replace(raw, path, " ", 1)
	}
	return normalizeGuidedMsg(stripMarkdownWrapText(rest))
}

// endpointEnvAliasNote — SAF (v0.10.1380): endpoint küme kıyası hedefinin
// (opTarget.Envs) kısa biçimle anılan ortamları için bağlam çipi metni.
func endpointEnvAliasNote(raw, path string, envs, set []string) string {
	if len(set) == 0 {
		return ""
	}
	return envAliasNoteTR(findEnvMentions(endpointEnvMsg(raw, path), envs), set)
}

// emitEnvAliasStep — v0.10.1380: boş olmayan çözüm notunu bağlam çipi yayar.
func emitEnvAliasStep(emit func(string, any), note string) {
	if emit == nil || note == "" {
		return
	}
	emitGuidedContextStep(emit, note)
}
