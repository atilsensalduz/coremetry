package logstore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// v0.10.1303 — log desenleri gereksiz ES point-in-time açıyordu.
//
// Kök neden: GroupBySignatureN her sayfayı WantCursor=true ile istiyordu.
// ES Search WantCursor'da önce PIT açar (_pit, +1 istek) ve dolu sayfada
// PIT'i imlece koyup 2 dk TUTAR (esRetainPIT). Tek sayfalık çağıranlar
// (desen açıklaması 500, Trend tabanı 500, explain_log_spike 500) imleci
// hiç kullanmadığı için her çağrı = PIT open + search + 2 dk segment
// tutma; çok sayfalı okumanın (2000) dolu SON sayfası da PIT'i sızdırıyordu.
// Önce: tek sayfa = open + search (close yok, PIT 2 dk açık).
// Sonra: tek sayfa = 1 search; çok sayfa = open + N search + close.

func TestPatternsPageWantsCursor(t *testing.T) {
	cases := []struct {
		remaining, limit int
		want             bool
	}{
		{500, 500, false}, // tek sayfa (desen açıklaması / taban / log_spike)
		{300, 300, false}, // tavan sayfadan küçük
		{2000, 500, true}, // çok sayfalı okumanın ilk sayfası
		{1000, 500, true}, // ardından bir sayfa daha var
		{501, 500, true},  // tek satır bile bir sayfa daha demek
		{0, 0, false},     // savunma: hiçbir şey kalmadı
	}
	for _, c := range cases {
		if got := patternsPageWantsCursor(c.remaining, c.limit); got != c.want {
			t.Errorf("patternsPageWantsCursor(%d,%d) = %v, want %v", c.remaining, c.limit, got, c.want)
		}
	}
}

// Sayfa başına ilan edilen WantCursor dizisi: imleç yalnız arkasından sayfa
// gelecekse; son (veya tek) sayfa imleçsiz.
func TestGroupBySignatureNCursorOnlyWhenPaging(t *testing.T) {
	rows := make([]*LogRecord, 0, 3000)
	for i := 0; i < 3000; i++ {
		rows = append(rows, rec(int64(i+1), "s", fmt.Sprintf("tick %d", 1000+i), 9))
	}
	cases := []struct {
		name      string
		sampleCap int
		wants     []bool
	}{
		{"desen açıklaması / taban / log_spike (500)", 500, []bool{false}},
		{"küçük tavan (300)", 300, []bool{false}},
		{"iki sayfa (700)", 700, []bool{true, false}},
		{"/logs paneli (2000)", 2000, []bool{true, true, true, false}},
		{"varsayılan (0 → 2000)", 0, []bool{true, true, true, false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := &pagedStore{rows: rows}
			res, err := GroupBySignatureN(context.Background(), st, Filter{WantCursor: true}, 10, c.sampleCap)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(st.wants, c.wants) {
				t.Fatalf("WantCursor/sayfa = %v, want %v", st.wants, c.wants)
			}
			if res.Sampled != res.Cap {
				t.Fatalf("sampled %d, cap %d — çok sayfalı yol hâlâ sayfalamalı", res.Sampled, res.Cap)
			}
		})
	}
}

// esPITCounter — ES taklidi: _pit açma/kapama ve _search isteklerini sayar.
// Her _search istenen boyda DOLU sayfa döner (10B doküman/gün ölçeğinde ilk
// sayfa hep doludur — sızıntıyı tetikleyen koşul tam olarak bu).
type esPITCounter struct {
	mu                         sync.Mutex
	pitOpen, pitClose, searchN int
	pitSearches                int // gövdesinde pit taşıyan _search
	seq                        int
}

