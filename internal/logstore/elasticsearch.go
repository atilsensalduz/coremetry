package logstore

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// ESConfig is the operator-supplied connection + field-mapping spec for
// an external Elasticsearch cluster. Field paths default to OTel-spec
// names — most ECS / OTel-shipped indices already use these.
type ESConfig struct {
	Addresses []string
	Username  string
	Password  string
	// APIKey is the base64 "id:api_key" string — the `encoded` field
	// returned by POST /_security/api_key. Takes precedence over basic
	// auth: if both APIKey and Username are set, the underlying client
	// sends an Authorization: ApiKey ... header and ignores user/pass.
	APIKey             string
	InsecureSkipVerify bool

	Index string // e.g. "logs-*" — supports glob/data-stream patterns

	// Field paths inside each ES document. Override per-deployment via
	// config so any shipping pipeline (Filebeat, Logstash, OTel
	// Collector → ES exporter) can be queried without re-indexing.
	Fields ESFieldMap
}

type ESFieldMap struct {
	Timestamp  string // default "@timestamp"
	TraceID    string // default "trace.id"
	SpanID     string // default "span.id"
	Service    string // default "service.name"
	Body       string // default "message"
	SeverityNo string // numeric, default "" (skip if absent)
	SeverityTx string // text, default "log.level"
}

func (c *ESConfig) defaults() {
	if c.Index == "" {
		c.Index = "logs-*"
	}
	if c.Fields.Timestamp == "" {
		c.Fields.Timestamp = "@timestamp"
	}
	if c.Fields.TraceID == "" {
		c.Fields.TraceID = "trace.id"
	}
	if c.Fields.SpanID == "" {
		c.Fields.SpanID = "span.id"
	}
	if c.Fields.Service == "" {
		c.Fields.Service = "service.name"
	}
	if c.Fields.Body == "" {
		c.Fields.Body = "message"
	}
	if c.Fields.SeverityTx == "" {
		c.Fields.SeverityTx = "log.level"
	}
}

// ESStore implements the LogStore Search interface against an external
// Elasticsearch cluster. Read-only — Coremetry never writes to ES.
type ESStore struct {
	cli    *elasticsearch.Client
	cfg    ESConfig
	fields ESFieldMap
}

func NewES(cfg ESConfig) (*ESStore, error) {
	cfg.defaults()

	transport := &http.Transport{}
	if cfg.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	cli, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: cfg.Addresses,
		Username:  cfg.Username,
		Password:  cfg.Password,
		APIKey:    cfg.APIKey,
		Transport: transport,
	})
	if err != nil {
		return nil, fmt.Errorf("create ES client: %w", err)
	}
	// Ping early so a misconfigured ES surfaces at startup rather than
	// at the first user query. 5s budget — ES clusters under load can
	// be slow but not seconds-slow on a no-op endpoint.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Info(cli.Info.WithContext(ctx)); err != nil {
		return nil, fmt.Errorf("ES ping: %w", err)
	}
	return &ESStore{cli: cli, cfg: cfg, fields: cfg.Fields}, nil
}

func (s *ESStore) Backend() string { return "elasticsearch" }

// Ping checks ES cluster availability via the lightweight Info API
// (same endpoint NewES uses at startup). 2-second timeout enforced by
// the caller's context.
func (s *ESStore) Ping(ctx context.Context) error {
	res, err := s.cli.Info(s.cli.Info.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("ES info: %s", res.Status())
	}
	return nil
}

