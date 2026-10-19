package anomaly

// exception_chain_test.go — v0.10.1379 — operator context: the exception
// explain lacked the downstream HTTP 500 that the trace explain (v0.10.1371)
// now shows.
//
// Exceptions sayfasındaki "Ask CoSRE" girdisinin kompakt span JSON'u (liteSpan)
// HTTP kodu / url.full / server.address / peer taşımıyordu: bağımlı çağrıdan
// HTTP 500 alıp ardından NPE atan servisin açıklaması bağımlılığı
// adlandıramıyordu. Karar: span JSON'u DEĞİŞMEZ
// (TestSampleTraceEvidenceByteIdenticalToLegacy onu eski koda karşı bayt bayt
// pinliyor); hatalı örnek trace için trace explain'le AYNI deterministik HATA
// ZİNCİRİ bloğu (internal/tracechain) girdiye eklenir. Pinler:
//   - span grubu: blok meta/trend'in ardında, stack ve trace JSON'undan ÖNCE;
//     kaynak halka "servis · CLIENT · POST host → yol · HTTP 500 · süre";
//     kapanış satırı halkanın exception event'i, yoksa grubun kendisi;
//     kaynak 60'lık kompakt kesimin DIŞINDA kalsa da adlandırılır.
//   - Oracle grubu (CH ve Tempo-önce yüklenen trace): blok Coremetry TRACE
//     bloğunun hemen önünde, kapanış "kod: operasyon (Oracle hata tablosu…)",
//     EvSpans kaynakla başlar.
//   - YOKLUK: hatasız trace / yüklenmeyen trace → blok yok, User bayt bayt eski.
// Hostlar example.test, servisler sentetik.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

const (
	ecRootID   = "e100000000000001"
	ecMapID    = "e100000000000002"
	ecClientID = "e100000000000003"
	ecNPEType  = "java.lang.NullPointerException"
	ecNPEMsg   = `Cannot invoke "String.trim()" because "resp.getName()" is null`
)

// ecSpan — teBase'e göre msec ofsetli span.
func ecSpan(id, parent, svc, name, kind string, startMs, durMs int64, errStatus bool) chstore.SpanRow {
	sp := chstore.SpanRow{TraceID: teTrace, SpanID: id, ParentSpanID: parent, ServiceName: svc, Name: name, Kind: kind,
		StartTime: teBase + startMs*int64(time.Millisecond), EndTime: teBase + (startMs+durMs)*int64(time.Millisecond)}
	if errStatus {
		sp.StatusCode = "error"
	}
	return sp
}

// ecDownstream500 — orders-api-prod SERVER (hata) → INTERNAL mapper (hata) →
// CLIENT POST osb.example.test HTTP 500 (kolon değil attribute: Tempo yolu).
func ecDownstream500() []chstore.SpanRow {
	root := ecSpan(ecRootID, "", "orders-api-prod", "POST /orders/submit", "server", 0, 120, true)
	mp := ecSpan(ecMapID, ecRootID, "orders-api-prod", "IdentityMapper.map", "internal", 5, 100, true)
	cl := ecSpan(ecClientID, ecMapID, "orders-api-prod", "POST", "client", 10, 50, true)
	cl.Attributes = map[string]string{"url.full": "https://osb.example.test/esb/IdentityQuery?wsdl", "server.address": "osb.example.test",
		"http.request.method": "POST", "http.response.status_code": "500"}
	return []chstore.SpanRow{root, mp, cl}
}

func ecGroup() *chstore.ExceptionGroup {
	return &chstore.ExceptionGroup{Type: ecNPEType, Message: ecNPEMsg, Service: "orders-api-prod",
		FirstSeen: teBase, LastSeen: teBase + int64(time.Hour)}
}

const ecHop1 = "1. [hata kaynağı] orders-api-prod · CLIENT · POST osb.example.test → /esb/IdentityQuery · HTTP 500 · 50 ms"

