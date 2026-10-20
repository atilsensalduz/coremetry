package api

// chat_operation_exception_explain.go — v0.10.1399 (operator-reported):
// "Mesela bir operasyonu soruyorum ama bana kafka timeout vs diyor ama
// exceptions açıp explain ettirdiğimde daha mantıklı güzel cevap veriyor.
// Chatten de sorduğumda böyle bakıp buna cevap verebilir."
//
// Kök sebep: "<OP> operasyonu neden hata veriyor?" sorusunda operasyon
// ön-getirmesi (chat_operation_prefetch.go) Exceptions grubunu modele TEK
// kompakt satır olarak veriyordu (renderExceptionGroupsTR), örnek trace'leri
// ise hatalı-önce + süreye göre sıralayıp en yavaş hatalının kırılımını
// "hatayı aynen aktar" kuralıyla ekliyordu (endpointDrillPhase →
// pickDrillTraces → composeDrillEvidenceTR). Tesadüfi bir yan hata (log
// yayınının PRODUCER span'inde Kafka TimeoutException) cevabı domine etti.
// Exceptions sayfasındaki ✨ açıklama ise AYNI grubu kendi girdi kurucusuyla
// (exceptionExplainInput → anomaly.Build{,Oracle}ExceptionExplainInput:
// çağıran span + SQL, örnek trace logları, patlama/sürekli akış) ve kendi
// sistem prompt'uyla anlatıyordu — farklı girdi, farklı cevap.
//
// Şimdi: soru hata-neden şekilliyse (opErrorWhyAsk) ve ön-getirme Exceptions
// grubu bulduysa, cevap sohbet akışının İÇİNDE (çekmece yok — operatör:
// ayrı açıklama çekmecesi CoSRE'den kopuk hissettiriyor) Exceptions
// açıklamasının BİREBİR çekirdeğiyle üretilir: aynı girdi kurucusu
// (drawerExceptionInput → exceptionExplainInput), aynı sistem prompt'u
// (exceptionExplainSystem), aynı önbellek anahtarı (explainCacheKey(system,
// in.User, "")) — düğme ve sohbet aynı cevabı paylaşır (emsal
// trace_explain_unified.go). Yüzey "explain-exception:chat" (aisurface
// kaydı; evalset Exception ailesi). Grup seçimi deterministik
// (pickExplainGroup: P1 → son saat → toplam → son görülme; oylar ASLA).
// Grup yok / okunamadı / girdi kurulamadı / soru hata-neden değil → akış
// bayt bayt eski (ön-getirme özetiyle serbest döngü).

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// opExceptionExplainTool — sohbet içi açıklamanın adım çipi.
	opExceptionExplainTool = "exception_explain"
	// opExceptionExplainInputTimeout — girdi kurucusunun tavanı (kurucunun
	// kendi okumaları 5–6 sn tavanlı; bu dış sınır). LLM çağrısı alışverişin
	// tavanını (chatExchangeTimeout, ≥180 sn) kullanır.
	opExceptionExplainInputTimeout = 30 * time.Second
	// opExceptionExplainOthersMax — diğer grupların çip tavanı.
	opExceptionExplainOthersMax = 3
)

// opErrorWhyAsk — SAF: soru operasyonun NEDEN hata verdiğini mi soruyor?
// "neden/niye hata", "hata veriyor/alıyor/veriyo", "why … error/fail(ing)".
// Gecikme ("yavaş", "p99", "gecikme"), sayı/oran ("kaç", "oranı"), log
// ("hata logları") ve topoloji soruları hayır — onlar bugünkü yollarında kalır.
func opErrorWhyAsk(msg string) bool {
	norm := normalizeGuidedMsg(msg)
	toks := guidedTokens(norm)
	if len(toks) == 0 || hasTopologySignal(toks) || hasLogSignal(toks) {
		return false
	}
	if hasSlowTraceSignal(norm) || tokenHasPrefix(toks, "yavaş", "yavas", "slow", "gecikme", "latency", "p99", "p95", "p50", "takıl", "takil") {
		return false
	}
	if tokenHasPrefix(toks, "oran", "rate", "kaç", "kac", "sayı", "sayi", "count", "trend") {
		return false
	}
	if !tokenHasPrefix(toks, "hata", "error", "fail", "başarısız", "basarisiz", "exception", "istisna") {
		return false
	}
	if hasWhySignal(toks) {
		return true
	}
	// "hata veriyor / alıyor / atıyor" — fiil şekli (gündelik "veriyo" dahil).
	return tokenHasPrefix(toks, "veriyo", "alıyo", "aliyo", "atıyo", "atiyo", "fırlatıyo", "patlıyo", "patliyo")
}

