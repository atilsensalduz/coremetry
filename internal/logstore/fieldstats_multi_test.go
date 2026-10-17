package logstore

// v0.10.1284 — AI SRE Faz 1a: explain_log_spike (log artışını ne açıklıyor).
// FieldStatsMany: ES tek _search gövdesi (saf) + yeteneksiz backend'de
// alan başına FieldStats geri düşüşü.

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestBuildFieldStatsMultiBody(t *testing.T) {
	q := map[string]any{"match_all": map[string]any{}}
	body := buildFieldStatsMultiBody(q, []string{"k8s.pod.name.keyword", "log.logger"}, 10, "4s")
	if body["size"] != 0 || body["track_total_hits"] != false || body["timeout"] != "4s" {
		t.Fatalf("maliyet korumaları eksik: %v", body)
	}
	aggs, _ := body["aggs"].(map[string]any)
	if len(aggs) != 2 {
		t.Fatalf("alan başına bir agg bekleniyordu, %d", len(aggs))
	}
	for i, want := range []string{"k8s.pod.name.keyword", "log.logger"} {
		a, _ := aggs["f"+string(rune('0'+i))].(map[string]any)
		terms, _ := a["terms"].(map[string]any)
		if terms["field"] != want || terms["size"] != 10 || terms["shard_size"] != 100 {
			t.Fatalf("f%d: %v", i, terms)
		}
	}
	// Agg adı alan adını taşımaz (nokta agg yolunda anlam taşır).
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), `"k8s.pod.name.keyword":{`) {
		t.Fatal("alan adı agg adı olarak kullanılmış")
	}
}

func TestDecodeFieldStatsMulti(t *testing.T) {
	var r esFieldStatsMultiResponse
	if err := json.Unmarshal([]byte(`{"timed_out":true,"_shards":{"failed":0},"aggregations":{
		"f0":{"sum_other_doc_count":5,"buckets":[{"key":"pod-a","doc_count":70},{"key":"pod-b","doc_count":25}]},
		"f1":{"sum_other_doc_count":0,"buckets":[]}}}`), &r); err != nil {
		t.Fatal(err)
	}
	out := decodeFieldStatsMulti(r, []string{"k8s.pod.name", "log.logger", "missing"})
	pod := out["k8s.pod.name"]
	if pod == nil || pod.Total != 100 || len(pod.Values) != 2 || pod.Values[0].Value != "pod-a" || !pod.Partial {
		t.Fatalf("pod: %+v", pod)
	}
	if lg := out["log.logger"]; lg == nil || lg.Total != 0 || len(lg.Values) != 0 {
		t.Fatalf("boş alan da haritada olmalı: %+v", lg)
	}
	if m := out["missing"]; m == nil || m.Values == nil {
		t.Fatalf("cevapta olmayan agg boş sonuç olmalı: %+v", m)
	}
}

// fieldStore — FieldStats'i alan başına senaryolar; yetenek YOK (CH gibi).
type fieldStore struct {
	Store
	mu    sync.Mutex
	calls []string
	fail  map[string]bool
}

func (s *fieldStore) FieldStats(_ context.Context, _ Filter, field string, limit int) (*FieldStatsResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, field)
	s.mu.Unlock()
	if s.fail[field] {
		return nil, errors.New("boom " + field)
	}
	return &FieldStatsResult{Field: field, Total: 10, Values: []FieldValueCount{{Value: field + "-v", Count: 10}}}, nil
}

func (s *fieldStore) Backend() string { return "clickhouse" }

// multiStore — yetenekli backend: tek çağrı.
type multiStore struct {
	fieldStore
	multiCalls int
}

func (s *multiStore) FieldStatsMulti(_ context.Context, _ Filter, fields []string, _ int) (map[string]*FieldStatsResult, error) {
	s.multiCalls++
	out := map[string]*FieldStatsResult{}
	for _, f := range fields {
		out[f] = &FieldStatsResult{Field: f, Values: []FieldValueCount{}}
	}
	return out, nil
}

func TestFieldStatsManyFallback(t *testing.T) {
	st := &fieldStore{fail: map[string]bool{"bad": true}}
	out, err := FieldStatsMany(context.Background(), st, Filter{}, []string{"a", "b", "", "a", "bad"}, 10)
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("okunamayan alan hatası birleşik dönmeli: %v", err)
	}
	if len(out) != 2 || out["a"] == nil || out["b"] == nil {
		t.Fatalf("okunan alanlar haritada: %v", out)
	}
	sort.Strings(st.calls)
	if strings.Join(st.calls, ",") != "a,b,bad" {
		t.Fatalf("tekrarsız alan başına bir çağrı: %v", st.calls)
	}

	// Yetenek varsa tek çağrı (Switchable sarmalı içinden de — Unwrap).
	ms := &multiStore{}
	sw := NewSwitchable(ms)
	out, err = FieldStatsMany(context.Background(), sw, Filter{}, []string{"a", "b"}, 10)
	if err != nil || len(out) != 2 || ms.multiCalls != 1 || len(ms.calls) != 0 {
		t.Fatalf("yetenek tek çağrı olmalı: err=%v out=%d multi=%d single=%v", err, len(out), ms.multiCalls, ms.calls)
	}

	// Boş liste: çağrı yok.
	if out, err := FieldStatsMany(context.Background(), st, Filter{}, nil, 10); err != nil || len(out) != 0 {
		t.Fatalf("boş: %v %v", out, err)
	}
}
