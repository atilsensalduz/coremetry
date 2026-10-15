package api

// v0.10.1213 — operatör: "son 1 saatteki log hataları" cevabı severity
// toplamı + küratörlü desen eşleşmesiyle geliyordu ("64.394 ERROR, eşleşme
// yok") — yorumlanabilir değil. Şimdi en yeni ~200 hata satırı okunup gövde
// şablonuna (sayı/hex/uuid/tırnak maskeli) göre gruplanıyor; LLM gerçek
// mesajları görüp yorumluyor. Hata satırı yoksa ve servis verilmişse
// servisin son satırları (seviyesiz) örneklenir — dürüst etiketle.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	guidedLogSampleN      = 200
	guidedLogSampleGroups = 8
	guidedLogSampleBody   = 240
)

// guidedRecentErrorSample — en yeni hata satırları; fallback=true → seviye
// süzgeci tutmadı, servisin son satırları döndü.
func (s *Server) guidedRecentErrorSample(ctx context.Context, service string, from, to time.Time) (recs []*logstore.LogRecord, fallback bool, err error) {
	if s.logs == nil {
		return nil, false, fmt.Errorf("log deposu yok")
	}
	f := logstore.Filter{Service: service, From: from, To: to, SeverityMin: 17, Search: "level:error",
		Limit: guidedLogSampleN, SkipTotal: true, SoftTimeout: 8 * time.Second}
	p, err := s.logs.Search(ctx, f)
	if err != nil {
		return nil, false, err
	}
	if len(p.Logs) > 0 || service == "" {
		return p.Logs, false, nil
	}
	f.SeverityMin, f.Search = 0, ""
	p, err = s.logs.Search(ctx, f)
	if err != nil {
		return nil, true, err
	}
	return p.Logs, true, nil
}

var (
	logTplUUID  = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	logTplHex   = regexp.MustCompile(`(?i)\b(?:0x)?[0-9a-f]*[0-9][0-9a-f]*[a-f][0-9a-f]*\b|@[0-9a-f]{4,}\b`)
	logTplQuote = regexp.MustCompile(`"[^"]{0,200}"|'[^']{0,200}'`)
	logTplNum   = regexp.MustCompile(`\d+`)
	logTplWS    = regexp.MustCompile(`\s+`)
)

// logBodyTemplate — SAF: değişken parçaları maskeleyip ilk 120 karakteri anahtar yapar.
func logBodyTemplate(body string) string {
	t := strings.TrimSpace(body)
	if i := strings.IndexByte(t, '\n'); i > 0 {
		t = t[:i]
	}
	t = logTplUUID.ReplaceAllString(t, "<id>")
	t = logTplQuote.ReplaceAllString(t, "<s>")
	t = logTplHex.ReplaceAllString(t, "<hex>")
	t = logTplNum.ReplaceAllString(t, "<n>")
	t = logTplWS.ReplaceAllString(t, " ")
	if r := []rune(t); len(r) > 120 {
		t = string(r[:120])
	}
	return t
}

type logSampleGroup struct {
	Template string
	Count    int
	Services []string
	Example  string
	TraceID  string
	Newest   int64
}

// groupLogSample — SAF: şablona göre grupla, sayıya (eşitse yeniliğe) göre sırala.
func groupLogSample(recs []*logstore.LogRecord, max int) (groups []logSampleGroup, rest int) {
	idx := map[string]int{}
	svcSeen := map[string]map[string]bool{}
	for _, r := range recs {
		if r == nil || strings.TrimSpace(r.Body) == "" {
			continue
		}
		k := logBodyTemplate(r.Body)
		i, ok := idx[k]
		if !ok {
			i = len(groups)
			idx[k] = i
			svcSeen[k] = map[string]bool{}
			groups = append(groups, logSampleGroup{Template: k, Example: r.Body, TraceID: r.TraceID, Newest: r.Timestamp})
		}
		g := &groups[i]
		g.Count++
		if r.Timestamp > g.Newest {
			g.Newest = r.Timestamp
		}
		if g.TraceID == "" && r.TraceID != "" {
			g.TraceID = r.TraceID
		}
		if r.ServiceName != "" && !svcSeen[k][r.ServiceName] && len(g.Services) < 3 {
			svcSeen[k][r.ServiceName] = true
			g.Services = append(g.Services, r.ServiceName)
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Newest > groups[j].Newest
	})
	if len(groups) > max {
		for _, g := range groups[max:] {
			rest += g.Count
		}
		groups = groups[:max]
	}
	return groups, rest
}

// renderLogSampleTR — SAF: örneklem grupları kanıt metni.
func renderLogSampleTR(recs []*logstore.LogRecord, fallback bool, service string) string {
	var b strings.Builder
	what := "hata satırı"
	if fallback {
		what = "satır (hata seviyeli satır bulunamadı — seviyesiz son loglar)"
	}
	scope := ""
	if service != "" {
		scope = ", servis: " + service
	}
	if len(recs) == 0 {
		fmt.Fprintf(&b, "Son log örneklemi%s: pencerede %s yok.\n", scope, what)
		return b.String()
	}
	lo, hi := recs[0].Timestamp, recs[0].Timestamp
	for _, r := range recs {
		if r.Timestamp < lo {
			lo = r.Timestamp
		}
		if r.Timestamp > hi {
			hi = r.Timestamp
		}
	}
	groups, rest := groupLogSample(recs, guidedLogSampleGroups)
	fmt.Fprintf(&b, "Son %d %s%s (%s'lık aralık), mesaj şablonuna göre gruplar:\n", len(recs), what, scope,
		fmtDurTR(time.Duration(hi-lo)))
	for _, g := range groups {
		ex := strings.Join(strings.Fields(g.Example), " ")
		if r := []rune(ex); len(r) > guidedLogSampleBody {
			ex = string(r[:guidedLogSampleBody]) + "…"
		}
		fmt.Fprintf(&b, "- ×%d", g.Count)
		if len(g.Services) > 0 && service == "" {
			fmt.Fprintf(&b, " [%s]", strings.Join(g.Services, ", "))
		}
		fmt.Fprintf(&b, " %s", ex)
		if g.TraceID != "" {
			fmt.Fprintf(&b, " (trace %s)", g.TraceID)
		}
		b.WriteString("\n")
	}
	if rest > 0 {
		fmt.Fprintf(&b, "- diğer gruplar: %d satır\n", rest)
	}
	b.WriteString("Not: son-satır örneklemi; sayılar yalnız örneklem içindir. Cevapta baskın grupları gerçek mesajlarıyla adlandır, olası nedeni ve etkilenen servisi/çağrıyı yorumla; toplam severity sayısını öne çıkarma.\n")
	return b.String()
}

// fmtDurTR — SAF: kısa süre metni (örneklemin kapsadığı aralık).
func fmtDurTR(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d sn", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d dk", int(d.Minutes()))
	default:
		return fmt.Sprintf("%.1f sa", d.Hours())
	}
}