// exGroupLabel — SAF: grubun kısa adı (Oracle → hata kodu, span → tip).
func exGroupLabel(g exGroupLine) string {
	if g.Code != "" {
		return g.Code
	}
	return g.Type
}

// pickExplainGroup — SAF, deterministik: açıklanacak grup + kalanlar.
// ignored/resolved düşer; sıra exceptionPriorityRank (P1 önce), son 1 sa
// sayısı (azalan), toplam oluşum (azalan), son görülme (azalan), parmak izi
// (artan — eşitlikte determinizm). Oylar (👍/👎) hiçbir zaman girmez.
func pickExplainGroup(groups []exGroupLine) (top exGroupLine, others []exGroupLine, ok bool) {
	cand := make([]exGroupLine, 0, len(groups))
	for _, g := range groups {
		switch strings.ToLower(strings.TrimSpace(g.State)) {
		case "ignored", "resolved":
			continue
		}
		if strings.TrimSpace(g.Fingerprint) == "" {
			continue
		}
		cand = append(cand, g)
	}
	if len(cand) == 0 {
		return exGroupLine{}, nil, false
	}
	sort.SliceStable(cand, func(i, j int) bool {
		a, b := cand[i], cand[j]
		if ra, rb := exceptionPriorityRank(a.Priority), exceptionPriorityRank(b.Priority); ra != rb {
			return ra < rb
		}
		if a.LastHour != b.LastHour {
			return a.LastHour > b.LastHour
		}
		if a.Occurrences != b.Occurrences {
			return a.Occurrences > b.Occurrences
		}
		if a.LastSeen != b.LastSeen {
			return a.LastSeen > b.LastSeen
		}
		return a.Fingerprint < b.Fingerprint
	})
	return cand[0], cand[1:], true
}

// opExceptionExplainFooterTR — SAF: cevabın sunucu yazımı künyesi.
func opExceptionExplainFooterTR(g exGroupLine, op string) string {
	parts := []string{"Exceptions grubu " + clipRunes(exGroupLabel(g), exGroupOpRunes)}
	if op = strings.TrimSpace(op); op != "" {
		parts = append(parts, clipRunes(op, exGroupOpRunes))
	}
	parts = append(parts, "Exceptions açıklamasıyla aynı kanıt")
	return "\n\nKaynak: " + strings.Join(parts, " · ")
}

// opExceptionExplainFollowups — SAF: diğer grupların açıklama çipleri (≤3,
// metin tekil) + ön-getirmenin operasyon topolojisi çipi.
func opExceptionExplainFollowups(others []exGroupLine, opFacts []followupFact) []chatFollowup {
	var out []chatFollowup
	seen := map[string]bool{}
	for _, g := range others {
		if len(out) >= opExceptionExplainOthersMax {
			break
		}
		c, ok := renderFollowup(followupFact{Kind: fuKindExceptionGroup, Code: exGroupLabel(g), Fingerprint: g.Fingerprint})
		if !ok || seen[c.Text] {
			continue
		}
		seen[c.Text] = true
		out = append(out, c)
	}
	for _, f := range opFacts {
		if f.Kind != fuKindOpTopology {
			continue
		}
		if c, ok := renderFollowup(f); ok && !seen[c.Text] {
			seen[c.Text] = true
			out = append(out, c)
		}
	}
	return out
}

// chatExceptionExplainInput — grup + Exceptions açıklamasının girdisi + sistem
// prompt'u. Girdi çekmece takibinin TEK kurucusundan (drawerExceptionInput →
// exceptionExplainInput; testte exceptionInputSeam), prompt dağıtıcının
// seçiminden (exceptionExplainSystem) — ✨ düğmesiyle aynı çift.
func (s *Server) chatExceptionExplainInput(ctx context.Context, fp string, loc *time.Location) (*chstore.ExceptionGroup, anomaly.ExceptionExplainInput, string, bool) {
	if exceptionInputSeam == nil && (s == nil || s.store == nil) {
		return nil, anomaly.ExceptionExplainInput{}, "", false
	}
	g, in, ok := s.drawerExceptionInput(ctx, fp, loc)
	if !ok || g == nil || strings.TrimSpace(in.User) == "" {
		return nil, anomaly.ExceptionExplainInput{}, "", false
	}
	return g, in, exceptionExplainSystem(g.Fingerprint), true
}