func (s *ESStore) Search(ctx context.Context, f Filter) (*Page, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	from := f.Offset
	if from < 0 {
		from = 0
	}

	query := s.buildQuery(f)
	body, err := json.Marshal(map[string]any{
		"query": query,
		"sort":  []any{map[string]any{s.fields.Timestamp: map[string]string{"order": "desc"}}},
		"from":  from,
		"size":  limit,
		"track_total_hits": true,
	})
	if err != nil {
		return nil, err
	}

	tru := true
	req := esapi.SearchRequest{
		Index: []string{s.cfg.Index},
		Body:  bytes.NewReader(body),
		// Treat "no matching index" / "one shard unavailable" as 0
		// hits instead of 404. Without these, an operator pointing
		// the read backend at a freshly-provisioned ES cluster
		// (no logs shipped yet) sees a 404 in the UI and assumes
		// Coremetry is broken — when really ES just has nothing
		// to search. Kibana applies the same defaults for the
		// Discover view.
		AllowNoIndices:    &tru,
		IgnoreUnavailable: &tru,
	}
	res, err := req.Do(ctx, s.cli)
	if err != nil {
		return nil, fmt.Errorf("ES search: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, parseESError("search", res, s.cfg.Index)
	}

	var raw struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID     string         `json:"_id"`
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode ES response: %w", err)
	}

	out := make([]*LogRecord, 0, len(raw.Hits.Hits))
	for _, h := range raw.Hits.Hits {
		out = append(out, s.mapHit(h.ID, h.Source))
	}
	return &Page{Total: raw.Hits.Total.Value, Logs: out}, nil
}

// Histogram runs a date_histogram aggregation against ES,
// optionally split by a terms aggregation on `groupBy`. The
// fields used for splitting map to the configured ESFieldMap so
// custom shipping pipelines (Vector, Filebeat, OTel ECS mode)
// don't need the operator to re-index. Single round-trip; ES
// handles bucketing server-side and we just stitch the response.
func (s *ESStore) Histogram(ctx context.Context, f Filter, bucketSec int, groupBy string) ([]LogSeries, error) {
	if bucketSec <= 0 {
		bucketSec = 30
	}
	groupField := ""
	switch groupBy {
	case "service":
		groupField = s.fields.Service + ".keyword"
	case "severity":
		groupField = s.fields.SeverityTx + ".keyword"
	}

	dateAgg := map[string]any{
		"date_histogram": map[string]any{
			"field":          s.fields.Timestamp,
			"fixed_interval": fmt.Sprintf("%ds", bucketSec),
			"min_doc_count":  0,
		},
	}
	var aggs map[string]any
	if groupField != "" {
		aggs = map[string]any{
			"groups": map[string]any{
				"terms": map[string]any{
					"field": groupField,
					"size":  20,
				},
				"aggs": map[string]any{"buckets": dateAgg},
			},
		}
	} else {
		aggs = map[string]any{"buckets": dateAgg}
	}

	body, err := json.Marshal(map[string]any{
		"size":  0,
		"query": s.buildQuery(f),
		"aggs":  aggs,
	})
	if err != nil {
		return nil, err
	}
	tru := true
	req := esapi.SearchRequest{
		Index: []string{s.cfg.Index},
		Body:  bytes.NewReader(body),
		// Same forgiveness as Search — no matching index → 0
		// buckets rather than a 404. Keeps the panel rendering
		// "no data" instead of a scary error toast.
		AllowNoIndices:    &tru,
		IgnoreUnavailable: &tru,
	}
	res, err := req.Do(ctx, s.cli)
	if err != nil {
		return nil, fmt.Errorf("ES histogram: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, parseESError("histogram", res, s.cfg.Index)
	}

	var raw struct {
		Aggregations map[string]any `json:"aggregations"`
	}
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode ES histogram: %w", err)
	}

	parseBuckets := func(a any) []LogPoint {
		root, ok := a.(map[string]any)
		if !ok {
			return nil
		}
		bs, ok := root["buckets"].([]any)
		if !ok {
			return nil
		}
		pts := make([]LogPoint, 0, len(bs))
		for _, b := range bs {
			bm, _ := b.(map[string]any)
			tMs, _ := bm["key"].(float64)
			cnt, _ := bm["doc_count"].(float64)
			pts = append(pts, LogPoint{T: int64(tMs) * int64(time.Millisecond), V: int64(cnt)})
		}
		return pts
	}

	if groupField == "" {
		return []LogSeries{{Name: "_total", Points: parseBuckets(raw.Aggregations["buckets"])}}, nil
	}
	groups, _ := raw.Aggregations["groups"].(map[string]any)
	bs, _ := groups["buckets"].([]any)
	out := make([]LogSeries, 0, len(bs))
	for _, b := range bs {
		bm, _ := b.(map[string]any)
		name, _ := bm["key"].(string)
		out = append(out, LogSeries{Name: name, Points: parseBuckets(bm["buckets"])})
	}
	return out, nil
}

