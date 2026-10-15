package devops

// search_service_code.go — v0.10.1208: CoSRE search_code aracı. Servisin
// deposu (ResolveRepo — adlandırma kuralı / service_metadata) içinde Code
// Search; dosya + (sunucu verirse) satır + kısa parça. Ardından
// read_source_code o dosyayı okur. Settings.CodeSearch kapalıysa çağrılmaz.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ServiceCodeHit — depo-içi bir arama isabeti.
type ServiceCodeHit struct {
	Path    string `json:"file"`
	Branch  string `json:"branch,omitempty"`
	Line    int    `json:"line,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

// CodeSearchEnabled — ayar + yapılandırma birlikte.
func (s *Service) CodeSearchEnabled() bool {
	return s != nil && s.Configured() && s.CurrentSettings().CodeSearch
}

// SearchServiceCode — repo süzgeçli Code Search, ≤top isabet.
func (s *Service) SearchServiceCode(ctx context.Context, repo, text string, top int) ([]ServiceCodeHit, error) {
	text, repo = strings.TrimSpace(text), strings.TrimSpace(repo)
	if text == "" || repo == "" {
		return nil, fmt.Errorf("arama metni ve depo gerekli")
	}
	if !s.CodeSearchEnabled() {
		return nil, fmt.Errorf("kod arama kapalı (Ayarlar → Kod entegrasyonu → Code Search)")
	}
	cfg := s.CurrentSettings()
	cli := s.clientFor(cfg.InsecureSkipVerify)
	body, err := json.Marshal(searchCodeBody{SearchText: text, Top: top, Filters: map[string]any{"Repository": []string{repo}}})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL(cfg), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "coremetry-devops/1.0")
	if cfg.PAT != "" || cfg.Username != "" {
		req.SetBasicAuth(cfg.Username, cfg.PAT)
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, fileBodyCap))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("kod arama http %d: %s", resp.StatusCode, firstLine(string(raw)))
	}
	return parseServiceCodeHits(raw, repo, top)
}

// parseServiceCodeHits — SAF: yanıt → isabetler; başka depo / TFVC elenir,
// parça tek satıra ve 200 karaktere kırpılır.
func parseServiceCodeHits(raw []byte, repo string, top int) ([]ServiceCodeHit, error) {
	var out searchCodeResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("kod arama yanıtı çözümlenemedi: %w", err)
	}
	hits := []ServiceCodeHit{}
	for _, r := range out.Results {
		if !strings.EqualFold(r.Repository.Name, repo) || strings.EqualFold(r.Repository.Type, "tfvc") {
			continue
		}
		h := ServiceCodeHit{Path: strings.TrimPrefix(r.Path, "/")}
		if len(r.Versions) > 0 {
			h.Branch = strings.TrimPrefix(r.Versions[0].BranchName, "refs/heads/")
		}
		for _, m := range r.Matches.Content {
			if m.Line > 0 {
				h.Line = m.Line
				sn := strings.TrimSpace(strings.ReplaceAll(m.CodeSnippet, "\n", " "))
				if len([]rune(sn)) > 200 {
					sn = string([]rune(sn)[:200]) + "…"
				}
				h.Snippet = sn
				break
			}
		}
		hits = append(hits, h)
		if len(hits) == top {
			break
		}
	}
	return hits, nil
}
