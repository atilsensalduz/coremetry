package api

// copilot_service_exc_samples_test.go — v0.10.1486: servis exception cevabının
// örnek trace + stack kesiti tablosu (render, dürüst eksik, sınırlar, önbellek,
// demet uçtan uca sahte okuyucuyla).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	excTID1 = "4bf92f3577b34da6a3ce929d0e0e4736"
	excSID1 = "00f067aa0ba902b7"
	excTID2 = "5cf92f3577b34da6a3ce929d0e0e4737"
	excSID2 = "10f067aa0ba902b8"
)

const excAppStack = `java.lang.IllegalStateException: stok yok
	at com.example.orders.OrderService.reserve(OrderService.java:42)
	at com.example.orders.OrderService.place(OrderService.java:30)
	at com.example.orders.OrderController.post(OrderController.java:17)
	at com.example.orders.OrderFilter.doFilter(OrderFilter.java:11)
	at com.example.orders.DeepFrame.never(DeepFrame.java:99)
	at org.springframework.web.servlet.FrameworkServlet.service(FrameworkServlet.java:897)
	at java.lang.Thread.run(Thread.java:833)`

const excLibStack = `java.sql.SQLException: pool exhausted
	at org.jboss.jca.adapters.jdbc.WrapperDataSource.getConnection(WrapperDataSource.java:275)
	at org.springframework.jdbc.datasource.DataSourceUtils.doGetConnection(DataSourceUtils.java:160)
	at java.lang.Thread.run(Thread.java:833)`

// fakeSvcExcReader — serviceExcReader sahtesi.
type fakeSvcExcReader struct {
	mu       sync.Mutex
	rows     []chstore.ExceptionRow
	stacks   map[string]string
	stackErr error
	calls    int
	lastRefs []chstore.ExceptionSpanRef
}

func (f *fakeSvcExcReader) GetExceptions(context.Context, chstore.ExceptionFilter) ([]chstore.ExceptionRow, error) {
	return f.rows, nil
}

func (f *fakeSvcExcReader) ExceptionSampleStacks(_ context.Context, _ string, refs []chstore.ExceptionSpanRef) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastRefs = append([]chstore.ExceptionSpanRef(nil), refs...)
	if f.stackErr != nil {
		return nil, f.stackErr
	}
	return f.stacks, nil
}

func installExcReader(t *testing.T, f *fakeSvcExcReader) {
	t.Helper()
	prev := serviceExcReadFn
	serviceExcReadFn = func(*Server) serviceExcReader { return f }
	t.Cleanup(func() { serviceExcReadFn = prev })
}

// excFixtureRows — iki örnekli grup + örneksiz grup.
func excFixtureRows() []chstore.ExceptionRow {
	ls := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC).UnixNano()
	return []chstore.ExceptionRow{
		{Type: "java.lang.IllegalStateException", Count: 1234, LastSeen: ls, SampleTraceID: excTID1, SampleSpanID: excSID1, Message: "stok yok"},
		{Type: "java.sql.SQLException", Count: 40, LastSeen: ls - 1e9, SampleTraceID: strings.ToUpper(excTID2), SampleSpanID: excSID2},
		{Type: "feign.RetryableException", Count: 3, LastSeen: ls - 2e9}, // örnek yok
	}
}

func excFixtureStacks() map[string]string {
	return map[string]string{
		chstore.ExceptionSampleKey(excTID1, excSID1): excAppStack,
		chstore.ExceptionSampleKey(excTID2, excSID2): excLibStack,
	}
}

func TestRenderServiceExcSamplesTable(t *testing.T) {
	rows := buildExcSampleRows(excFixtureRows(), excFixtureStacks(), nil, nil)
	got := renderServiceExcSamplesTR("orders-api", rows, 7)
	for _, want := range []string{
		"**Örnek trace ve stack** — en çok görülen 3 grup",
		"| # | Exception | Sayı | Örnek trace | Stack kesiti |",
		"| 1 | java.lang.IllegalStateException | 1,234 | [4bf92f3577b3…](/trace?id=" + excTID1 + "&span=" + excSID1 + ") | `com.example.orders.OrderService.reserve (OrderService.java:42)` ← ",
		// büyük harfli trace id küçültülür (link /trace sayfasının biçimi)
		"(/trace?id=" + excTID2 + "&span=" + excSID2 + ")",
		"uygulama çerçevesi yok; fırladığı yer (kütüphane): `org.jboss.jca.adapters.jdbc.WrapperDataSource.getConnection (WrapperDataSource.java:275)`",
		"| 3 | feign.RetryableException | 3 | örnek yok | — |",
		"+4 grup daha; örnekleri: [orders-api · Exceptions](/problems?service=orders-api)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("tabloda %q yok:\n%s", want, got)
		}
	}
	// Sınır: en çok serviceExcStackLines uygulama çerçevesi; tam stack basılmaz.
	if strings.Contains(got, "DeepFrame") || strings.Contains(got, "FrameworkServlet") || strings.Contains(got, "Thread.run") {
		t.Errorf("stack kesiti sınırı aşıldı / kütüphane satırı basıldı:\n%s", got)
	}
	if renderServiceExcSamplesTR("orders-api", nil, 0) != "" {
		t.Error("grup yokken ek boş olmalı")
	}
}