// buildQuery constructs the ES bool/must query corresponding to a Filter.
func (s *ESStore) buildQuery(f Filter) map[string]any {
	must := []any{}

	// Time range
	if !f.From.IsZero() || !f.To.IsZero() {
		rng := map[string]any{}
		if !f.From.IsZero() {
			rng["gte"] = f.From.UTC().Format(time.RFC3339Nano)
		}
		if !f.To.IsZero() {
			rng["lte"] = f.To.UTC().Format(time.RFC3339Nano)
		}
		must = append(must, map[string]any{
			"range": map[string]any{s.fields.Timestamp: rng},
		})
	}
	// trace_id / span_id correlation — single-document keyword lookup, the
	// hottest predicate. `term` is the cheapest possible ES op against a
	// keyword field (no analysis, no scoring), and the OTel Collector ES
	// exporter in ECS mode maps both as keyword by default. If an
	// operator's pipeline maps these as `text`, override
	// `Fields.TraceID` to the .keyword multi-field (e.g. "trace.id.keyword").
	if f.TraceID != "" {
		must = append(must, map[string]any{
			"term": map[string]any{s.fields.TraceID: strings.ToLower(f.TraceID)},
		})
	}
	if f.SpanID != "" {
		must = append(must, map[string]any{
			"term": map[string]any{s.fields.SpanID: strings.ToLower(f.SpanID)},
		})
	}
	if f.Service != "" {
		must = append(must, map[string]any{
			"term": map[string]any{s.fields.Service: f.Service},
		})
	}
	if f.Search != "" {
		// Free-text search box → Lucene query_string. Plain words still
		// work as a body match (default_field), but the user can also
		// write structured queries like:
		//
		//     level:error
		//     service.name:java-demo AND NOT message:health
		//     trace.id:c9ea*
		//
		// AllowLeadingWildcard is OFF for performance — leading * means
		// scanning every term in the inverted index, which is multi-second
		// at our scale. AllowLeadingWildcard can be re-enabled via the
		// field map if an operator really needs it.
		must = append(must, map[string]any{
			"query_string": map[string]any{
				"query":                  f.Search,
				"default_field":          s.fields.Body,
				"default_operator":       "AND",
				"allow_leading_wildcard": false,
				"lenient":                true, // tolerate type mismatches (e.g. searching numeric column with a word)
			},
		})
	}
	if f.SeverityMin > 0 && s.fields.SeverityNo != "" {
		must = append(must, map[string]any{
			"range": map[string]any{s.fields.SeverityNo: map[string]any{"gte": f.SeverityMin}},
		})
	}

	if len(must) == 0 {
		return map[string]any{"match_all": map[string]any{}}
	}
	return map[string]any{"bool": map[string]any{"must": must}}
}

// mapHit walks the source document using the configured field paths
// (which can be dotted, e.g. "trace.id") and pulls each value into the
// canonical LogRecord shape. Anything not under a known path falls into
// the Attributes map so it's still inspectable in the UI.
func (s *ESStore) mapHit(id string, src map[string]any) *LogRecord {
	r := &LogRecord{
		Attributes:         map[string]string{},
		ResourceAttributes: map[string]string{},
	}

	if v := readPath(src, s.fields.Timestamp); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			r.Timestamp = t.UnixNano()
		}
	}
	r.TraceID = readPath(src, s.fields.TraceID)
	r.SpanID  = readPath(src, s.fields.SpanID)
	r.ServiceName = readPath(src, s.fields.Service)
	r.Body = readPath(src, s.fields.Body)
	r.SeverityText = readPath(src, s.fields.SeverityTx)

	if s.fields.SeverityNo != "" {
		if v := readPath(src, s.fields.SeverityNo); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 24 {
				r.Severity = uint8(n)
			}
		}
	}
	// ES doc IDs are strings; convert to a stable hash-ish int64 so the
	// frontend's existing numeric ID assumption still holds. Lossy but
	// the ID is only used for React keys, never as a lookup.
	r.ID = stringToInt64ID(id)

	// Stash everything else into Attributes so the user can still see
	// host name, custom fields, etc. Skip the canonical fields we
	// already extracted to avoid duplication.
	skip := map[string]bool{
		s.fields.Timestamp: true, s.fields.TraceID: true, s.fields.SpanID: true,
		s.fields.Service: true, s.fields.Body: true,
		s.fields.SeverityTx: true, s.fields.SeverityNo: true,
	}
	flatten("", src, r.Attributes, skip)
	return r
}

