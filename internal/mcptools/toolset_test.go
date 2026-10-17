package mcptools

// v0.10.1302 — CoSRE: modele göre araç seti (küçük modelde serbest-sorgu
// araçları gizli). simple varyant dört aracı sohbetten çıkarır; full varyant,
// ChatToolList ve dış MCP (Register → tools/list) onları taşımaya devam eder.
// simple katalogdaki hiçbir kompakt metin gizli bir aracı anmaz ve hiçbir
// işlenmiş metinde işaret (⟦ ⟧) kalmaz.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcp"
)

var simpleHiddenNames = []string{"query_metric", "search_logs", "search_traces", "list_metric_labels"}

func toolNames(ts []mcp.Tool) map[string]bool {
	out := map[string]bool{}
	for _, t := range ts {
		out[t.Name] = true
	}
	return out
}

func TestChatToolListForHidesFreeFormToolsOnlyInSimple(t *testing.T) {
	if len(simpleHiddenTools) != len(simpleHiddenNames) {
		t.Fatalf("simpleHiddenTools %d ad, beklenen %d", len(simpleHiddenTools), len(simpleHiddenNames))
	}
	chat := ChatToolList(Deps{})
	full := ChatToolListFor(Deps{}, "full")
	auto := ChatToolListFor(Deps{}, "")
	simple := ChatToolListFor(Deps{}, "simple")
	inChat, inFull, inAuto, inSimple := toolNames(chat), toolNames(full), toolNames(auto), toolNames(simple)
	for _, n := range simpleHiddenNames {
		if !inChat[n] || !inFull[n] || !inAuto[n] {
			t.Errorf("%s ChatToolList / full / boş varyantta olmalı", n)
		}
		if inSimple[n] {
			t.Errorf("%s simple varyantta GİZLİ olmalı", n)
		}
	}
	if len(full) != len(chat) || len(simple) != len(chat)-len(simpleHiddenNames) {
		t.Fatalf("boylar: chat %d full %d simple %d", len(chat), len(full), len(simple))
	}
	// Yapısal karşılıklar simple'da kalır.
	for _, n := range []string{"query_metrics", "query_logs", "query_traces", "list_metric_names", "trace_stats"} {
		if !inSimple[n] {
			t.Errorf("%s simple varyantta olmalı", n)
		}
	}
	// Sıra korunur: simple, ChatToolList'in alt dizisi.
	i := 0
	for _, tl := range chat {
		if simpleHiddenTools[tl.Name] {
			continue
		}
		if simple[i].Name != tl.Name {
			t.Fatalf("simple sıra sapıyor: %d. %q ≠ %q", i, simple[i].Name, tl.Name)
		}
		i++
	}
	for i := range chat {
		if full[i].Name != chat[i].Name {
			t.Fatalf("full sıra sapıyor: %d", i)
		}
	}
}

// Dış MCP etkilenmez: tools/list gizli dördü de servis eder.
func TestRegisterStillServesHiddenTools(t *testing.T) {
	srv := mcp.New("coremetry-test", "v0.0.0-test")
	Register(srv, Deps{})
	var out struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(mcpToolsListRaw(t, srv)), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tl := range out.Result.Tools {
		got[tl.Name] = true
		if strings.ContainsAny(tl.Description, "⟦⟧") {
			t.Errorf("%s: tools/list açıklamasında araç seti işareti", tl.Name)
		}
	}
	for _, n := range simpleHiddenNames {
		if !got[n] {
			t.Errorf("dış MCP tools/list %s'i servis etmeli", n)
		}
	}
}

var hiddenToolNameRe = regexp.MustCompile(`\b(query_metric|search_logs|search_traces|list_metric_labels)\b`)

func TestSimpleChatDescriptionsNeverNameHiddenTools(t *testing.T) {
	marked := 0
	for _, tl := range ToolList(Deps{}) {
		if strings.Contains(tl.ShortDescription, "⟦") {
			marked++
		}
		for _, simple := range []bool{false, true} {
			if txt := tl.ChatDescriptionFor(simple); strings.ContainsAny(txt, "⟦⟧") {
				t.Errorf("%s (simple=%v): işlenmiş metinde işaret kaldı (bozuk ⟦full|simple⟧?): %q", tl.Name, simple, txt)
			}
		}
		if simpleHiddenTools[tl.Name] || externalOnlyTools[tl.Name] {
			continue
		}
		if m := hiddenToolNameRe.FindString(tl.ChatDescriptionFor(true)); m != "" {
			t.Errorf("%s: simple kompakt metin gizli aracı anıyor (%s): %q", tl.Name, m, tl.ChatDescriptionFor(true))
		}
	}
	// query_metrics / query_logs / query_traces / list_metric_names /
	// get_log_histogram / list_clusters — altı işaretli metin.
	if marked != 6 {
		t.Errorf("işaretli kompakt metin %d, beklenen 6", marked)
	}
}

// mcpToolsListRaw — dış MCP tools/list cevabının ham gövdesi (Streamable-HTTP).
func mcpToolsListRaw(t *testing.T, srv *mcp.Server) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/mcp", srv.HandleStreamable)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/mcp", "application/json",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
