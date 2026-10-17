package mcptools

// v0.10.1284 — AI SRE Faz 1a: explain_log_spike (log artışını ne açıklıyor).
// Koşucu: sahte log deposuyla uçtan uca (tespit → boyutlar → kanıt → blok),
// kısmi sonuç (boyut zaman aşımı → partial, önbelleğe girmez), zarf sırası ve
// sohbet bloklarının kanıt bağı.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// spikeFakeStore — CH benzeri (toplu alan okuması YOK) sahte depo.
type spikeFakeStore struct {
	logstore.Store
	from, to   time.Time
	spikeFrom  time.Time
	slowField  string
	mu         sync.Mutex
	fieldCalls int
}

func (s *spikeFakeStore) Backend() string { return "clickhouse" }

func (s *spikeFakeStore) FieldMapping(context.Context, bool) logstore.FieldMapping {
	return logstore.FieldMapping{Backend: "clickhouse", Roles: map[string]logstore.FieldResolution{
		logstore.RolePod:     {Field: "k8s.pod.name", Source: logstore.FieldSchema},
		logstore.RoleCluster: {Source: logstore.FieldNone},
	}}
}

// Histogram: istenen kova genişliğinde (gerçek arka uçlar gibi epoch hizalı);
// dakikada 100 INFO, dakika 50-52'de 400 INFO + 200 ERROR.
func (s *spikeFakeStore) Histogram(_ context.Context, f logstore.Filter, bucketSec int, _ string) ([]logstore.LogSeries, error) {
	info := logstore.LogSeries{Name: "INFO"}
	errS := logstore.LogSeries{Name: "ERROR"}
	step := time.Duration(bucketSec) * time.Second
	for t := floorTime(f.From, step); t.Before(f.To); t = t.Add(step) {
		var v, e int64
		for sec := t; sec.Before(t.Add(step)); sec = sec.Add(time.Second) {
			m := int(sec.Sub(s.from) / time.Minute)
			if m >= 50 && m <= 52 {
				v += 400
				e += 200
			} else {
				v += 100
			}
		}
		v, e = v/60, e/60
		info.Points = append(info.Points, logstore.LogPoint{T: t.UnixNano(), V: v})
		if e > 0 {
			errS.Points = append(errS.Points, logstore.LogPoint{T: t.UnixNano(), V: e})
		}
	}
	return []logstore.LogSeries{info, errS}, nil
}