func TestBuildExcSampleRowsHonestStates(t *testing.T) {
	ex := excFixtureRows()
	// Okuma düştü: örnekli satırlar "okunamadı" (yok DEĞİL), linkler durur.
	rows := buildExcSampleRows(ex, nil, errors.New("timeout"), nil)
	out := renderServiceExcSamplesTR("orders-api", rows, len(ex))
	if !strings.Contains(out, "stack okunamadı") || strings.Contains(out, "stack yok (") {
		t.Errorf("okuma hatası dürüst değil:\n%s", out)
	}
	if !strings.Contains(out, "/trace?id="+excTID1) {
		t.Errorf("stack okunamasa da trace linki kalmalı:\n%s", out)
	}
	// Okundu ama örnek span'de stack yok → "stack yok".
	rows = buildExcSampleRows(ex[:1], map[string]string{}, nil, nil)
	if rows[0].Stack != excStackMissing || !strings.Contains(excStackCell(rows[0]), "stack yok") {
		t.Errorf("boş stack: %+v", rows[0])
	}
	// Geçersiz işaretçi (sıfır / hex değil) → örnek yok, link yok.
	bad := []chstore.ExceptionRow{
		{Type: "A", Count: 1, LastSeen: 1, SampleTraceID: strings.Repeat("0", 32), SampleSpanID: excSID1},
		{Type: "B", Count: 1, LastSeen: 1, SampleTraceID: "not-a-trace", SampleSpanID: excSID1},
	}
	rows = buildExcSampleRows(bad, nil, nil, nil)
	for _, r := range rows {
		if r.TraceID != "" || r.Stack != excStackNoSample {
			t.Errorf("geçersiz işaretçi örnek sayıldı: %+v", r)
		}
	}
	if len(excSampleRefs(bad)) != 0 || len(excSampleLinks(rows)) != 0 {
		t.Error("geçersiz işaretçiyle okuma/çip olmamalı")
	}
	// Span id yok → span'siz link, stack okunmaz.
	noSpan := []chstore.ExceptionRow{{Type: "C", Count: 1, LastSeen: 1, SampleTraceID: excTID1}}
	rows = buildExcSampleRows(noSpan, nil, nil, nil)
	if rows[0].TraceID != excTID1 || rows[0].SpanID != "" || excTraceHref(rows[0].TraceID, rows[0].SpanID) != "/trace?id="+excTID1 {
		t.Errorf("span'siz örnek: %+v", rows[0])
	}
	if len(excSampleRefs(noSpan)) != 0 {
		t.Error("span'siz örnek stack okumasına girmemeli")
	}
}

func TestExcSampleBounds(t *testing.T) {
	var many []chstore.ExceptionRow
	for i := 0; i < 30; i++ {
		many = append(many, chstore.ExceptionRow{Type: fmt.Sprintf("x.E%d", i), Count: uint64(100 - i), LastSeen: int64(1e18) + int64(i),
			SampleTraceID: fmt.Sprintf("%032x", i+1), SampleSpanID: fmt.Sprintf("%016x", i+1)})
	}
	if refs := excSampleRefs(many); len(refs) != serviceExcSampleMax {
		t.Errorf("okuma tavanı: %d örnek, want %d", len(refs), serviceExcSampleMax)
	}
	rows := buildExcSampleRows(many, nil, nil, nil)
	if len(rows) != serviceExcSampleMax {
		t.Errorf("tablo tavanı: %d satır", len(rows))
	}
	if links := excSampleLinks(rows); len(links) != serviceExcLinkChips || !strings.HasPrefix(links[0].Href, "/trace?id=") {
		t.Errorf("çip tavanı/biçimi: %+v", links)
	}
	// Uzun / çözümlenemeyen stack: en çok serviceExcStackLines satır, satır başı rune tavanı.
	raw := strings.Repeat(strings.Repeat("ş", 500)+"\n", 50)
	lines, lib, isRaw := excStackExcerpt(raw, nil)
	if !isRaw || lib || len(lines) != serviceExcStackLines {
		t.Fatalf("ham kesit: raw=%v lib=%v n=%d", isRaw, lib, len(lines))
	}
	for _, l := range lines {
		if n := len([]rune(l)); n > serviceExcFrameRunes+1 {
			t.Errorf("satır %d rune (tavan %d)", n, serviceExcFrameRunes)
		}
	}
	app, _, _ := excStackExcerpt(excAppStack, nil)
	if len(app) == 0 || len(app) > serviceExcStackLines {
		t.Errorf("uygulama kesiti %d satır", len(app))
	}
}

