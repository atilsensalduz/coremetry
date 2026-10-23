package api

// copilot_service_exc_samples.go — v0.10.1486: servis exception cevabı grup
// başına ÖRNEK TRACE + KISA STACK taşır (operatör onayı 2026-10-23; v0.10.1479
// DECISIONS "bilinen boşluk": "<svc> exception grupları neler, örnek stack ve
// ilgili trace'i bul" service_exceptions'a gidiyordu ama demet yalnız tür ·
// sayı · mesaj yazıyordu).
//
// Karar (sunucu toplar, model anlatır):
//   - Örnek trace/span işaretçisi GetExceptions'tan (argMax(trace_id/span_id,
//     time)) — ek okuma yok. Link /trace?id=…&span=… (explain_span emsali).
//   - Stack metni ayrı, TEK ve sınırlı okuma (chstore.ExceptionSampleStacks:
//     en çok serviceExcSampleMax örnek, örnek başına ±1 sn zaman dilimi,
//     LIMIT, max_execution_time, metin SQL'de kırpılı), kısa alt-tavan
//     (serviceExcStackTimeout) ve Redis önbelleği (örnek span değişmez).
//   - Kesit stackparse.SummarizeAppFrames: ilk serviceExcStackLines uygulama
//     çerçevesi (DevOps uygulama önekleri varsa önce); uygulama çerçevesi
//     yoksa fırlatma noktası "(kütüphane)" etiketiyle; çözümlenemeyen biçimde
//     ilk birkaç satır. Tam stack ASLA basılmaz.
//   - Tablo sunucu eki (route.AnswerTail, copilot_team_overview.go emsali):
//     model düşüremez/değiştiremez; trace ID'sini model uyduramaz. İlk
//     serviceExcLinkChips örnek ayrıca link çipi.
//   - Dürüstlük: örnek işaretçisi yok → "örnek yok"; stack okunamadı ≠ stack
//     yok; kalan gruplar Exceptions linkiyle.

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/stackparse"
)

const (
	// serviceExcSampleMax — örnek trace + stack basılan en çok grup (en çok
	// görülen önce). Stack okumasının da tavanı.
	serviceExcSampleMax = 5
	// serviceExcStackLines — grup başına stack kesitindeki en çok satır.
	serviceExcStackLines = 4
	// serviceExcFrameRunes — tek kesit satırının rune tavanı.
	serviceExcFrameRunes = 140
	// serviceExcLinkChips — cevap altındaki örnek trace çipleri.
	serviceExcLinkChips = 3
	// serviceExcStackTimeout — stack okumasının alt-tavanı: düşerse tablo
	// trace linkleriyle ve "stack okunamadı" notuyla yine basılır.
	serviceExcStackTimeout = 4 * time.Second
	// serviceExcStackTTL — örnek span'in stack'i değişmez; pencere kayınca
	// örnek değişir, anahtar da.
	serviceExcStackTTL = 10 * time.Minute
)

var excSpanIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// serviceExcReader — demetin iki okuması (test dikişi; outboundReadFn emsali).
type serviceExcReader interface {
	GetExceptions(ctx context.Context, f chstore.ExceptionFilter) ([]chstore.ExceptionRow, error)
	ExceptionSampleStacks(ctx context.Context, service string, refs []chstore.ExceptionSpanRef) (map[string]string, error)
}

var serviceExcReadFn = func(s *Server) serviceExcReader {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store
}

// excStackState — örneğin stack hâli (dürüst ayrım).
type excStackState int

const (
	excStackOK       excStackState = iota
	excStackNoSample               // grubun örnek işaretçisi yok
	excStackUnread                 // okuma düştü / zaman aşımı (yok DEĞİL)
	excStackMissing                // örnek okundu ama stacktrace alanı boş / span bulunamadı
)

// excSampleRow — tablonun bir satırı.
type excSampleRow struct {
	Type    string
	Count   uint64
	TraceID string // "" = örnek yok
	SpanID  string // "" = span'siz link
	Lines   []string
	Library bool // uygulama çerçevesi yok; satır fırlatma noktası (kütüphane)
	Raw     bool // çözümlenemeyen biçim; ham ilk satırlar
	Stack   excStackState
}

// excSampleRef — SAF: satırın geçerli örnek işaretçisi (küçük harf hex).
func excSampleRef(e chstore.ExceptionRow) (traceID, spanID string) {
	tid := strings.ToLower(strings.TrimSpace(e.SampleTraceID))
	if !fuTraceRe.MatchString(tid) || strings.Trim(tid, "0") == "" {
		return "", ""
	}
	sid := strings.ToLower(strings.TrimSpace(e.SampleSpanID))
	if !excSpanIDRe.MatchString(sid) || strings.Trim(sid, "0") == "" {
		sid = ""
	}
	return tid, sid
}