// opExceptionExplainInChat — operasyonun en önemli Exceptions grubunu sohbet
// akışının içinde Exceptions açıklamasıyla anlatır: adım çipi → delta* →
// answer. handled=false → çağıran bugünkü akışa (serbest döngü) devam eder;
// adım çipi yayınlandıysa hata çipte görünür.
func (s *Server) opExceptionExplainInChat(ctx context.Context, emit func(string, any), p *opPrefetch, loc *time.Location) (handled, ok bool) {
	if s == nil || s.copilot == nil || p == nil || !p.errorAsk || p.Exceptions == nil || p.Exceptions.Err != "" {
		return false, false
	}
	top, others, found := pickExplainGroup(p.Exceptions.Groups)
	if !found {
		return false, false
	}
	label := exGroupLabel(top)
	args, _ := json.Marshal(map[string]any{"operation": p.Operation, "group": label, "fingerprint": top.Fingerprint})
	n := emitGuidedStep(emit, opExceptionExplainTool, string(args))
	ictx, cancel := context.WithTimeout(ctx, opExceptionExplainInputTimeout)
	_, in, system, gok := s.chatExceptionExplainInput(ictx, top.Fingerprint, loc)
	cancel()
	if !gok {
		emitGuidedStepResult(emit, n, opExceptionExplainTool, "", errExceptionExplainInput)
		return false, false
	}
	emitGuidedStepResult(emit, n, opExceptionExplainTool, in.User, nil)

	// ✨ Explain root cause ile AYNI anahtar (copilot_exception.go, kod dalı kapalı).
	// v0.10.1400 — akış + önbellek gövdesi tek yerde (chat_explain_core.go
	// streamExplainCore; chatExplainTurn ve guidedTraceExplain de oradan).
	res, sok := s.streamExplainCore(ctx, emit, aisurface.ExplainExceptionChat, system, in.User, explainCacheKey(system, in.User, ""))
	if !sok {
		return true, false
	}
	xid := res.Xid
	text := trimExplainText(res) + opExceptionExplainFooterTR(top, p.Operation)
	// v0.10.1400 — grup linki ÇIPLAK grup sayfası: açıklama zaten bu turda;
	// `&ai=exception` artık sohbete yeni bir açıklama turu açar (çift tur olurdu).
	groupHref := top.Href
	if groupHref == "" {
		groupHref = top.ExplainHref
	}
	links := []guidedAnswerLink{{Label: "Exceptions grubu " + clipRunes(label, exGroupOpRunes), Href: groupHref}}
	if p.TracesHref != "" {
		links = append(links, guidedAnswerLink{Label: clipRunes(p.Operation, exGroupOpRunes) + " trace'leri", Href: p.TracesHref})
	}
	links = append(links, s.answerRequestIDLinks(ctx, text, top.Service)...)
	ans := withExplainCached(map[string]any{"text": text, "exchangeId": xid, "links": dedupLinksByHref(links)}, res)
	emit("answer", withAnswerFollowups(ans, opExceptionExplainFollowups(others, p.followupFacts())))
	return true, true
}

// errExceptionExplainInput — girdi kurulamadı (grup okunamadı / boş girdi).
var errExceptionExplainInput = errors.New("exception grubu okunamadı — açıklama kurulamadı")

// opDrillSkippedForErrorAsk — SAF: ön-getirmenin örnek trace kırılımı
// atlanır mı? Hata-neden sorusunda okunabilir Exceptions grubu varsa evet —
// cevap grubun açıklamasından gelir; kırılımın en yavaş hatalısı tesadüfi
// bir yan hatayı (log yayını PRODUCER timeout'u) öne çıkarıyordu.
func opDrillSkippedForErrorAsk(t opTarget, p *opPrefetch) bool {
	if !t.ErrorAsk || p == nil || p.Exceptions == nil || p.Exceptions.Err != "" {
		return false
	}
	_, _, ok := pickExplainGroup(p.Exceptions.Groups)
	return ok
}