func (s *spikeFakeStore) FieldStats(ctx context.Context, f logstore.Filter, field string, _ int) (*logstore.FieldStatsResult, error) {
	s.mu.Lock()
	s.fieldCalls++
	s.mu.Unlock()
	if field == s.slowField {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	spike := f.From.Equal(s.spikeFrom)
	switch field {
	case "k8s.pod.name":
		if spike {
			return fsr(1800, "pod-a", 1500, "pod-b", 300), nil
		}
		return fsr(1500, "pod-a", 500, "pod-b", 1000), nil
	}
	return &logstore.FieldStatsResult{Field: field, Values: []logstore.FieldValueCount{}}, nil
}

func (s *spikeFakeStore) Search(_ context.Context, f logstore.Filter) (*logstore.Page, error) {
	p := &logstore.Page{}
	for i := 0; i < 8; i++ {
		p.Logs = append(p.Logs, &logstore.LogRecord{Timestamp: s.spikeFrom.Add(time.Duration(i) * time.Second).UnixNano(),
			Severity: 17, SeverityText: "ERROR", ServiceName: "checkout", TraceID: "0123456789abcdef0123456789abcdef",
			Body: fmt.Sprintf("connection reset by peer 10.0.0.%d", i), ResourceAttributes: map[string]string{"k8s.pod.name": "pod-a"}})
	}
	return p, nil
}

func spikeFixture() (*spikeFakeStore, LogSpikeRequest) {
	to := floorTime(time.Now().UTC(), time.Minute)
	from := to.Add(-time.Hour)
	st := &spikeFakeStore{from: from, to: to, spikeFrom: from.Add(50 * time.Minute)}
	return st, LogSpikeRequest{Service: "checkout", From: from, To: to}
}

func TestRunLogSpikeDetectsAndRanks(t *testing.T) {
	st, req := spikeFixture()
	res, err := RunLogSpike(context.Background(), Deps{LogStore: st}, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Partial || res.Error != nil || res.Spike == nil {
		t.Fatalf("sonuç: ok=%v partial=%v err=%+v msg=%q", res.OK, res.Partial, res.Error, res.Message)
	}
	sp := res.Spike
	if sp.Source != spikeSourceDetected || sp.FromMs != st.spikeFrom.UnixMilli() || sp.ToMs != st.spikeFrom.Add(3*time.Minute).UnixMilli() {
		t.Fatalf("tepe penceresi: %+v", sp)
	}
	// Taban = clamp(3·3 dk, 15 dk) = 15 dk; hız 600 vs 100/dk → ×6.
	if sp.BaseFromMs != st.spikeFrom.Add(-15*time.Minute).UnixMilli() || sp.Factor != 6 || sp.SpikeCount != 1800 || sp.BaseCount != 1500 {
		t.Fatalf("taban/oran: %+v", sp)
	}
	if len(res.Contributors) == 0 || res.Contributors[0].Dim != spikeDimPod || res.Contributors[0].Value != "pod-a" {
		t.Fatalf("ilk katkı pod-a olmalı: %+v", res.Contributors)
	}
	foundSev := false
	for _, c := range res.Contributors {
		if c.Dim == spikeDimSeverity && c.Value == "ERROR" && c.New {
			foundSev = true
		}
	}
	if !foundSev {
		t.Fatalf("ERROR bandı yeni katkı olmalı: %+v", res.Contributors)
	}
	states := map[string]string{}
	for _, d := range res.Dims {
		states[d.Dim] = d.State
	}
	want := map[string]string{spikeDimSeverity: hopOK, spikeDimPod: hopOK, spikeDimCluster: hopUnmapped,
		spikeDimLogger: hopEmpty, spikeDimErrorType: hopEmpty, spikeDimPattern: hopOK}
	for k, v := range want {
		if states[k] != v {
			t.Errorf("dim %s state %q, want %q (%v)", k, states[k], v, res.Dims)
		}
	}
	if _, ok := states[spikeDimService]; ok {
		t.Error("servis verilince service boyutu okunmamalı")
	}
	// CH (toplu okuma yok): aday listesi kısaltılır → pencere başına 3 alan.
	if st.fieldCalls != 6 {
		t.Errorf("CH alan okuması %d, want 6 (3 alan × 2 pencere)", st.fieldCalls)
	}
	hops := []string{}
	for _, e := range res.Evidences {
		hops = append(hops, e.Hop)
	}
	if strings.Join(hops, ",") != "histogram,fields,patterns,samples" || res.Evidence == nil {
		t.Fatalf("kanıt halkaları: %v", hops)
	}
	if len(res.Data.Samples) == 0 || len(res.Data.Histogram) != 60 || len(res.Data.Blocks) != 3 {
		t.Fatalf("veri: samples=%d hist=%d blocks=%d", len(res.Data.Samples), len(res.Data.Histogram), len(res.Data.Blocks))
	}
}

func TestRunLogSpikeFlatIsEmpty(t *testing.T) {
	st, req := spikeFixture()
	req.Level = "error"
	req.Query = ""
	// level=error: ERROR bandı yalnız 3 kovada var → medyan 0, tepe 200 → yine tepe.
	res, _ := RunLogSpike(context.Background(), Deps{LogStore: st}, req)
	if !res.OK || res.Spike == nil {
		t.Fatalf("ERROR bandında tepe: %+v", res)
	}
	for _, d := range res.Dims {
		if d.Dim == spikeDimSeverity && d.State != hopSkipped {
			t.Fatalf("level=error'da seviye boyutu skipped: %+v", d)
		}
	}
	// Verilen aralık + zayıf oran → uyarı, kıyas yine yapılır.
	req2 := LogSpikeRequest{Service: "checkout", SpikeFrom: st.from.Add(10 * time.Minute), SpikeTo: st.from.Add(20 * time.Minute)}
	res2, _ := RunLogSpike(context.Background(), Deps{LogStore: st}, req2)
	if !res2.OK || res2.Spike == nil || res2.Spike.Source != spikeSourceGiven {
		t.Fatalf("verilen aralık: %+v", res2)
	}
	if !strings.Contains(strings.Join(res2.Warnings, "|"), "artış doğrulanamadı") {
		t.Fatalf("düz aralıkta uyarı bekleniyordu: %v", res2.Warnings)
	}
}

func TestRunLogSpikePartial(t *testing.T) {
	st, req := spikeFixture()
	st.slowField = "log.logger"
	stored := map[string]bool{}
	memo := func(ctx context.Context, key string, _ time.Duration, fn func(context.Context) (any, bool, error)) ([]byte, error) {
		v, ok, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		stored[key] = ok
		return json.Marshal(v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := RunLogSpike(ctx, Deps{LogStore: st, Memo: memo}, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || !res.Partial {
		t.Fatalf("boyut zaman aşımı → ok + partial: ok=%v partial=%v", res.OK, res.Partial)
	}
	var logger LogSpikeDim
	for _, d := range res.Dims {
		if d.Dim == spikeDimLogger {
			logger = d
		}
	}
	if logger.State != hopTimeout {
		t.Fatalf("logger boyutu timeout olmalı: %+v", logger)
	}
	if len(res.Contributors) == 0 || res.Contributors[0].Value != "pod-a" {
		t.Fatalf("okunan boyutlar yine sıralanır: %+v", res.Contributors)
	}
	if len(stored) != 1 {
		t.Fatalf("memo bir kez çağrılmalı: %v", stored)
	}
	for k, ok := range stored {
		if ok || !strings.HasPrefix(k, "logspike:v1:") {
			t.Fatalf("kısmi sonuç saklanmamalı (key=%s ok=%v)", k, ok)
		}
	}
}

func TestNormalizeLogSpikeRequest(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if _, _, e := normalizeLogSpikeRequest(ctx, LogSpikeRequest{SpikeFrom: now.Add(-time.Hour)}, now); e == nil || e.Code != cosreErrBadArgs {
		t.Fatal("tek uç bad_args olmalı")
	}
	if _, _, e := normalizeLogSpikeRequest(ctx, LogSpikeRequest{SpikeFrom: now.Add(time.Minute), SpikeTo: now.Add(time.Hour)}, now); e == nil {
		t.Fatal("gelecekteki aralık bad_args olmalı")
	}
	r, w, e := normalizeLogSpikeRequest(ctx, LogSpikeRequest{SpikeFrom: now.Add(-9 * time.Hour), SpikeTo: now.Add(-time.Hour + 7*time.Second), Level: "hata"}, now)
	if e != nil || r.SpikeTo.Sub(r.SpikeFrom) > 6*time.Hour+30*time.Second || r.Level != logSpikeLevelError || len(w) == 0 {
		t.Fatalf("6 sa kırpma + seviye eşanlamı: %+v %v %v", r, w, e)
	}
	if r.SpikeFrom.Unix()%30 != 0 || r.SpikeTo.Unix()%30 != 0 {
		t.Fatalf("30 sn ızgarası: %v %v", r.SpikeFrom, r.SpikeTo)
	}
	r, _, _ = normalizeLogSpikeRequest(ctx, LogSpikeRequest{From: now.Add(-5 * time.Minute), To: now.Add(-17 * time.Second)}, now)
	if r.To.Sub(r.From) != 15*time.Minute || r.To.Unix()%30 != 0 || r.Window != "custom" {
		t.Fatalf("otomatik pencere en az 15 dk + ızgara: %+v", r)
	}
	if _, _, e := parseLogSpikeRequest(json.RawMessage(`{"spike_start_iso":"dün"}`)); e == nil {
		t.Fatal("RFC3339 olmayan zaman bad_args olmalı")
	}
}

func TestLogSpikeFieldOrder(t *testing.T) {
	st, req := spikeFixture()
	res, _ := RunLogSpike(context.Background(), Deps{LogStore: st}, req)
	b, _ := json.Marshal(res)
	s := string(b)
	order := []string{`"tool"`, `"ok"`, `"partial"`, `"warnings"`, `"request"`, `"spike"`, `"contributors"`, `"dims"`, `"evidence"`, `"evidences"`, `"data"`}
	last := -1
	for _, k := range order {
		i := strings.Index(s, k+":")
		if i < 0 {
			if k == `"warnings"` {
				continue
			}
			t.Fatalf("%s alanı yok", k)
		}
		if i < last {
			t.Fatalf("%s sırası bozuk", k)
		}
		last = i
	}
	prefix := s[:strings.Index(s, `"data":`)]
	if n := utf8.RuneCountInString(prefix); n >= 6000 {
		t.Fatalf("veri öncesi zarf %d rune — sohbet kesmesi (6000) durumu yer", n)
	}
}

func TestToolResultBlocksLogSpike(t *testing.T) {
	st, req := spikeFixture()
	res, _ := RunLogSpike(context.Background(), Deps{LogStore: st}, req)
	b, _ := json.Marshal(res)
	content := string(b)
	if !IsEvidenceTool(ExplainLogSpikeName) || !IsCompositeEvidenceTool(ExplainLogSpikeName) {
		t.Fatal("explain_log_spike bileşik kanıt aracı olmalı")
	}
	evs, ok := ToolResultEvidenceList(ExplainLogSpikeName, content)
	if !ok || len(evs) != 4 {
		t.Fatalf("kanıt listesi: %v %d", ok, len(evs))
	}
	refs := map[string]string{}
	for i := range evs {
		evs[i].Ref = fmt.Sprintf("ev%d", i)
		refs[evs[i].Hop] = evs[i].Ref
		if evs[i].Href == "" {
			t.Errorf("%s halkasının /logs bağlantısı yok", evs[i].Hop)
		}
	}
	bl := ToolResultBlocks(ExplainLogSpikeName, content, evs)
	if len(bl) != 3 || bl[0].Type != blocks.TypeChart || bl[1].Type != blocks.TypeTable || bl[2].Type != blocks.TypeTable {
		t.Fatalf("bloklar: %+v", bl)
	}
	chart := bl[0].Payload.(map[string]any)
	if chart["evidenceRef"] != refs[logSpikeHopHistogram] {
		t.Fatalf("grafik histogram çipine bağlı olmalı: %v", chart["evidenceRef"])
	}
	regions, _ := chart["regions"].([]any)
	if len(regions) != 2 {
		t.Fatalf("taban + artış bölgesi: %v", chart["regions"])
	}
	if bl[1].Payload.(map[string]any)["evidenceRef"] != refs[logSpikeHopFields] {
		t.Fatal("katkı tablosu alan çipine bağlı olmalı")
	}
	if bl[2].Payload.(map[string]any)["evidenceRef"] != refs[logSpikeHopSamples] {
		t.Fatal("örnek tablosu örnek çipine bağlı olmalı")
	}
}
