package api

// chat_answer_links.go — v0.10.1297 (operatör 2026-10-17, prod CoSRE, gemma4
// 31b): BSA operasyon adı sorusunun cevabında model metne
//
//	// build_link result
//	https://coremetry.io/traces?order=desc&range=custom:…&search=<ad>&sort=duration
//
// yazdı. İki ayrı kusur:
//
//  1. HOST UYDURMA. build_link kök-göreli href döner; dış MCP açıklaması
//     "kurulumun base URL'ini önüne ekle" diyordu, sohbet kısa açıklaması
//     "cevabın sonuna KOY" diyordu. Base URL'i bilmeyen küçük model ürünün
//     adından bir alan adı türetti (coremetry.io — kodda hiçbir yerde yok).
//  2. ARAÇ ÇIKTISI SIZINTISI. "// build_link result" modelin araç sonucunu
//     metne kopyalamasının izi (küçük model davranışı).
//
// Açıklamalar düzeltildi (build_link.go, prompts.go), ama prompt bir rica;
// bu dosya DETERMİNİSTİK kapı: serbest döngünün son metni yayından önce
// buradan geçer.
//   - "// <araç> result|sonucu" yankı satırı silinir (yalnız katalogdaki
//     araç adlarıyla — sıradan "// not" satırına dokunulmaz).
//   - Coremetry'ye ait görünen mutlak link (host'unda "coremetry" geçen ya da
//     PUBLIC_URL'in kendisi, ya da yolu sunucunun bu turda doğruladığı bir
//     linkin yoluyla aynı olan yabancı host) sunucunun DOĞRULANMIŞ linkiyle
//     (build_link / deep_link çipinin href'i, kök-göreli) değiştirilir;
//     doğrulanmış eşi yoksa ve host yabancıysa link SÖKÜLÜR (markdown'da
//     etiket kalır). Wiki / dış doküman linkleri (Coremetry yolu taşımayan
//     yabancı host) aynen kalır.
//   - Boş kalan kod çiti silinir.

import (
	"net/url"
	"regexp"
	"strings"
)

// answerToolEchoRe — "// build_link result", "# search_traces sonucu:" gibi
// araç-çıktısı başlık satırları. Ad katalogda değilse satır kalır.
var answerToolEchoRe = regexp.MustCompile(`(?m)^[ \t]*(?://+|#+|--)[ \t]*([A-Za-z][A-Za-z0-9_]*)[ \t]+(?i:result|results|output|response|sonucu|çıktısı|ciktisi)[ \t]*:?[ \t]*(?:\r?\n|$)`)

// answerMDLinkRe — markdown link, mutlak hedef.
var answerMDLinkRe = regexp.MustCompile(`\[([^\]\n]*)\]\((https?://[^\s)]+)\)`)

// answerBareURLRe — çıplak mutlak URL.
var answerBareURLRe = regexp.MustCompile("https?://[^\\s<>()\\[\\]\"'`]+")

// answerEmptyFenceRe — içi boş (yalnız boşluk) kod çiti.
var answerEmptyFenceRe = regexp.MustCompile("(?m)^[ \\t]*```[A-Za-z0-9_-]*[ \\t]*\\r?\\n(?:[ \\t]*\\r?\\n)*[ \\t]*```[ \\t]*(?:\\r?\\n|$)")

