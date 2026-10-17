package logstore

// fieldstats_multi.go — v0.10.1284 (AI SRE Faz 1a, explain_log_spike): ÇOK
// alanın üst değerleri TEK okumada.
//
// FieldStats tek alan okur (Discover alan paneli, tıkla-aç). Log artışını
// açıklayan araç aynı pencerede pod / cluster / logger / hata türü … gibi
// ≤10 alana bakar; alan başına bir _search, ES'te pencere başına 6-10
// istek demekti. İsteğe bağlı yetenek (FieldMapper / TraceContextDiagnoser
// emsali — Store arayüzü DEĞİŞMEDİ, Switchable bilerek iletmez, çağıran
// Unwrap üzerinden type-assert eder):
//
//	ES  — TEK _search: size 0, alan başına bir terms agg, shard_size 10×, request_cache,
//	      yumuşak timeout. Agg yazımı (`f.keyword` mı çıplak `f` mi) cache'li
//	      field_caps / probe çözümünden gelir (termsAggField, es_terms_field.go)
//	      — çıplak bir TEXT alana terms agg 400 döner ve tek kötü alan bütün
//	      isteği düşürürdü; çözüm bilinemezse `.keyword` (unmapped → boş, 400
//	      değil).
//	CH  — yetenek yok: FieldStatsMany alan başına FieldStats'e düşer (≤3
//	      paralel). Her alan ayrı bir taramadır; çağıran CH'de aday sayısını
//	      kısar.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// FieldStatsMultier — isteğe bağlı backend yeteneği: alanların üst
// değerleri tek okumada. Sonuç haritası istenen alan adıyla anahtarlı;
// bir alanın hiç değeri yoksa boş Values ile yine bulunur.
type FieldStatsMultier interface {
	FieldStatsMulti(ctx context.Context, f Filter, fields []string, limit int) (map[string]*FieldStatsResult, error)
}

// fieldStatsManyParallel — yeteneksiz backend'de (CH) eşzamanlı FieldStats tavanı.
const fieldStatsManyParallel = 3

// fieldStatsMultiMax — tek istekteki alan tavanı (agg sayısı).
const fieldStatsMultiMax = 16

// FieldStatsMany — alanların üst değerleri: yetenek varsa tek okuma, yoksa
// alan başına FieldStats (≤3 paralel). Kısmi başarı mümkündür: harita
// okunabilen alanları taşır, err okunamayanların birleşik hatasıdır
// (çağıran "harita boş değil + err" durumunu kısmi sayar).
func FieldStatsMany(ctx context.Context, st Store, f Filter, fields []string, limit int) (map[string]*FieldStatsResult, error) {
	if st == nil {
		return nil, fmt.Errorf("log store not configured")
	}
	fields = dedupNonEmpty(fields)
	if len(fields) == 0 {
		return map[string]*FieldStatsResult{}, nil
	}
	if len(fields) > fieldStatsMultiMax {
		fields = fields[:fieldStatsMultiMax]
	}
	if m, ok := Unwrap(st).(FieldStatsMultier); ok {
		return m.FieldStatsMulti(ctx, f, fields, limit)
	}
	out := make(map[string]*FieldStatsResult, len(fields))
	var mu sync.Mutex
	var errs []error
	sem := make(chan struct{}, fieldStatsManyParallel)
	var wg sync.WaitGroup
	for _, fld := range fields {
		wg.Add(1)
		go func(fld string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", fld, ctx.Err()))
				mu.Unlock()
				return
			}
			defer func() { <-sem }()
			r, err := st.FieldStats(ctx, f, fld, limit)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", fld, err))
				return
			}
			if r == nil {
				r = &FieldStatsResult{Field: fld, Values: []FieldValueCount{}}
			}
			out[fld] = r
		}(fld)
	}
	wg.Wait()
	return out, errors.Join(errs...)
}

