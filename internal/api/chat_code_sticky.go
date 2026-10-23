package api

// chat_code_sticky.go — v0.10.1465 (operatör onayı 2026-10-23: "Önerini
// yapalım"): kod turu KONUŞMAYA yapışır.
//
// v0.10.1461'in bilinen sınırı: kod turunun cevabı (alıntılanan satırlar)
// konuşma geçmişinde kalır; aynı konuşmada "Kod" hapı kapatılınca sonraki tur o
// geçmişi çözülen profile ve dış MCP araçlı döngüye taşıyordu. Kural artık
// konuşma bazında:
//
//  1. İŞARET: açık kod turu uygulandığında ya da bir turda read_source_code /
//     search_code BAŞARIYLA döndüğünde (bağımsız sohbet, trace takibi,
//     exception takibi — hangi yol olursa) sunucu sohbet bağlamı (v0.10.478,
//     Redis, kullanıcı + konuşma) CodeSticky=true olur. İşaretli konuşmanın her
//     turu işareti yeniden yazar (24 s TTL tazelenir).
//  2. İSTEMCİ İPUCU: istemci de bilir (code-lock olayı → asistan turunda
//     codeRead, arşive yazılır) ve kilitli konuşmada context.codeLocked
//     gönderir. İpucu YALNIZ sıkılaştırır (Redis TTL'i dolmuş, Geçmiş'ten
//     yeniden açılmış konuşma); asla gevşetmez.
//  3. YAPIŞKAN TUR: hap ne olursa olsun alışveriş HER kademeden önce
//     RouteSensitive(SensitiveCode) ile yerel profile sabitlenir (guided /
//     çekmece / RAG / niyet / açıklama / döngü — hepsinin model çağrısı copilot
//     kapısından geçer); dış MCP kataloğu, köprüsü ve extNames kurulmaz.
//     Wiki araçları kalır (wiki de localOnly; tur yerel modelde).
//  4. KAPALI DÜŞÜŞ: yerel profil artık yoksa tur dürüst kapalı cevap verir ve
//     BİTER — sağlayıcıya tek istek gitmez, geçmiş yerel olmayan profile hiç
//     gönderilmez (fail-closed).
//  5. SIFIRLAMA: yalnız yeni konuşma ("Temizle" / yeni kimlik). clear_context
//     aracı işareti silmez (çalışma kümesi değil).
//
// Evalset vakası konuşmasızdır (boş durum, ipucu yok) → yapışkan değil.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// codeStickyClosedText — yapışkan konuşmada yerel profil yokken turun cevabı.
const codeStickyClosedText = "Bu konuşmada kaynak kod okundu; konuşma yalnız yerel (self-hosted) model profilinde sürer ve şu an yerel profil yok — geçmiş başka bir modele gönderilmedi. Yeni konuşma için Temizle; ya da bir admin Settings › CoSRE › Model profilleri › Düzenle › «Yerel (self-hosted) model» kutusunu işaretlemeli."

// codeStickyStepPrefix — yapışkan turun step etiketinin başı.
const codeStickyStepPrefix = "Kod okunan konuşma"

// codeLockEvent — istemciye "bu konuşma kilitli" olayı (FE: asistan turunda codeRead).
const codeLockEvent = "code-lock"

// codeStickyActive — SAF: konuşma yapışkan mı (sunucu işareti ya da istemci ipucu).
func codeStickyActive(st *chatContextState, hint bool) bool {
	return hint || (st != nil && st.ctx.CodeSticky)
}

// codeStickyStepLabel — SAF: yapışkan turun step etiketi.
func codeStickyStepLabel(label string, closed bool) string {
	if closed {
		return codeStickyStepPrefix + " " + localOnlyClosedReason
	}
	return codeStickyStepPrefix + ": " + label + " · konuşma yerel modelde, dış araçlar kapalı"
}

// noteCodeRead — konuşmayı yapışkan işaretler ve code-lock olayını tur başına
// bir kez yayınlar. Durum yoksa (konuşma dışı çağrı) yalnız olay.
func noteCodeRead(st *chatContextState, emit func(string, any)) {
	if st != nil {
		st.codeRead.Store(true)
		if !st.codeLockSent.CompareAndSwap(false, true) {
			return
		}
	}
	if emit != nil {
		emit(codeLockEvent, map[string]bool{"locked": true})
	}
}

// applyCodeSticky — alışverişin başında (her kademeden ÖNCE): konuşma
// yapışkansa ctx yerel profile sabitlenir + SensitiveCode işaretlenir.
// closed=true → yerel profil yok: kapalı cevap yayınlandı, çağıran "done"
// yayınlayıp DÖNER (sağlayıcı çağrısı yok).
func (s *Server) applyCodeSticky(ctx context.Context, emit func(string, any), st *chatContextState, hint bool) (out context.Context, sticky, closed bool) {
	if !codeStickyActive(st, hint) {
		return ctx, false, false
	}
	noteCodeRead(st, emit)
	rctx, ok := s.routeSensitive(ctx, copilot.SensitiveCode)
	if !ok {
		emit("step", map[string]string{"label": codeStickyStepLabel("", true)})
		emit("answer", map[string]any{"text": codeStickyClosedText, "exchangeId": copilot.MetaFromContext(ctx).ExchangeID,
			"sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}})
		return ctx, true, true
	}
	emit("step", map[string]string{"label": codeStickyStepLabel(s.chatProfileLabel(rctx), false)})
	return rctx, true, false
}

// chatProfileLabel — ctx'in çözülen profilinin etiketi (etiket > kimlik > model).
func (s *Server) chatProfileLabel(ctx context.Context) string {
	if s == nil || s.copilot == nil {
		return ""
	}
	id := s.copilot.ProfileIDFor(ctx)
	label := id
	if l, _, model, _, ok := s.copilot.ProfileView(id); ok {
		switch {
		case strings.TrimSpace(l) != "":
			label = l
		case label == "" && model != "":
			label = model
		}
	}
	return label
}

// isCodeReadTool — SAF: sonucu kaynak kod taşıyan araç.
func isCodeReadTool(name string) bool {
	return name == mcptools.SourceCodeToolName || name == mcptools.SearchCodeToolName
}

// wrapCodeReadHandlers — kod araçlarının handler'ları: BAŞARILI dönüş
// konuşmayı yapışkan işaretler (onRead). Diğer araçlar aynen.
func wrapCodeReadHandlers(byName map[string]mcp.ToolHandler, onRead func()) {
	for n, h := range byName {
		if !isCodeReadTool(n) || h == nil {
			continue
		}
		h := h
		byName[n] = func(ctx context.Context, args json.RawMessage) (any, error) {
			res, err := h(ctx, args)
			if err == nil {
				onRead()
			}
			return res, err
		}
	}
}