func TestExcStacksCacheKeyHashesAllInputs(t *testing.T) {
	a := []chstore.ExceptionSpanRef{{TraceID: excTID1, SpanID: excSID1, TimeNs: 1}, {TraceID: excTID2, SpanID: excSID2, TimeNs: 2}}
	b := []chstore.ExceptionSpanRef{a[1], a[0]}
	if excStacksCacheKey("orders-api", a) != excStacksCacheKey("orders-api", b) {
		t.Error("sıra anahtarı değiştirmemeli")
	}
	c := []chstore.ExceptionSpanRef{a[0], {TraceID: excTID2, SpanID: excSID1, TimeNs: 2}}
	if excStacksCacheKey("orders-api", a) == excStacksCacheKey("orders-api", c) {
		t.Error("aynı uzunlukta farklı küme aynı anahtar (v0.5.187 sınıfı)")
	}
	if excStacksCacheKey("orders-api", a) == excStacksCacheKey("billing-api", a) {
		t.Error("servis anahtara girmeli")
	}
}

func TestServiceExceptionsBundleSamples(t *testing.T) {
	f := &fakeSvcExcReader{rows: excFixtureRows(), stacks: excFixtureStacks()}
	installExcReader(t, f)
	s := &Server{cache: &ctxMemCache{m: map[string][]byte{}}}
	to := time.Date(2026, 10, 23, 12, 30, 0, 0, time.UTC)
	route := guidedRoute{Intent: guidedServiceExceptions, Service: "orders-api"}
	_, emit := captureEmit()
	ev, _, err := s.guidedServiceExceptionsBundle(context.Background(), emit, &route, to.Add(-time.Hour), to)
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 || len(f.lastRefs) != 2 {
		t.Fatalf("tek toplu stack okuması bekleniyordu (2 örnek): calls=%d refs=%d", f.calls, len(f.lastRefs))
	}
	if !strings.Contains(route.AnswerTail, "/trace?id="+excTID1+"&span="+excSID1) || !strings.Contains(route.AnswerTail, "OrderService.reserve") {
		t.Errorf("sunucu eki örnek trace/stack taşımıyor:\n%s", route.AnswerTail)
	}
	if !strings.Contains(ev, "tabloyu tekrar yazma") || !strings.Contains(ev, "OrderService.reserve") {
		t.Errorf("kanıt notu eksik:\n%s", ev)
	}
	links := guidedAnswerLinkTargets(route)
	if len(links) != 3 || links[0].Href != "/problems?service=orders-api" || links[1].Href != "/trace?id="+excTID1+"&span="+excSID1 {
		t.Errorf("çipler: %+v", links)
	}
	// İkinci tur: önbellekten (okuma yok).
	route2 := guidedRoute{Intent: guidedServiceExceptions, Service: "orders-api"}
	if _, _, err := s.guidedServiceExceptionsBundle(context.Background(), emit, &route2, to.Add(-time.Hour), to); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Errorf("stack önbelleği kullanılmadı: %d okuma", f.calls)
	}
	// Okuma hatası: demet düşmez, linkler kalır.
	f2 := &fakeSvcExcReader{rows: excFixtureRows(), stackErr: errors.New("max_execution_time")}
	installExcReader(t, f2)
	s2 := &Server{cache: &ctxMemCache{m: map[string][]byte{}}}
	route3 := guidedRoute{Intent: guidedServiceExceptions, Service: "orders-api"}
	if _, _, err := s2.guidedServiceExceptionsBundle(context.Background(), emit, &route3, to.Add(-time.Hour), to); err != nil {
		t.Fatalf("stack okuma hatası demeti düşürmemeli: %v", err)
	}
	if !strings.Contains(route3.AnswerTail, "stack okunamadı") || !strings.Contains(route3.AnswerTail, "/trace?id="+excTID1) {
		t.Errorf("hata hâli:\n%s", route3.AnswerTail)
	}
	// Grup yok: ek yok, okuma yok.
	f3 := &fakeSvcExcReader{}
	installExcReader(t, f3)
	route4 := guidedRoute{Intent: guidedServiceExceptions, Service: "orders-api"}
	ev4, _, _ := s.guidedServiceExceptionsBundle(context.Background(), emit, &route4, to.Add(-time.Hour), to)
	if route4.AnswerTail != "" || f3.calls != 0 || !strings.Contains(ev4, "teknik hata (exception) yok") {
		t.Errorf("boş hâl: tail=%q calls=%d", route4.AnswerTail, f3.calls)
	}
}