// excSampleRefs — SAF: stack okumasına gidecek işaretçiler (ilk
// serviceExcSampleMax grup, örneği olanlar).
func excSampleRefs(exs []chstore.ExceptionRow) []chstore.ExceptionSpanRef {
	var out []chstore.ExceptionSpanRef
	for i, e := range exs {
		if i >= serviceExcSampleMax {
			break
		}
		tid, sid := excSampleRef(e)
		if tid == "" || sid == "" || e.LastSeen <= 0 {
			continue
		}
		out = append(out, chstore.ExceptionSpanRef{TraceID: tid, SpanID: sid, TimeNs: e.LastSeen})
	}
	return out
}

// excStackExcerpt — SAF: stack → kısa kesit (≤ serviceExcStackLines satır).
func excStackExcerpt(stack string, appPrefixes []string) (lines []string, library, raw bool) {
	stack = strings.TrimSpace(stack)
	if stack == "" {
		return nil, false, false
	}
	sum := stackparse.SummarizeAppFrames(stack, serviceExcStackLines, appPrefixes)
	switch {
	case len(sum.App) > 0:
		for _, f := range sum.App {
			lines = append(lines, truncateRunes(f.Short(), serviceExcFrameRunes))
		}
		return lines, false, false
	case sum.HasThrown:
		return []string{truncateRunes(sum.Thrown.Short(), serviceExcFrameRunes)}, true, false
	}
	for _, l := range strings.Split(stack, "\n") {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		lines = append(lines, truncateRunes(l, serviceExcFrameRunes))
		if len(lines) >= serviceExcStackLines {
			break
		}
	}
	return lines, false, true
}

// buildExcSampleRows — SAF: gruplar + okunan stack'ler → tablo satırları.
// stacks nil + stackErr → örnekli satırlar excStackUnread.
func buildExcSampleRows(exs []chstore.ExceptionRow, stacks map[string]string, stackErr error, appPrefixes []string) []excSampleRow {
	var out []excSampleRow
	for i, e := range exs {
		if i >= serviceExcSampleMax {
			break
		}
		r := excSampleRow{Type: e.Type, Count: e.Count}
		r.TraceID, r.SpanID = excSampleRef(e)
		switch {
		case r.TraceID == "":
			r.Stack = excStackNoSample
		case r.SpanID == "" || e.LastSeen <= 0:
			r.Stack = excStackMissing
		case stackErr != nil:
			r.Stack = excStackUnread
		default:
			st, ok := stacks[chstore.ExceptionSampleKey(r.TraceID, r.SpanID)]
			r.Lines, r.Library, r.Raw = excStackExcerpt(st, appPrefixes)
			if !ok || len(r.Lines) == 0 {
				r.Stack = excStackMissing
			}
		}
		out = append(out, r)
	}
	return out
}

// excTraceHref — SAF: örnek trace linki (span varsa span'e odaklı).
func excTraceHref(traceID, spanID string) string {
	h := "/trace?id=" + url.QueryEscape(traceID)
	if spanID != "" {
		h += "&span=" + url.QueryEscape(spanID)
	}
	return h
}

// renderServiceExcSamplesTR — SAF (tablo testli): sunucu eki. total = toplam
// grup sayısı (kalanlar için Exceptions linki).
func renderServiceExcSamplesTR(service string, rows []excSampleRow, total int) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n**Örnek trace ve stack** — en çok görülen %d grup (stack: ilk uygulama çerçeveleri; JDK / uygulama sunucusu / kütüphane satırları atlandı):\n\n", len(rows))
	b.WriteString("| # | Exception | Sayı | Örnek trace | Stack kesiti |\n| ---: | --- | ---: | --- | --- |\n")
	for i, r := range rows {
		trace := "örnek yok"
		if r.TraceID != "" {
			trace = fmt.Sprintf("[%s…](%s)", r.TraceID[:12], excTraceHref(r.TraceID, r.SpanID))
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s |\n", i+1, mdCell(truncateRunes(r.Type, 80)), fmtThousands(r.Count), trace, excStackCell(r))
	}
	if more := total - len(rows); more > 0 {
		fmt.Fprintf(&b, "\n+%d grup daha; örnekleri: [%s · Exceptions](/problems?service=%s)\n", more, mdCell(service), url.QueryEscape(service))
	}
	return b.String()
}

