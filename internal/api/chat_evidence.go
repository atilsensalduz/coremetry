package api

// chat_evidence.go — v0.10.1272 (CoSRE adım 3/8): kanıt çipleri.
//
// query_metrics / query_traces / query_logs sonuçlarındaki `evidence`
// (kaynak · sorgu · pencere · env/cluster · satır · süre) sohbet olaylarına
// YAPISAL olarak taşınır:
//
//   - `step-result.evidence` — çipin kendi kanıtı (+ "Explore'da aç" href'i);
//     adımda başka köprü yoksa `href` de bu olur (eski istemci "Üründe aç"
//     bağlantısını görür).
//   - `block` (v0.10.1272 operatör eki: "chat içerisine de getirebilirsin") —
//     aracın zaten sınırlı verisinden chart / trace_list / table bloğu
//     (mcptools.ToolResultBlock); yük `evidenceRef` = kanıtın `ref`'i, çip
//     bloğu açar/kapatır. Ek sorgu yok.
//   - `answer.evidence[]` — alışveriş boyunca toplanan kanıt listesi; cevap
//     balonu çipleri bundan çizer, arşive de bu liste yazılır (geri yüklenen
//     cevap çiplerini korur).
//
// Geriye uyum: iki alan da İSTEĞE BAĞLI ve yalnız kanıt varsa yazılır — kanıt
// zarfı taşımayan araçların step-result / answer gövdesi bayt-bayt aynı.
// href mcptools.EvidenceHref'ten (tek saf, tablo testli eşleme); burada URL
// kurulmaz.

import (
	"fmt"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// chatEvidenceMax — cevap başına kanıt çipi tavanı (çağrı bütçesi 6–12; Derin
// kipte bile şerit taşmasın).
const chatEvidenceMax = 8

// attachStepEvidence — araç sonucu kanıt zarfı taşıyorsa step-result gövdesine
// `evidence` (ve boşsa `href`) yazar. Saf (yalnız stepEv'i değiştirir).
func attachStepEvidence(stepEv map[string]any, tool, content string) (mcptools.ChatEvidence, bool) {
	ev, ok := mcptools.ToolResultEvidence(tool, content)
	if !ok {
		return mcptools.ChatEvidence{}, false
	}
	// Çip ↔ sohbet içi blok bağı: adım kimliğinden (istek boyunca tekil).
	if i, has := eventInt(stepEv["i"]); has && i > 0 {
		ev.Ref = fmt.Sprintf("ev%d", i)
	}
	stepEv["evidence"] = ev
	if _, set := stepEv["href"]; !set && ev.Href != "" {
		stepEv["href"] = ev.Href
	}
	return ev, true
}

// withEvidenceScope — v0.10.1274: kanıt çipine ekran-kapsamı künyesi (boşsa
// dokunmaz); step-result.evidence de güncellenir. Saf.
func withEvidenceScope(stepEv map[string]any, ev mcptools.ChatEvidence, sa mcptools.ScopeApplied) mcptools.ChatEvidence {
	if sa.Empty() {
		return ev
	}
	c := sa
	ev.CtxApplied = &c
	stepEv["evidence"] = ev
	return ev
}

// mergeChatEvidence — çağrı sırasıyla biriken kanıt; tavan chatEvidenceMax. Saf.
func mergeChatEvidence(acc []mcptools.ChatEvidence, ev mcptools.ChatEvidence) []mcptools.ChatEvidence {
	if len(acc) >= chatEvidenceMax {
		return acc
	}
	return append(acc, ev)
}

// withAnswerEvidence — `answer` gövdesine kanıt listesi; liste boşsa alan
// YAZILMAZ (eski cevap şekli aynen). Saf.
func withAnswerEvidence(ans map[string]any, evs []mcptools.ChatEvidence) map[string]any {
	if len(evs) > 0 {
		ans["evidence"] = evs
	}
	return ans
}