func dedupNonEmpty(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// buildFieldStatsMultiBody — SAF: çok alanlı _search gövdesi. aggFields[i]
// i'inci alanın terms agg yazımı; agg adı "f<i>" (alan adı agg adına
// konmaz — nokta/köşeli parantez agg yolunda anlam taşır). v0.8.3 maliyet
// korumaları: size 0, sınırlı terms, shard_size 10×, track_total_hits
// kapalı, yumuşak timeout.
func buildFieldStatsMultiBody(query any, aggFields []string, size int, esTimeout string) map[string]any {
	aggs := make(map[string]any, len(aggFields))
	for i, af := range aggFields {
		aggs[fmt.Sprintf("f%d", i)] = map[string]any{
			"terms": map[string]any{
				"field":      af,
				"size":       size,
				"shard_size": size * 10,
			},
		}
	}
	return map[string]any{
		"size":             0,
		"query":            query,
		"aggs":             aggs,
		"track_total_hits": false,
		"timeout":          esTimeout,
	}
}

// esFieldStatsMultiResponse — çok alanlı cevabın çözümü (agg adları dinamik).
type esFieldStatsMultiResponse struct {
	esSearchEnvelope
	Aggregations map[string]json.RawMessage `json:"aggregations"`
}

type esTermsAggResult struct {
	SumOther int64 `json:"sum_other_doc_count"`
	Buckets  []struct {
		Key      any   `json:"key"`
		DocCount int64 `json:"doc_count"`
	} `json:"buckets"`
}

// decodeFieldStatsMulti — SAF: cevap → alan başına sonuç. Toplam (payda)
// alanın kendi değer toplamıdır (top kovalar + sum_other; FieldStats ile
// aynı anlam).
func decodeFieldStatsMulti(raw esFieldStatsMultiResponse, fields []string) map[string]*FieldStatsResult {
	out := make(map[string]*FieldStatsResult, len(fields))
	for i, fld := range fields {
		r := &FieldStatsResult{Field: fld, Values: []FieldValueCount{}, Partial: raw.partial(), ShardsFailed: raw.Shards.Failed}
		if a, ok := raw.Aggregations[fmt.Sprintf("f%d", i)]; ok {
			var t esTermsAggResult
			if json.Unmarshal(a, &t) == nil {
				r.Total = t.SumOther
				for _, b := range t.Buckets {
					r.Total += b.DocCount
					r.Values = append(r.Values, FieldValueCount{Value: fmt.Sprintf("%v", b.Key), Count: b.DocCount})
				}
			}
		}
		out[fld] = r
	}
	return out
}

// termsAggFields — alanların terms agg yazımları (cache'li). Önbellekte
// olmayanlar için TEK field_caps (termsAggField alan başına bir tane
// atardı); field_caps yoksa alan başına termsAggField'ın probe yoluna düşer.
func (s *ESStore) termsAggFields(ctx context.Context, bares []string) []string {
	out := make([]string, len(bares))
	now := time.Now()
	var miss []int
	s.groupFields.mu.Lock()
	for i, b := range bares {
		if v, ok := s.groupFields.byAxis["terms:"+b]; ok && now.Before(v.expires) && len(v.fields) == 1 {
			out[i] = v.fields[0]
			continue
		}
		miss = append(miss, i)
	}
	s.groupFields.mu.Unlock()
	if len(miss) == 0 {
		return out
	}
	names := make([]string, 0, len(miss))
	for _, i := range miss {
		names = append(names, bares[i])
	}
	pctx, cancel := context.WithTimeout(ctx, groupFieldCapsTimeout)
	defer cancel()
	idx := s.queryIndices(pctx, Filter{From: now.Add(-24 * time.Hour), To: now})
	caps, err := s.fieldCaps(pctx, idx, envFieldCapsFields(names))
	if err != nil {
		for _, i := range miss {
			out[i] = s.termsAggField(ctx, bares[i]) // probe yolu (kendi cache'i)
		}
		return out
	}
	s.groupFields.mu.Lock()
	if s.groupFields.byAxis == nil {
		s.groupFields.byAxis = map[string]esGroupFieldsVerdict{}
	}
	for _, i := range miss {
		sp := termsAggSpellingFromCaps(bares[i], caps)
		if sp == "" {
			sp = bares[i] + ".keyword" // yalnız text / yok: unmapped `.keyword` boş döner, 400 değil
		}
		out[i] = sp
		s.groupFields.byAxis["terms:"+bares[i]] = esGroupFieldsVerdict{fields: []string{sp}, expires: now.Add(groupFieldTTL)}
	}
	s.groupFields.mu.Unlock()
	log.Printf("[logstore-es] terms agg fields %v → %v (field_caps, batched)", names, out)
	return out
}

// FieldStatsMulti — FieldStatsMultier (ES): tek _search.
func (s *ESStore) FieldStatsMulti(ctx context.Context, f Filter, fields []string, limit int) (map[string]*FieldStatsResult, error) {
	fields = dedupNonEmpty(fields)
	if len(fields) == 0 {
		return map[string]*FieldStatsResult{}, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	// Well-known UI id'leri FieldStats'teki gibi yapılandırılmış alana.
	bares := make([]string, len(fields))
	for i, fld := range fields {
		switch fld {
		case "service":
			bares[i] = s.fields.Service
		case "severity", "level":
			bares[i] = s.fields.SeverityTx
		default:
			bares[i] = strings.TrimSuffix(fld, ".keyword")
		}
	}
	aggFields := s.termsAggFields(ctx, bares)
	f, _ = s.applyEnvResolution(ctx, f)
	f.From, f.To = clampWindow(f.From, f.To)
	query := s.buildQuery(f)
	idx := s.queryIndices(ctx, f)
	timeout := esTimeseriesTimeoutFromEnv("10s")
	if f.SoftTimeout > 0 {
		timeout = esSoftTimeout(f.SoftTimeout)
	}
	body, err := json.Marshal(buildFieldStatsMultiBody(query, aggFields, limit, timeout))
	if err != nil {
		return nil, err
	}
	tru := true
	req := esapi.SearchRequest{
		Index:             idx,
		Body:              bytes.NewReader(body),
		AllowNoIndices:    &tru,
		IgnoreUnavailable: &tru,
		RequestCache:      &tru,
	}
	res, err := req.Do(ctx, s.cli)
	if err != nil {
		return nil, s.recordQueryError("fieldstats-multi", idx, body, 0, fmt.Errorf("ES fieldstats-multi: %w", err))
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, s.recordQueryError("fieldstats-multi", idx, body, res.StatusCode,
			parseESError("fieldstats-multi", res, s.cfg.Index))
	}
	var decoded esFieldStatsMultiResponse
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if d := decoded.describe(); d != "" {
		log.Printf("[logstore-es] PARTIAL fieldstats-multi (%s) — top values are a subset (fields=%v)", d, fields)
	}
	return decodeFieldStatsMulti(decoded, fields), nil
}
