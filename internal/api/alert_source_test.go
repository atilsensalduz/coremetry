package api

// v0.10.1489 — Alerts önizlemesi / taban önerisi kaynak-duyarlı: cevap
// kaynağı söyler, önbellek anahtarı kaynak girdilerini taşır, denetim satırı
// yalnız alarm kaynağı değişince, rotalar defterde.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestAlertSourceAuditDetail(t *testing.T) {
	prev := chstore.DefaultAnomalySensitivity()
	if _, ok := alertSourceAuditDetail(prev, prev); ok {
		t.Fatal("değişiklik yokken denetim satırı olmamalı")
	}
	next := prev
	next.AlertSource = chstore.AnomalyREDSourceSpan
	d, ok := alertSourceAuditDetail(prev, next)
	if !ok || d != `{"from":{"alertSource":"auto"},"to":{"alertSource":"span"}}` {
		t.Fatalf("geri alma denetimde görünmeli: %v %s", ok, d)
	}
	// Eski blob (alan yok) = auto: değişiklik değil.
	if _, ok := alertSourceAuditDetail(chstore.AnomalySensitivityConfig{}, prev); ok {
		t.Fatal("boş alan = auto")
	}
	// Anomali kaynağı değişikliği alarm denetim satırı ÜRETMEZ (ayrı vidalar).
	red := prev
	red.REDSource = chstore.AnomalyREDSourceSpan
	if _, ok := alertSourceAuditDetail(prev, red); ok {
		t.Fatal("redSource değişikliği alertSource satırı üretmemeli")
	}
}

func TestAlertSourceKeyPartHashesInputs(t *testing.T) {
	base := chstore.DefaultAnomalySensitivity()
	span := base
	span.AlertSource = "span"
	env := base
	env.REDSourceEnv = "prod-eu-a"
	keys := map[string]bool{}
	for _, k := range []string{
		alertSourceKeyPart(base, true), alertSourceKeyPart(span, true), alertSourceKeyPart(env, true), alertSourceKeyPart(base, false),
	} {
		if keys[k] {
			t.Fatalf("anahtar çakıştı: %s", k)
		}
		keys[k] = true
	}
}

func TestAlertSourceRoutesRegistered(t *testing.T) {
	if _, ok := extraRouteRegistrars["alert-source"]; !ok {
		t.Fatal("alert-source rotaları deftere kaydolmamış")
	}
}

func TestAlertPreviewSeriesStatesSource(t *testing.T) {
	orig := alertRuleSeriesStoreOf
	origRd := alertVMReaderPtr.Load()
	t.Cleanup(func() { alertRuleSeriesStoreOf = orig; alertVMReaderPtr.Store(origRd) })
	v := 4.2
	st := &fakeAlertRuleSeriesStore{pts: []chstore.AlertMetricPoint{{T: 1, V: &v}}}
	alertRuleSeriesStoreOf = func(*Server) alertRuleSeriesStore { return st }
	srv := &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	// VM'siz okuyucu (yapılandırılmamış) → span + gerekçe.
	rd := anomaly.NewAlertVMReader(chstore.DefaultAnomalySensitivity)
	alertVMReaderPtr.Store(rd)
	call := func(q string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.getAlertPreviewSeries(w, httptest.NewRequest("GET", "/api/alert-rules/preview-series?"+q, nil))
		return w
	}
	if w := call("metric=error_rate&windowSec=300"); w.Code != 400 || st.calls != 0 {
		t.Fatalf("servis zorunlu: %d calls=%d", w.Code, st.calls)
	}
	w := call("service=orders-api&metric=error_rate&windowSec=600&from=" + itoa64(time.Now().Add(-time.Hour).UnixNano()))
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var body alertRuleSeriesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if st.gotQuery.WindowSec != 600 || st.gotQuery.Service != "orders-api" || body.WindowSec != 600 {
		t.Fatalf("taslağın penceresi: %+v / %+v", st.gotQuery, body)
	}
	if body.Source == nil || body.Source.VM || !strings.Contains(body.Source.Evidence, "kaynak: span (örneklenmiş) — VM kullanılmadı: VictoriaMetrics yapılandırılmamış") {
		t.Fatalf("kaynak: %+v", body.Source)
	}
}

func TestAlertRuleSeriesSyntheticStaysSpan(t *testing.T) {
	orig := alertRuleSeriesStoreOf
	t.Cleanup(func() { alertRuleSeriesStoreOf = orig })
	st := &fakeAlertRuleSeriesStore{}
	alertRuleSeriesStoreOf = func(*Server) alertRuleSeriesStore { return st }
	srv := &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	id := chstore.RuleSvcSlowdownPrefix + "orders-api"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/alert-rules/"+id+"/series?service=orders-api&metric=p99_ms&from="+itoa64(time.Now().Add(-time.Hour).UnixNano()), nil)
	r.SetPathValue("id", id)
	srv.getAlertRuleSeries(w, r)
	var body alertRuleSeriesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Source == nil || body.Source.VM || body.Source.Reason != alertSourceSpanOnlyReason || st.calls != 1 {
		t.Fatalf("yaygın yavaşlama dizisi span kalmalı: %+v calls=%d", body.Source, st.calls)
	}
}
