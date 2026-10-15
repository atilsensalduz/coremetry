package api

// chat_resolved_code_scope.go — v0.10.1209: genel sohbette read_source_code
// kapsamı. Panel öznesi (trace / exception) yokken kod YALNIZ bu turda
// search_code ya da resolve_business_code sonucunun döndürdüğü servisler için
// okunur: servis bir araç sonucundan gelmek zorunda, model uyduramaz.

import (
	"context"
	"encoding/json"
	"sync"

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
	}
	return nil
}

// wrap — kapsamı besleyen iki aracın işleyicisini sarar; diğerleri aynen.
func (r *resolvedCodeScope) wrap(name string, h mcp.ToolHandler) mcp.ToolHandler {
	if name != mcptools.SearchCodeToolName && name != "resolve_business_code" {
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