// readPath fetches a value from a map by dotted path, returning the
// stringified value or "" if missing. Handles nested maps for ECS-style
// docs ({trace: {id: "..."}}).
func readPath(src map[string]any, path string) string {
	if path == "" {
		return ""
	}
	parts := strings.Split(path, ".")
	var cur any = src
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[p]
		if !ok {
			return ""
		}
	}
	return toString(cur)
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		// JSON numbers parse as float64; strip the .0 for integers.
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// flatten walks a nested map and writes "a.b.c=value" pairs into dst.
// Skip-set values are pre-extracted into the canonical LogRecord fields.
func flatten(prefix string, src map[string]any, dst map[string]string, skip map[string]bool) {
	for k, v := range src {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		if skip[full] {
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			flatten(full, nested, dst, skip)
			continue
		}
		dst[full] = toString(v)
	}
}

// parseESError pulls a human-readable reason from a non-2xx ES
// response. ES wraps real failures in an envelope like
//
//	{"error":{"type":"index_not_found_exception",
//	          "reason":"no such index [logs-otel-default]"},
//	 "status":404}
//
// res.String() dumps the lot — the type is buried mid-line — so
// we surface the reason cleanly. For 404 specifically we append
// a "check the index pattern in your config" hint because that's
// the canonical operator footgun (typo in the configured index
// name, or pointing at a brand-new ES that hasn't seen logs yet).
//
// Falls back to res.Status() + raw body when the envelope shape
// isn't what we expect (older ES, transport-level errors).
func parseESError(op string, res *esapi.Response, configuredIndex string) error {
	var parsed struct {
		Error struct {
			Type      string `json:"type"`
			Reason    string `json:"reason"`
			IndexUUID string `json:"index_uuid"`
			Index     string `json:"index"`
		} `json:"error"`
		Status int `json:"status"`
	}
	body, _ := io.ReadAll(res.Body)
	if json.Unmarshal(body, &parsed) == nil && parsed.Error.Reason != "" {
		switch {
		case res.StatusCode == 404 && parsed.Error.Type == "index_not_found_exception":
			return fmt.Errorf(
				"ES %s: index %q not found — check `logs.elasticsearch.index` in your config (current value %q). Run `curl <es>/_cat/indices?v=true` against your cluster to see what's available",
				op, parsed.Error.Index, configuredIndex)
		case res.StatusCode == 401 || res.StatusCode == 403:
			return fmt.Errorf(
				"ES %s: %s (status %d) — check API key / username + password and that the credential has read access to %q",
				op, parsed.Error.Reason, res.StatusCode, configuredIndex)
		default:
			return fmt.Errorf("ES %s %d: %s (%s)",
				op, res.StatusCode, parsed.Error.Reason, parsed.Error.Type)
		}
	}
	return fmt.Errorf("ES %s %s: %s", op, res.Status(), string(body))
}

// stringToInt64ID is a 64-bit FNV-1a so React keys stay numeric. Not a
// crypto hash; collisions are fine because the ID is never used for
// lookups, only for stable list-rendering.
func stringToInt64ID(s string) int64 {
	const (
		offset64 uint64 = 14695981039346656037
		prime64  uint64 = 1099511628211
	)
	h := offset64
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return int64(h >> 1)
}