// excStackCell — SAF: tablonun stack hücresi.
func excStackCell(r excSampleRow) string {
	switch r.Stack {
	case excStackNoSample:
		return "—"
	case excStackUnread:
		return "stack okunamadı (zaman aşımı / okuma hatası)"
	case excStackMissing:
		return "stack yok (örnek span'de stacktrace alanı boş)"
	}
	parts := make([]string, 0, len(r.Lines))
	for _, l := range r.Lines {
		parts = append(parts, "`"+strings.ReplaceAll(mdCell(l), "`", "'")+"`")
	}
	cell := strings.Join(parts, " ← ")
	switch {
	case r.Library:
		cell = "uygulama çerçevesi yok; fırladığı yer (kütüphane): " + cell
	case r.Raw:
		cell = "ilk satırlar: " + strings.Join(parts, " · ")
	}
	return cell
}

// excSampleLinks — SAF: ilk serviceExcLinkChips örnek trace çipi.
func excSampleLinks(rows []excSampleRow) []guidedAnswerLink {
	var out []guidedAnswerLink
	for _, r := range rows {
		if r.TraceID == "" {
			continue
		}
		t := r.Type
		if i := strings.LastIndexAny(t, ".:"); i >= 0 && i < len(t)-1 {
			t = t[i+1:]
		}
		out = append(out, guidedAnswerLink{Label: "Örnek trace · " + truncateRunes(t, 40), Href: excTraceHref(r.TraceID, r.SpanID)})
		if len(out) >= serviceExcLinkChips {
			break
		}
	}
	return out
}

// excSampleEvidenceTR — SAF: modele giden kısa not + grup başına ilk çerçeve.
func excSampleEvidenceTR(rows []excSampleRow) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Örnek trace + stack tablosunu sunucu cevabın sonuna ekliyor (tabloyu tekrar yazma, trace ID yazma/uydurma). İlk gruplar için stack'in ilk satırı:\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "- %s: ", r.Type)
		switch {
		case r.Stack == excStackOK && len(r.Lines) > 0 && r.Library:
			b.WriteString("uygulama çerçevesi yok, kütüphanede fırladı " + r.Lines[0])
		case r.Stack == excStackOK && len(r.Lines) > 0:
			b.WriteString(r.Lines[0])
		default:
			b.WriteString(oneLine(excStackCell(r)))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// excStacksCacheKey — SAF: TÜM girdilerin sıralı FNV özeti (cache.go kuralı).
func excStacksCacheKey(service string, refs []chstore.ExceptionSpanRef) string {
	keys := make([]string, 0, len(refs))
	for _, r := range refs {
		keys = append(keys, fmt.Sprintf("%s/%s@%d", r.TraceID, r.SpanID, r.TimeNs))
	}
	sort.Strings(keys)
	h := fnv.New64a()
	_, _ = h.Write([]byte(service))
	for _, k := range keys {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(k))
	}
	return fmt.Sprintf("copilot:excstacks:v1:%x", h.Sum64())
}

// readServiceExcStacks — tek sınırlı okuma, önbellekli. nil harita + hata =
// okunamadı.
func (s *Server) readServiceExcStacks(ctx context.Context, rd serviceExcReader, service string, refs []chstore.ExceptionSpanRef) (map[string]string, error) {
	if len(refs) == 0 {
		return map[string]string{}, nil
	}
	key := excStacksCacheKey(service, refs)
	if s.cache != nil {
		if b, ok, _ := s.cache.Get(ctx, key); ok && len(b) > 0 {
			var m map[string]string
			if json.Unmarshal(b, &m) == nil {
				return m, nil
			}
		}
	}
	sctx, cancel := context.WithTimeout(ctx, serviceExcStackTimeout)
	defer cancel()
	m, err := rd.ExceptionSampleStacks(sctx, service, refs)
	if err != nil {
		return nil, err
	}
	if s.cache != nil {
		if b, merr := json.Marshal(m); merr == nil {
			_ = s.cache.Set(ctx, key, b, serviceExcStackTTL)
		}
	}
	return m, nil
}

// serviceExcAppPrefixes — operatörün DevOps uygulama önekleri (yoksa nil).
func (s *Server) serviceExcAppPrefixes() []string {
	if s == nil || s.devops == nil {
		return nil
	}
	return s.devops.Snapshot().AppPrefixes
}

// truncateRunes — rune güvenli kırpma ("…" ekli).
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
