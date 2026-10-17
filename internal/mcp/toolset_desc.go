package mcp

// toolset_desc.go — v0.10.1302 — CoSRE: modele göre araç seti (küçük modelde
// serbest-sorgu araçları gizli).
//
// Sohbet kataloğunun iki varyantı var (full | simple; mcptools.ChatToolListFor).
// simple varyantta gizli bir aracı ANAN kompakt açıklama (ör. query_logs'un
// "Lucene sorgusu: search_logs.") modeli sunulmayan araca yönlendirirdi. İkinci
// bir açıklama alanı yerine AYNI literalde satır içi işaret:
//
//	"… ≤50 satır.⟦ Lucene sorgusu: search_logs.|⟧"
//
// ⟦A|B⟧ → full varyantta A, simple varyantta B. İşaretin içinde TAM BİR "|"
// olur (A ve B "|" taşıyamaz); bozuk işaret olduğu gibi kalır ve mcptools'un
// "işlenmiş metinde ⟦ yok" kapısı onu yakalar. MCP tools/list Description'ı
// servis eder — işaretler dış istemciye hiç gitmez.

import (
	"context"
	"strings"
)

const (
	toolsetMarkOpen  = "⟦"
	toolsetMarkClose = "⟧"
	toolsetMarkSep   = "|"
)

// ChatDescriptionFor — kataloğun sohbet görünümü, araç setine göre.
// simple=false ChatDescription() ile aynıdır. ShortDescription boşsa tam
// Description'a düşer (güvenli yön; işaret işlenmez). SAF.
func (t Tool) ChatDescriptionFor(simple bool) string {
	if t.ShortDescription == "" {
		return t.Description
	}
	return RenderToolsetMarkers(t.ShortDescription, simple)
}

// RenderToolsetMarkers — SAF: ⟦full|simple⟧ işaretlerini çözer. İşaretsiz
// metin aynen döner.
func RenderToolsetMarkers(s string, simple bool) string {
	if !strings.Contains(s, toolsetMarkOpen) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for {
		i := strings.Index(s, toolsetMarkOpen)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		rest := s[i+len(toolsetMarkOpen):]
		j := strings.Index(rest, toolsetMarkClose)
		if j < 0 {
			b.WriteString(s) // kapanmamış → olduğu gibi (kapı yakalar)
			return b.String()
		}
		body := rest[:j]
		after := rest[j+len(toolsetMarkClose):]
		full, simpleSide, ok := strings.Cut(body, toolsetMarkSep)
		if !ok || strings.Contains(simpleSide, toolsetMarkSep) || strings.Contains(body, toolsetMarkOpen) {
			// bozuk işaret → olduğu gibi (kapı yakalar)
			b.WriteString(s[:i+len(toolsetMarkOpen)+j+len(toolsetMarkClose)])
			s = after
			continue
		}
		b.WriteString(s[:i])
		if simple {
			b.WriteString(simpleSide)
		} else {
			b.WriteString(full)
		}
		s = after
	}
}

// ── v0.10.1302 — araç seti BAĞLAMI: araç sonuçları ve şemalar ───────────
//
// Kompakt açıklamalar tek yer değil: araç SONUÇLARI (hint/next/not/hata
// metni) ve şema özellik açıklamaları da modele gider. Aynı ⟦full|simple⟧
// işareti orada da kullanılır; hangi tarafın basılacağını çağrının ctx'i
// söyler. Sohbetin serbest döngüsü simple varyantta WithSimpleToolset(ctx,
// true) koyar (mcptools.WithToolset); dış MCP istemcisi ve guided/niyet
// kademeleri işaretsiz ctx ile koşar → full (bugünkü metin).

type simpleToolsetKey struct{}

// WithSimpleToolset — bu çağrı zincirinde simple araç seti mi.
func WithSimpleToolset(ctx context.Context, simple bool) context.Context {
	return context.WithValue(ctx, simpleToolsetKey{}, simple)
}

// SimpleToolsetFrom — ctx simple araç setinde mi; işaretsiz ctx → false (full).
func SimpleToolsetFrom(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(simpleToolsetKey{}).(bool)
	return v
}

// ToolsetText — araç sonucundaki işaretli metnin ctx'in araç setine göre hâli.
func ToolsetText(ctx context.Context, s string) string {
	return RenderToolsetMarkers(s, SimpleToolsetFrom(ctx))
}

// RenderSchemaToolsetMarkers — SAF: JSON şemasındaki dizge değerlerde
// işaretleri çözer. İşaret taşımayan alt ağaç PAYLAŞILIR (kopyalanmaz);
// taşıyan yol kopyalanır — kayıt defterinin şeması asla yerinde değişmez.
func RenderSchemaToolsetMarkers(schema map[string]any, simple bool) map[string]any {
	out, _ := renderSchemaValue(schema, simple)
	m, _ := out.(map[string]any)
	if m == nil && schema != nil {
		return schema
	}
	return m
}

func renderSchemaValue(v any, simple bool) (any, bool) {
	switch x := v.(type) {
	case string:
		if !strings.Contains(x, toolsetMarkOpen) {
			return x, false
		}
		return RenderToolsetMarkers(x, simple), true
	case map[string]any:
		var cp map[string]any
		for k, e := range x {
			if r, changed := renderSchemaValue(e, simple); changed {
				if cp == nil {
					cp = make(map[string]any, len(x))
					for k2, e2 := range x {
						cp[k2] = e2
					}
				}
				cp[k] = r
			}
		}
		if cp == nil {
			return x, false
		}
		return cp, true
	case []any:
		var cp []any
		for i, e := range x {
			if r, changed := renderSchemaValue(e, simple); changed {
				if cp == nil {
					cp = append([]any(nil), x...)
				}
				cp[i] = r
			}
		}
		if cp == nil {
			return x, false
		}
		return cp, true
	}
	return v, false
}
