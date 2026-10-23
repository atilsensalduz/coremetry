package api

// chat_resolved_code_scope.go — v0.10.1209: genel sohbette read_source_code
// kapsamı. Panel öznesi (trace / exception) yokken kod YALNIZ bu turda
// search_code ya da resolve_business_code sonucunun döndürdüğü servisler için
// okunur: servis bir araç sonucundan gelmek zorunda, model uyduramaz.
//
// v0.10.1461 (R2, chat_code_turn.go) — kapsam artık YALNIZ açık kod turunda
// kurulur (1209'un örtük yolu kalktı). Tohum: ekrandaki servis (istek /
// sayfa bağlamı). Besleyiciler genişledi: list_services (dönen ad + bulanık
// eşleşmenin matched_names'i), resolve_entity (service türü adaylar),
// get_service_health (found → çözülen ad). İlke aynı: servis EKRANDAN ya da
// bir ARAÇ SONUCUNDAN gelir, model metninden değil.

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

type resolvedCodeScopeKey struct{}

type resolvedCodeScope struct {
	mu  sync.Mutex
	set map[string]bool
}

func withResolvedCodeScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, resolvedCodeScopeKey{}, &resolvedCodeScope{set: map[string]bool{}})
}

func resolvedCodeScopeFrom(ctx context.Context) *resolvedCodeScope {
	rs, _ := ctx.Value(resolvedCodeScopeKey{}).(*resolvedCodeScope)
	return rs
}

func (r *resolvedCodeScope) add(svcs ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range svcs {
		if s != "" {
			r.set[s] = true
		}
	}
}

func (r *resolvedCodeScope) has(svc string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.set[svc]
}

// scopeServicesFrom — SAF: araç sonucundan kapsama girecek servisler.
// search_code yalnız depo çözüldüyse (ok / no_hits) argümandaki servisi;
// resolve_business_code adayların servis kırılımını verir.
func scopeServicesFrom(tool string, args json.RawMessage, res any) []string {
	switch tool {
	case mcptools.SearchCodeToolName:
		r, ok := res.(mcptools.CodeSearchResult)
		if !ok || (r.Outcome != "ok" && r.Outcome != "no_hits") {
			return nil
		}
		var a struct {
			Service string `json:"service"`
		}
		if json.Unmarshal(args, &a) != nil {
			return nil
		}
		return []string{a.Service}
	case "resolve_business_code":
		m, ok := res.(map[string]any)
		if !ok {
			return nil
		}
		cands, _ := m["candidates"].([]mcptools.BizCandidate)
		var out []string
		for _, c := range cands {
			for _, s := range c.Services {
				out = append(out, s.Service)
			}
		}
		return out
	case "list_services": // v0.10.1461
		m, ok := res.(map[string]any)
		if !ok {
			return nil
		}
		var out []string
		rows, _ := m["services"].([]chstore.ServiceSummary)
		for _, row := range rows {
			out = append(out, row.Name)
		}
		names, _ := m["matched_names"].([]string)
		return append(out, names...)
	case "resolve_entity": // v0.10.1461 — yalnız servis türü adaylar
		m, ok := res.(map[string]any)
		if !ok {
			return nil
		}
		cands, _ := m["candidates"].([]mcptools.EntityCandidate)
		var out []string
		for _, c := range cands {
			if c.Kind == entity.TypeService {
				out = append(out, c.Name)
			}
		}
		return out
	case "get_service_health": // v0.10.1461 — bulunduysa çözülen ad
		m, ok := res.(map[string]any)
		if !ok || m["found"] != true {
			return nil
		}
		if n, _ := m["matched_name"].(string); n != "" {
			return []string{n}
		}
		if row, ok := m["summary"].(chstore.ServiceSummary); ok {
			return []string{row.Name}
		}
		return nil
	}
	return nil
}

// scopeFeeders — kapsamı besleyen araçlar (wrap yalnız bunları sarar).
var scopeFeeders = map[string]bool{
	mcptools.SearchCodeToolName: true, "resolve_business_code": true,
	"list_services": true, "resolve_entity": true, "get_service_health": true, // v0.10.1461
}

// wrap — kapsamı besleyen araçların işleyicisini sarar; diğerleri aynen.
func (r *resolvedCodeScope) wrap(name string, h mcp.ToolHandler) mcp.ToolHandler {
	if !scopeFeeders[name] {
		return h
	}
	return func(ctx context.Context, args json.RawMessage) (any, error) {
		res, err := h(ctx, args)
		if err == nil {
			r.add(scopeServicesFrom(name, args, res)...)
		}
		return res, err
	}
}

// hasTool — SAF: katalogda ad var mı.
func hasTool(tools []mcp.Tool, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}
