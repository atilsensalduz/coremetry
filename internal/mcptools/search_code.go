package mcptools

// search_code — v0.10.1208: servisin deposunda Azure DevOps Code Search.
// Depo YALNIZ servisten çözülür (api: devops.ResolveRepo — service_metadata
// pini ya da adlandırma kuralı); model depo seçemez. Çıktı dosya + (sunucu
// verirse) satır + kısa parça; kodu okumak için read_source_code. Koşullu
// sohbet aracı: Deps.CodeSearch nil (DevOps / Code Search kapalı) → sunulmaz.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/mcp"
)

const (
	SearchCodeToolName = "search_code"
	searchCodeMax      = 10
)

// CodeSearchResult — api tarafının cevabı.
type CodeSearchResult struct {
	Outcome    string                  `json:"outcome"` // ok | no_repo | no_hits | error
	Repo       string                  `json:"repo,omitempty"`
	RepoSource string                  `json:"repo_source,omitempty"`
	Hits       []devops.ServiceCodeHit `json:"hits"`
	Hint       string                  `json:"hint,omitempty"`
}

// CodeSearcher — api/chat_code_search.go; nil = araç sunulmaz.
type CodeSearcher func(ctx context.Context, service, query string, limit int) (CodeSearchResult, error)

type searchCodeArgs struct {
	Service string `json:"service"`
	Query   string `json:"query"`
	Limit   int    `json:"limit,omitempty"`
}

func searchCodeTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             SearchCodeToolName,
		ShortDescription: "Servisin deposunda kod ara (Code Search): dosya, satır, kısa parça.",
		Description: "Search the source code of ONE service with Azure DevOps Code Search. The repository is derived from the service name (never pass a repo). " +
			"Returns file paths with line and a short snippet when the server provides them; then call read_source_code with service + file (+ line) to read the code. " +
			"Use exact identifiers (class, method, error code, log text) as query. limit default 5, max 10. Read-only.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service": map[string]any{"type": "string", "description": "Service name (resolve it first if unsure)."},
				"query":   map[string]any{"type": "string", "description": "Text to search: identifier, error code, log message fragment."},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": searchCodeMax, "description": "Hits. Default 5."},
			},
			"required": []string{"service", "query"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a searchCodeArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("decode args: %w", err)
			}
			a.Service, a.Query = strings.TrimSpace(a.Service), strings.TrimSpace(a.Query)
			if a.Service == "" || a.Query == "" {
				return nil, fmt.Errorf("service and query required")
			}
			if d.CodeSearch == nil {
				return CodeSearchResult{Outcome: "error", Hits: []devops.ServiceCodeHit{}, Hint: "kod arama yapılandırılmamış"}, nil
			}
			return d.CodeSearch(ctx, a.Service, a.Query, clampLimit(a.Limit, 5, searchCodeMax))
		},
	}
}
