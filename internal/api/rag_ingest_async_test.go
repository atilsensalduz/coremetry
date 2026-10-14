package api

// rag_ingest_async_test.go — v0.10.1173 (Operator-reported: büyük belge
// yüklerken 60 sn zaman aşımı): yükleme isteği indekslemeyi arkaya bırakır;
// durum listesi süresi geçen hatayı atar.

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestRAGPendingList(t *testing.T) {
	now := time.Now()
	setRAGPending(ragPending{DocID: "t-a", Name: "a.xlsx", State: "indexing", At: now.UnixNano()})
	setRAGPending(ragPending{DocID: "t-b", Name: "b.csv", State: "failed", Error: "x", At: now.Add(-time.Hour).UnixNano()})
	setRAGPending(ragPending{DocID: "t-c", Name: "c.pdf", State: "failed", Error: "y", At: now.Add(-time.Minute).UnixNano()})
	defer func() { clearRAGPending("t-a"); clearRAGPending("t-b"); clearRAGPending("t-c") }()
	got := map[string]string{}
	for _, p := range ragPendingList(now) {
		got[p.DocID] = p.State
	}
	if got["t-a"] != "indexing" || got["t-c"] != "failed" {
		t.Fatalf("got %v", got)
	}
	if _, ok := got["t-b"]; ok {
		t.Fatal("30 dk'dan eski hata atılmalı")
	}
}

func TestUploadIndexesInBackground(t *testing.T) {
	b, _ := os.ReadFile("rag.go")
	src := string(b)
	i := strings.Index(src, "func (s *Server) uploadRAGDocument(")
	j := strings.Index(src[i:], "\n}\n")
	body := src[i : i+j]
	if strings.Contains(body, "s.ragIngestDocument(r.Context()") {
		t.Fatal("yükleme isteği indekslemeyi istek bağlamında SENKRON yapmamalı (60 sn zaman aşımı)")
	}
	if !strings.Contains(body, "s.ragIngestInBackground(") || !strings.Contains(body, "http.StatusAccepted") {
		t.Fatal("yükleme arka plan indekslemesi + 202 dönmeli")
	}
}
