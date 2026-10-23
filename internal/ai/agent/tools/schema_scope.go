package tools

// schema_scope.go — v0.10.1456 (CoSRE araştırma programı C1, karar 6): araç
// argümanı şema doğrulaması Executor'ın Scope dikişinde.
//
// Kipler (yüzey başına; ayar system_settings `ai.toolArgValidation`,
// chstore/ai_toolarg_settings.go):
//
//	off     — doğrulama yok (bugünkü davranış; Next'e aynen geçer).
//	warn    — doğrular, Observe + log + metrik; çağrı AYNEN koşar. Modelin
//	          gördüğü hiçbir şey değişmez (sohbetin ilk hafta kipi — operatör
//	          kararı 3: önce ölç, sonra kapat).
//	enforce — sorun varsa handler KOŞMAZ: mcp.ArgIssuesErr → Executor
//	          Kind=bad_args, modele alan-adlı ipucu (retryable). Aynı araç için
//	          ARDIŞIK ikinci ret Final=true ("bu aracı bırak", retryable=false)
//	          — karar 6 "1 retry, sonra skipped". Geçerli bir çağrı sayacı
//	          sıfırlar. AI SRE döngüsünün (2b) kipi.
//
// Bileşim: doğrulamadan SONRA Next.Constrain (G13 kapsamı ileride buraya
// zincirlenir); Next nil = kısıtsız. Şeması olmayan ad (dış MCP aracı)
// doğrulanmaz. SchemaScope durumludur (ardışık ret sayacı) → Executor
// başına bir örnek; Executor gibi tek alışveriş/dal ömrü.

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"

	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/selfobs"
)

// Doğrulama kipleri.
const (
	ArgModeOff     = "off"
	ArgModeWarn    = "warn"
	ArgModeEnforce = "enforce"
)

// ValidArgMode — kip adı tanınıyor mu.
func ValidArgMode(m string) bool {
	return m == ArgModeOff || m == ArgModeWarn || m == ArgModeEnforce
}

// argBadRetries — enforce: aynı araç için Final'dan önce izin verilen ret
// sayısı (karar 6: bir yeniden deneme).
const argBadRetries = 1

// SchemaScope — Scope uygulaması. Alanlar kurulumda doldurulur, sonra
// değişmez; yalnız ret sayacı mutex'li.
type SchemaScope struct {
	// Schemas — araç adı → InputSchema (mcp.Tool.InputSchema; işaretli
	// ⟦full|simple⟧ açıklamalar doğrulamayı etkilemez).
	Schemas map[string]map[string]any
	// Mode — off | warn | enforce; tanınmayan değer off sayılır.
	Mode string
	// Next — doğrulamadan sonra zincirlenen kapsam; nil = kısıtsız.
	Next Scope
	// Observe — her doğrulanan çağrının kararı (issues nil = geçerli);
	// çağıran ai_calls notu / span özniteliği / adım notu için kullanır.
	// Koordinatör goroutine'inde, span açılmadan HEMEN önce çağrılır.
	Observe func(name, mode string, issues []mcp.ArgIssue)

	mu  sync.Mutex
	bad map[string]int
}

// NewSchemaScope — kurucu (sıfır değer de kullanılabilir).
func NewSchemaScope(schemas map[string]map[string]any, mode string, next Scope) *SchemaScope {
	return &SchemaScope{Schemas: schemas, Mode: mode, Next: next}
}

// Constrain — Scope sözleşmesi.
func (s *SchemaScope) Constrain(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	mode := s.Mode
	if mode != ArgModeWarn && mode != ArgModeEnforce {
		return s.next(ctx, name, args)
	}
	schema, ok := s.Schemas[name]
	if !ok {
		return s.next(ctx, name, args)
	}
	issues := mcp.ValidateArgs(schema, args)
	if s.Observe != nil {
		s.Observe(name, mode, issues)
	}
	if len(issues) == 0 {
		selfobs.ObserveToolArgValidation(mode, "ok")
		if mode == ArgModeEnforce {
			s.resetBad(name)
		}
		return s.next(ctx, name, args)
	}
	if mode == ArgModeWarn {
		selfobs.ObserveToolArgValidation(mode, "invalid")
		log.Printf("[tool-args] warn %s: %s", name, IssuesSummary(issues))
		return s.next(ctx, name, args)
	}
	final := s.noteBad(name) > argBadRetries
	result := "invalid"
	if final {
		result = "gave_up"
	}
	selfobs.ObserveToolArgValidation(mode, result)
	log.Printf("[tool-args] enforce %s (%s): %s", name, result, IssuesSummary(issues))
	return nil, &mcp.ArgIssuesErr{Tool: name, Issues: issues, Final: final}
}

func (s *SchemaScope) next(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	if s.Next == nil {
		return args, nil
	}
	return s.Next.Constrain(ctx, name, args)
}

func (s *SchemaScope) noteBad(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bad == nil {
		s.bad = map[string]int{}
	}
	s.bad[name]++
	return s.bad[name]
}

func (s *SchemaScope) resetBad(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bad, name)
}

// IssuesSummary — SAF: log / ai_calls notu için kısa "alan:kural" listesi
// (değer YOK: modelin argümanı log satırına taşınmaz).
func IssuesSummary(issues []mcp.ArgIssue) string {
	parts := make([]string, 0, len(issues))
	for _, is := range issues {
		p := is.Path
		if p == "" {
			p = "(kök)"
		}
		parts = append(parts, p+":"+is.Rule)
	}
	return strings.Join(parts, ",")
}