func (c *esPITCounter) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Elastic-Product", "Elasticsearch")
	w.Header().Set("Content-Type", "application/json")
	p := r.URL.Path
	switch {
	case p == "/":
		_, _ = io.WriteString(w, `{"name":"n1","cluster_name":"logs-a","version":{"number":"8.13.0"},"tagline":"You Know, for Search"}`)
	case strings.HasSuffix(p, "/_pit") && r.Method == http.MethodPost:
		c.mu.Lock()
		c.pitOpen++
		c.mu.Unlock()
		_, _ = io.WriteString(w, `{"id":"pit-1"}`)
	case p == "/_pit" && r.Method == http.MethodDelete:
		c.mu.Lock()
		c.pitClose++
		c.mu.Unlock()
		_, _ = io.WriteString(w, `{"succeeded":true,"num_freed":1}`)
	case strings.HasSuffix(p, "/_search"):
		var body struct {
			Size int             `json:"size"`
			PIT  json.RawMessage `json:"pit"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		c.mu.Lock()
		c.searchN++
		if len(body.PIT) > 0 {
			c.pitSearches++
		}
		hits := make([]map[string]any, 0, body.Size)
		base := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC).UnixMilli()
		for i := 0; i < body.Size; i++ {
			c.seq++
			ts := base - int64(c.seq)
			hits = append(hits, map[string]any{
				"_id":     fmt.Sprintf("d%d", c.seq),
				"_source": map[string]any{"message": fmt.Sprintf("tick %d", 1000+c.seq), "@timestamp": time.UnixMilli(ts).UTC().Format(time.RFC3339Nano)},
				"sort":    []any{ts, c.seq},
			})
		}
		c.mu.Unlock()
		out := map[string]any{"took": 1, "timed_out": false,
			"_shards": map[string]any{"total": 1, "successful": 1, "failed": 0},
			"hits":    map[string]any{"total": map[string]any{"value": 10000, "relation": "gte"}, "hits": hits}}
		if len(body.PIT) > 0 {
			out["pit_id"] = "pit-1"
		}
		_ = json.NewEncoder(w).Encode(out)
	default:
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found","reason":"stub"},"status":404}`)
	}
}

// Gerçek ES istemcisi + sahte taşıma: çağıranın örnek tavanına göre istek
// sayıları. Tek sayfalık çağıranlar PIT'e HİÇ dokunmaz; çok sayfalı okuma
// PIT'i açar, sayfalar ve sonda KAPATIR (2 dk tutmaz).
func TestGroupBySignatureN_ESRequestCounts(t *testing.T) {
	cases := []struct {
		name                        string
		sampleCap                   int
		pitOpen, searches, pitClose int
	}{
		{"log_patterns_explain (500)", 500, 0, 1, 0},
		{"api_logs_patterns taban (500)", 500, 0, 1, 0},
		{"explain_log_spike (500)", 500, 0, 1, 0},
		{"/logs paneli (2000)", 2000, 1, 4, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cnt := &esPITCounter{}
			srv := httptest.NewServer(http.HandlerFunc(cnt.handler))
			t.Cleanup(srv.Close)
			es, err := NewES(ESConfig{Addresses: []string{srv.URL}, Fields: ESFieldMap{Body: "message", Timestamp: "@timestamp"}})
			if err != nil {
				t.Fatalf("NewES: %v", err)
			}
			now := time.Now()
			res, err := GroupBySignatureN(context.Background(), es, Filter{From: now.Add(-time.Hour), To: now}, 10, c.sampleCap)
			if err != nil {
				t.Fatal(err)
			}
			if res.Sampled != c.sampleCap {
				t.Fatalf("sampled %d, want %d", res.Sampled, c.sampleCap)
			}
			cnt.mu.Lock()
			defer cnt.mu.Unlock()
			if cnt.pitOpen != c.pitOpen || cnt.searchN != c.searches || cnt.pitClose != c.pitClose {
				t.Fatalf("istekler: pitOpen=%d search=%d pitClose=%d; want %d/%d/%d",
					cnt.pitOpen, cnt.searchN, cnt.pitClose, c.pitOpen, c.searches, c.pitClose)
			}
			if c.pitOpen == 0 && cnt.pitSearches != 0 {
				t.Fatalf("tek sayfalık okuma PIT'li _search göndermemeli (%d)", cnt.pitSearches)
			}
			if c.pitOpen > 0 && cnt.pitSearches != c.searches {
				t.Fatalf("çok sayfalı okumanın her sayfası PIT içinde olmalı: %d/%d", cnt.pitSearches, c.searches)
			}
		})
	}
}