func TestExceptionChainSpanGroup(t *testing.T) {
	g := ecGroup()
	stack := ecNPEType + ": " + ecNPEMsg + "\n\tat com.example.orders.IdentityMapper.map(IdentityMapper.java:760)"
	ld := &fakeSpanLoader{spans: map[string][]chstore.SpanRow{teTrace: ecDownstream500()}}
	st := sampleTraceEvidence(context.Background(), ld, nil, teTrace, []string{stack})

	// Span JSON'u bayt bayt eski (HTTP span'i olsa da) — tasarım kararı.
	old := legacySampleTraceEvidence(context.Background(), &fakeSpanLoader{spans: ld.spans}, nil, teTrace, []string{stack})
	if st.TraceBlock != old.traceBlock || st.TraceBlock == "" {
		t.Fatalf("kompakt span bloğu eski şekilde kalmalı:\nyeni %q\neski %q", st.TraceBlock, old.traceBlock)
	}

	chain, origin := exceptionChainBlock(st.Trace.Spans, spanGroupChainException(g))
	if origin != ecClientID || !strings.HasPrefix(chain, "\n\n"+tracechain.Header+"\n") {
		t.Fatalf("zincir: origin=%q\n%s", origin, chain)
	}
	lines := strings.Split(strings.TrimPrefix(chain, "\n\n"), "\n")
	if len(lines) != 5 || lines[1] != ecHop1 {
		t.Fatalf("1. halka\n got: %q\nwant: %q\n%s", lines[1], ecHop1, chain)
	}
	if !strings.HasPrefix(lines[3], "3. [kök] orders-api-prod · SERVER · POST /orders/submit · status error · 120 ms") {
		t.Errorf("kök halka: %s", lines[3])
	}
	wantEx := "Zincirin ardından kaydedilen exception: orders-api-prod · " + ecNPEType + ": " + ecNPEMsg + " (exception grubu)"
	if lines[4] != wantEx {
		t.Errorf("kapanış satırı\n got: %s\nwant: %s", lines[4], wantEx)
	}

	user := assembleExceptionPromptChain(g, time.UTC, "toplam=3", chain, st.StackForPrompt, st.TraceBlock, st.LogsBlock, "", "")
	iTrend, iCh, iStack, iTrace := strings.Index(user, "Occurrence trendi"), strings.Index(user, tracechain.Header),
		strings.Index(user, "Temsilî STACKTRACE"), strings.Index(user, "Örnek hata TRACE'i")
	if !(iTrend >= 0 && iTrend < iCh && iCh < iStack && iStack < iTrace) {
		t.Fatalf("blok sırası: trend=%d zincir=%d stack=%d trace=%d", iTrend, iCh, iStack, iTrace)
	}
	if strings.Count(user, "```") != strings.Count(assembleExceptionPrompt(g, time.UTC, "toplam=3", st.StackForPrompt, st.TraceBlock, st.LogsBlock, "", ""), "```") {
		t.Error("zincir bloğu çitsiz olmalı")
	}

	// Halkanın kendi exception event'i grubun yedeğini yener.
	spans := ecDownstream500()
	spans[0].Events = []any{map[string]any{"name": "exception", "attributes": map[string]any{"exception.type": ecNPEType, "exception.message": ecNPEMsg}}}
	ev, _ := exceptionChainBlock(spans, spanGroupChainException(g))
	if !strings.HasSuffix(ev, "orders-api-prod · "+ecNPEType+": "+ecNPEMsg+" (span event)") {
		t.Errorf("span event kapanışı:\n%s", ev)
	}
}

// Kaynak 60'lık kompakt kesimin dışında: zincir yine TÜM span'lerden.
func TestExceptionChainOriginBeyondCompactCut(t *testing.T) {
	spans := teSpans(90, 3) // 30 hata span'i; kompakt yalnız ilk 20'yi garanti eder
	r := compactTraceEvidence(spans)
	chain, origin := exceptionChainBlock(r.Spans, tracechain.Exception{})
	deepest := spans[89].SpanID
	if origin != deepest || !strings.Contains(chain, "op-89") {
		t.Fatalf("kaynak en derin hata (%s) olmalı: %q\n%s", deepest, origin, chain)
	}
	for _, l := range r.Compact {
		if l.SpanID == deepest {
			t.Fatal("kurgu: en derin hata kompakt kesimin dışında olmalı")
		}
	}
	if n := len([]rune(chain)); n > tracechain.BlockMaxRunes+3 {
		t.Errorf("blok tavanı: %d", n)
	}
}