// sanitizeAnswerLinks — SAF (tablo testli). validated: sunucunun bu turda
// kurduğu linkler (loopLinks — build_link / deep_link / araç köprüsü);
// publicURL: COREMETRY_PUBLIC_URL ("" = yok); tools: katalogdaki araç adları.
func sanitizeAnswerLinks(text string, validated []guidedAnswerLink, publicURL string, tools map[string]bool) string {
	if text == "" {
		return text
	}
	text = answerToolEchoRe.ReplaceAllStringFunc(text, func(line string) string {
		m := answerToolEchoRe.FindStringSubmatch(line)
		if len(m) > 1 && tools[m[1]] {
			return ""
		}
		return line
	})
	if !strings.Contains(text, "://") {
		return answerEmptyFenceRe.ReplaceAllString(text, "")
	}
	ownOrigin := normalizeOrigin(publicURL)
	text = answerMDLinkRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := answerMDLinkRe.FindStringSubmatch(m)
		label, raw := sub[1], sub[2]
		repl, act := answerLinkAction(raw, validated, ownOrigin)
		switch act {
		case answerLinkReplace:
			return "[" + label + "](" + repl + ")"
		case answerLinkDrop:
			return label
		}
		return m
	})
	text = answerBareURLRe.ReplaceAllStringFunc(text, func(m string) string {
		raw := strings.TrimRight(m, ".,;:!?")
		tail := m[len(raw):]
		repl, act := answerLinkAction(raw, validated, ownOrigin)
		switch act {
		case answerLinkReplace:
			return repl + tail
		case answerLinkDrop:
			return tail // cümle noktalaması kalır
		}
		return m
	})
	return answerEmptyFenceRe.ReplaceAllString(text, "")
}

type answerLinkAct int

const (
	answerLinkKeep answerLinkAct = iota
	answerLinkReplace
	answerLinkDrop
)

// answerLinkAction — SAF: tek mutlak link için karar.
func answerLinkAction(raw string, validated []guidedAnswerLink, ownOrigin string) (string, answerLinkAct) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", answerLinkKeep
	}
	own := ownOrigin != "" && normalizeOrigin(raw) == ownOrigin
	coremetryHost := strings.Contains(strings.ToLower(u.Hostname()), "coremetry")
	v := validatedAppLink(u.Path, validated)
	switch {
	case v != "" && (own || coremetryHost || isCoremetryAppPath(u.Path)):
		return v, answerLinkReplace
	case own:
		return "", answerLinkKeep // kendi kurulumumuz, doğrulanmış eşi yok: FE zaten app yoluna çevirir
	case coremetryHost:
		return "", answerLinkDrop // ürün adından türetilmiş yabancı host (coremetry.io)
	}
	return "", answerLinkKeep
}

// validatedAppLink — SAF: yolu eşleşen SON doğrulanmış kök-göreli link.
func validatedAppLink(path string, validated []guidedAnswerLink) string {
	if path == "" {
		return ""
	}
	out := ""
	for _, l := range validated {
		h := l.Href
		if !strings.HasPrefix(h, "/") || strings.HasPrefix(h, "//") || strings.HasPrefix(h, "/\\") {
			continue
		}
		p := h
		if i := strings.IndexAny(p, "?#"); i >= 0 {
			p = p[:i]
		}
		if strings.EqualFold(strings.TrimRight(p, "/"), strings.TrimRight(path, "/")) {
			out = h
		}
	}
	return out
}

// coremetryAppRoots — ürün sayfalarının ilk yol parçası (build_link sayfaları
// + araç köprülerinin hedefleri).
var coremetryAppRoots = map[string]bool{
	"traces": true, "trace": true, "logs": true, "service": true, "pod": true, "clusters": true, "endpoints": true,
	"endpoint": true, "problems": true, "incident": true, "incidents": true, "databases": true, "messaging": true,
	"rollouts": true, "inbox": true, "service-map": true, "explore": true,
}

// isCoremetryAppPath — SAF.
func isCoremetryAppPath(p string) bool {
	p = strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	return coremetryAppRoots[strings.ToLower(p)]
}

// chatToolNameSet — katalog adları (yankı satırı kapısı).
func chatToolNameSet[V any](schemas map[string]V) map[string]bool {
	out := make(map[string]bool, len(schemas)+1)
	for k := range schemas {
		out[k] = true
	}
	out[opPrefetchTool] = true // sentetik ön-getirme kaydı da yankılanabilir
	return out
}
