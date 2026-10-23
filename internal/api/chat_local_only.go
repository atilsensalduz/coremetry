package api

// chat_local_only.go — v0.10.1451 (karar 8, operasyon kartı planı R1): api
// katmanının localOnly kararları. İlkeller copilot/sensitive.go'da
// (WithSensitiveContent, CheckLocalOnly, RouteSensitive, ErrLocalOnly); burada
// yalnız "bu çağıran için yerel profil var mı, yoksa ne söylenir, sohbet
// kataloğundan ne düşer" kararları durur.
//
// Kural (operatör kararı 1, 2026-10-23): tik admin beyanıdır ve deploy'la
// anında yürürlüğe girer. Tik yoksa kod/wiki okuma her yolda "kapalı: yerel
// profil yok" der — içerik HİÇ kurulmaz (sızıntı yönünde değil, dürüst düşüş);
// sohbet döngüsünde kod/wiki araçları sunulmaz ve katalog + prompt o
// özellikler kapalıymış gibi bayt bayt aynı kalır.

import (
	"context"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// localOnlyClosedReason — yerel profil yokken kod/wiki yollarının dürüst
// gerekçesi (kod bağlamı Reason'ı, sohbet step etiketi, wiki cevabı).
const localOnlyClosedReason = "kapalı: yerel profil yok — kod ve wiki içeriği yalnız yerel (self-hosted) model profiline gider (Ayarlar › AI › profil › Yerel model)"

// localOnlyWikiClosedText — açık wiki sorusunun yerel profil yokken cevabı.
const localOnlyWikiClosedText = "Wiki okuma kapalı: yerel profil yok. Wiki içeriği yalnız yerel (self-hosted) model profiline gider; bir admin Ayarlar › AI › profil › «Yerel model» işaretini vermeli."

// localOnlyTestSeam — YALNIZ testte dolu: copilot'suz sunucu (sahte wiki
// anlatıcısı, wikiNarrateFn) için yerel profil kararını sabitler. Üretimde nil:
// copilot yoksa yerel profil de yoktur.
var localOnlyTestSeam func() bool

func claimsRole(ctx context.Context) string {
	if c := auth.FromContext(ctx); c != nil {
		return c.Role
	}
	return ""
}

// localOnlyAvailable — bu çağrının hassas içeriği bir yerel profile
// gidebilir mi (çözülen profil yerel ya da role açık yerel profil var).
func (s *Server) localOnlyAvailable(ctx context.Context) bool {
	if (s == nil || s.copilot == nil) && localOnlyTestSeam != nil {
		return localOnlyTestSeam()
	}
	return s != nil && s.copilot != nil && s.copilot.LocalOnlyAvailable(ctx, claimsRole(ctx))
}

// routeSensitive — hassas içerikli çağrının ctx'i (yerel profile sabitlenmiş
// + işaretli). ok=false → yerel profil yok; çağıran içeriği kurmamalı.
func (s *Server) routeSensitive(ctx context.Context, kind copilot.SensitiveKind) (context.Context, bool) {
	if s == nil || s.copilot == nil {
		if localOnlyTestSeam != nil && localOnlyTestSeam() {
			return copilot.WithSensitiveContent(ctx, kind), true
		}
		return ctx, false
	}
	return s.copilot.RouteSensitive(ctx, claimsRole(ctx), kind)
}

// isSensitiveChatTool — SAF: aracın sonucu kaynak kod ya da wiki sayfa
// gövdesi taşır mı (read_source_code, search_code parçaları, search_wiki
// parçaları, read_wiki_page).
func isSensitiveChatTool(name string) bool {
	return name == mcptools.SourceCodeToolName || name == mcptools.SearchCodeToolName || isWikiTool(name)
}

// sensitiveChatKind — SAF: katalogdaki hassas araçların türü ("" = yok).
func sensitiveChatKind(tools []mcp.Tool) copilot.SensitiveKind {
	var k copilot.SensitiveKind
	for _, t := range tools {
		switch {
		case t.Name == mcptools.SourceCodeToolName || t.Name == mcptools.SearchCodeToolName:
			if k == "" {
				k = copilot.SensitiveCode
			} else if k == copilot.SensitiveWiki {
				k = copilot.SensitiveCode + "+" + copilot.SensitiveWiki
			}
		case isWikiTool(t.Name):
			if k == "" {
				k = copilot.SensitiveWiki
			} else if k == copilot.SensitiveCode {
				k = copilot.SensitiveCode + "+" + copilot.SensitiveWiki
			}
		}
	}
	return k
}

// dropSensitiveChatTools — SAF: kod/wiki araçlarını ADIYLA düşürür (sıra
// korunur). Sonuç, o özellikler kapalıyken kurulan katalogla aynıdır.
func dropSensitiveChatTools(tools []mcp.Tool) []mcp.Tool {
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if !isSensitiveChatTool(t.Name) {
			out = append(out, t)
		}
	}
	return out
}

// localOnlyChatDecision — sohbet döngüsü kataloğunun localOnly kararı.
type localOnlyChatDecision int

const (
	localOnlyNoSensitive localOnlyChatDecision = iota // katalogda kod/wiki aracı yok
	localOnlyKeep                                     // çözülen profil yerel: aynen + işaret
	localOnlySwitch                                   // yerel profile geçildi: katalog o profilin araç setiyle yeniden kurulur
	localOnlyDrop                                     // yerel profil yok: araçlar düşer, prompt bayt bayt eski
)

// chatLocalOnly — katalogda kod/wiki aracı varsa ctx'i yerel profile sabitler
// ve işaretler; yerel profil yoksa localOnlyDrop (çağıran araçları düşürür).
func (s *Server) chatLocalOnly(ctx context.Context, tools []mcp.Tool) (context.Context, localOnlyChatDecision) {
	kind := sensitiveChatKind(tools)
	if kind == "" {
		return ctx, localOnlyNoSensitive
	}
	before := s.copilot.ProfileLocalOnly(ctx)
	rctx, ok := s.routeSensitive(ctx, kind)
	switch {
	case !ok:
		return ctx, localOnlyDrop
	case before:
		return rctx, localOnlyKeep
	default:
		return rctx, localOnlySwitch
	}
}