func TestExceptionChainAbsent(t *testing.T) {
	g := ecGroup()
	healthy := ecDownstream500()
	for i := range healthy {
		healthy[i].StatusCode = "ok"
	}
	for _, tc := range []struct {
		name string
		ld   *fakeSpanLoader
	}{
		{"hatasız trace", &fakeSpanLoader{spans: map[string][]chstore.SpanRow{teTrace: healthy}}},
		{"trace yüklenmedi", &fakeSpanLoader{err: errors.New("ch down")}},
		{"trace boş", &fakeSpanLoader{spans: map[string][]chstore.SpanRow{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := sampleTraceEvidence(context.Background(), tc.ld, nil, teTrace, nil)
			chain, origin := exceptionChainBlock(st.Trace.Spans, spanGroupChainException(g))
			if chain != "" || origin != "" {
				t.Fatalf("zincir olmamalı: %q %q", chain, origin)
			}
			a := assembleExceptionPromptChain(g, time.UTC, "t", chain, "s", st.TraceBlock, "", "", "")
			if b := assembleExceptionPrompt(g, time.UTC, "t", "s", st.TraceBlock, "", "", ""); a != b || strings.Contains(a, "HATA ZİNCİRİ") {
				t.Fatal("zincirsiz prompt bayt bayt eski olmalı")
			}
			if got := tracechain.PrependUnique(origin, st.Trace.EvSpans); fmt.Sprint(got) != fmt.Sprint(st.Trace.EvSpans) {
				t.Fatalf("EvSpans değişmemeli: %v", got)
			}
		})
	}
}

func TestExceptionChainOracleGroup(t *testing.T) {
	t.Cleanup(func() { oracleFactsFn.Store(nil) })
	SetOracleExplainFacts(func(chstore.ExceptionGroup) (OracleExplainFacts, bool) { return oraFacts(), true })
	id := func(n int) string { return fmt.Sprintf("%032x", n) }
	const wantClose = "Zincirin ardından kaydedilen exception: APP_ERR_042: OP_TRANSFER (Oracle hata tablosu, kaynak core-errlog)"
	healthy := ecDownstream500()
	for i := range healthy {
		healthy[i].StatusCode = ""
	}

	check := func(t *testing.T, in ExceptionExplainInput, want bool) {
		t.Helper()
		has := strings.Contains(in.User, tracechain.Header)
		if has != want {
			t.Fatalf("zincir bloğu var=%v, beklenen %v:\n%s", has, want, in.User)
		}
		if !want {
			return
		}
		if !strings.Contains(in.User, ecHop1) || !strings.Contains(in.User, wantClose) {
			t.Fatalf("halka / kapanış satırı:\n%s", in.User)
		}
		iRes, iCh, iTr := strings.Index(in.User, "Çözülen trace'ler"), strings.Index(in.User, tracechain.Header), strings.Index(in.User, "Coremetry TRACE'i (")
		if !(iRes < iCh && iCh < iTr) || !strings.Contains(in.User, wantClose+"\n\nCoremetry TRACE'i (") {
			t.Fatalf("blok sırası: çözülen=%d zincir=%d trace=%d", iRes, iCh, iTr)
		}
		if len(in.EvSpans) == 0 || in.EvSpans[0] != ecClientID {
			t.Fatalf("EvSpans kaynakla başlamalı: %v", in.EvSpans)
		}
	}

	t.Run("CH'den yüklenen trace", func(t *testing.T) {
		rd := &fakeOracleReader{rows: oraRows(6, 3), found: []string{id(1)}, spans: map[string][]chstore.SpanRow{id(1): ecDownstream500()}}
		check(t, BuildOracleExceptionExplainInput(context.Background(), rd, nil, oraGroup(), time.UTC), true)
	})
	t.Run("Tempo-önce yüklenen trace", func(t *testing.T) {
		rd := &fakeProbeReader{fakeOracleReader: &fakeOracleReader{rows: oraRows(6, 3), found: []string{}},
			probeSpans: map[string][]chstore.SpanRow{id(1): ecDownstream500()}}
		in := BuildOracleExceptionExplainInput(context.Background(), rd, nil, oraGroup(), time.UTC)
		if in.Oracle.CoremetryTrace != id(1) {
			t.Fatalf("Tempo-önce deneme yüklemeliydi: %q", in.Oracle.CoremetryTrace)
		}
		check(t, in, true)
	})
	t.Run("hatasız trace → blok yok", func(t *testing.T) {
		rd := &fakeOracleReader{rows: oraRows(6, 3), found: []string{id(1)}, spans: map[string][]chstore.SpanRow{id(1): healthy}}
		in := BuildOracleExceptionExplainInput(context.Background(), rd, nil, oraGroup(), time.UTC)
		check(t, in, false)
		if !strings.Contains(in.User, "\n\nCoremetry TRACE'i (") || len(in.EvSpans) != 0 {
			t.Fatalf("hatasız trace bloğu eski şekliyle basılmalı: ev=%v", in.EvSpans)
		}
	})
	t.Run("trace yüklenmedi → blok yok", func(t *testing.T) {
		rd := &fakeOracleReader{rows: oraRows(6, 3), found: []string{id(1)}, getTraceErr: errors.New("ch down")}
		check(t, BuildOracleExceptionExplainInput(context.Background(), rd, nil, oraGroup(), time.UTC), false)
	})
}
