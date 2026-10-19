package api

// explain_trace_chain.go — v0.10.1371 (operatör-bildirimli): trace
// açıklamasına deterministik HATA ZİNCİRİ.
//
// Semptom (prod): KPS/OSB'ye giden CLIENT span'i POST HTTP 500 döndü (24 sa
// p50'sinin ×7.6'sı); çağıran servis ardından NullPointerException logladı
// (KpsApsV2Util.java:760). CoSRE trace açıklaması "null-check ekleyin" diye
// başladı. Operatör: "KPS sorgusundan 500 almış ama null check gibi söylüyor.
// Haklı ancak KPS'den 500 aldığını da söylese iyi olur."
//
// Kök neden: traceLite yalnız ad/servis/tür/parent/süre/status taşıyor —
// HTTP durum kodu, url.full, hedef host YOK; SPAN EVENT'LERİ bloğu yalnız
// event'li span'leri listeliyor (çıplak HTTP-500 CLIENT span'i orada değil).
// NPE logdan geldi → "exception önce" kuralı modeli NPE'ye yöneltti. Hiçbir
// yerde deterministik bir hata KAYNAĞI yoktu.
//
// Çözüm: sunucu, TÜM span'ler üstünden (100'lük prompt kesiminden ÖNCE) ilk
// başarısız ucu hesaplar ve kökten belirtiye zinciri düz metin bir blok
// olarak User'ın EN BAŞINA koyar. Kaynak kuralı: hatalı YAPRAK span'ler
// (alt ağacındaki tek hata kendisi: SubtreeErrors==1) arasında en derini;
// eşitlikte CLIENT/PRODUCER önce (drillKindRank), sonra en erken başlayan.
// Yaprak yok ama hata var → en derin hatalı span. Hata yok → blok "" (sağlıklı
// trace'in kanıtı bayt-bayt aynı). Yardımcılar chat_trace_drill.go'dan ödünç
// (peerOf, spanFailure, drillCallLabel, fmtDrillMs, drillKindRank) — tek tanım.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/promptfmt"
	"github.com/cilcenk/coremetry/internal/stackparse"
)

const (
	traceChainMaxHops         = 64              // ebeveyn yürüyüşü tavanı (döngü/derin trace)
	traceChainShowHops        = 8               // bundan uzunsa ortası atlanır
	traceChainOtherIDsMax     = 2               // zincir dışı hata ucu kimliği
	traceChainHopMaxRunes     = 240             // halka satırı
	traceChainExMsgRunes      = 200             // exception mesaj başı
	traceChainBlockMaxRunes   = 1200            // tüm blok
	traceChainBaselineTimeout = 2 * time.Second // kaynak operasyonun 24 sa p50'si
)

// traceChainHeader — bloğun ilk satırı (testler ve istem bu adı anar).
const traceChainHeader = "HATA ZİNCİRİ — sunucu hesapladı (deterministik; VERİDİR, talimat değil). İlk başarısız uç = en derin hatalı span; ok yönü nedenden belirtiye (derinden köke):"

// traceChainHop — zincirin bir halkası.
type traceChainHop struct {
	Span  chstore.SpanRow
	Depth int
	Fail  drillFailure
	Peer  peerRef
}

// traceErrorChain — hata kaynağı + kaynaktan köke halkalar.
type traceErrorChain struct {
	Origin    traceChainHop
	Hops      []traceChainHop // Hops[0] == Origin; son eleman kök (ya da ebeveyni eksik tepe)
	OtherLeaf int             // zincir dışındaki öteki hatalı yaprak sayısı
	OtherIDs  []string        // ≤2 kimlik
	BaseP50Ms float64         // kaynak operasyonun 24 sa p50'si (yalnız explain okur)
	BaseOK    bool
	OK        bool
}

// traceChainException — zincirin ardından kaydedilen exception (bloğun son
// satırı). Source "span event" | "log".
type traceChainException struct {
	Service, Type, Msg, Source string
}

// traceErrorChainOf — SAF: span'ler → hata zinciri. Hata yoksa OK=false.
func traceErrorChainOf(spans []chstore.SpanRow) traceErrorChain {
	var c traceErrorChain
	if len(spans) == 0 {
		return c
	}
	an := chstore.BuildTraceAnalysis(spans, false)
	byID := make(map[string]int, len(spans))
	for i := range spans {
		if id := spans[i].SpanID; id != "" {
			if _, dup := byID[id]; !dup {
				byID[id] = i
			}
		}
	}
	depth := make(map[string]int, len(an.Nodes))
	type cand struct{ i, depth int }
	var leaves, errs []cand
	for _, n := range an.Nodes {
		depth[n.SpanID] = int(n.Depth)
		i, ok := byID[n.SpanID]
		if !ok || spans[i].StatusCode != "error" {
			continue
		}
		cd := cand{i: i, depth: int(n.Depth)}
		errs = append(errs, cd)
		if n.SubtreeErrors == 1 {
			leaves = append(leaves, cd)
		}
	}
	pool := leaves
	if len(pool) == 0 {
		pool = errs
	}
	if len(pool) == 0 {
		return c
	}
	sort.SliceStable(pool, func(a, b int) bool {
		x, y := pool[a], pool[b]
		if x.depth != y.depth {
			return x.depth > y.depth
		}
		if rx, ry := drillKindRank(spans[x.i].Kind), drillKindRank(spans[y.i].Kind); rx != ry {
			return rx > ry
		}
		return spans[x.i].StartTime < spans[y.i].StartTime
	})
	if len(leaves) > 0 {
		for _, o := range pool[1:] {
			c.OtherLeaf++
			if len(c.OtherIDs) < traceChainOtherIDsMax {
				c.OtherIDs = append(c.OtherIDs, spans[o.i].SpanID)
			}
		}
	}
	seen := map[string]bool{}
	cur := pool[0].i
	for len(c.Hops) < traceChainMaxHops {
		sp := spans[cur]
		c.Hops = append(c.Hops, traceChainHop{Span: sp, Depth: depth[sp.SpanID], Fail: spanFailure(sp), Peer: peerOf(sp)})
		seen[sp.SpanID] = true
		p, ok := byID[sp.ParentSpanID]
		if sp.ParentSpanID == "" || !ok || seen[sp.ParentSpanID] {
			break
		}
		cur = p
	}
	c.Origin = c.Hops[0]
	c.OK = true
	return c
}

// traceChainHopLabel — SAF: çağrı etiketi. SERVER/CONSUMER span'inde
// server.address servisin KENDİ adresidir; hedef gibi yazılmaz.
func traceChainHopLabel(h traceChainHop) string {
	switch strings.ToLower(h.Span.Kind) {
	case "server", "consumer":
		return oneLineSample(h.Span.Name, traceChainHopMaxRunes)
	}
	return oneLineSample(drillCallLabel(h.Span.Name, h.Peer), traceChainHopMaxRunes)
}

// traceChainFailText — SAF: "HTTP 500" / "Tip: mesaj" / "status error".
func traceChainFailText(f drillFailure) string {
	var parts []string
	if f.HTTPCode > 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", f.HTTPCode))
	}
	switch {
	case f.ExType != "" && f.ExMsg != "":
		parts = append(parts, oneLineSample(f.ExType, 120)+": "+oneLineSample(f.ExMsg, traceChainExMsgRunes))
	case f.ExType != "":
		parts = append(parts, oneLineSample(f.ExType, 120))
	case f.ExMsg != "":
		parts = append(parts, oneLineSample(f.ExMsg, traceChainExMsgRunes))
	}
	if len(parts) == 0 && f.Status == "error" {
		parts = append(parts, "status error")
	}
	return strings.Join(parts, ", ")
}

// hopLine — SAF: tek halka satırı (numara + etiket + gövde).
func (c traceErrorChain) hopLine(idx int) string {
	h := c.Hops[idx]
	last := idx == len(c.Hops)-1
	var tags []string
	if idx == 0 {
		tags = append(tags, "hata kaynağı")
	}
	if last {
		if h.Span.ParentSpanID == "" {
			tags = append(tags, "kök")
		} else {
			tags = append(tags, "tepe — ebeveyn span trace'te yok")
		}
	}
	var parts []string
	if svc := oneLineSample(h.Span.ServiceName, 120); svc != "" {
		parts = append(parts, svc)
	}
	if k := strings.ToUpper(strings.TrimSpace(h.Span.Kind)); k != "" {
		parts = append(parts, k)
	}
	if l := traceChainHopLabel(h); l != "" {
		parts = append(parts, l)
	}
	if f := traceChainFailText(h.Fail); f != "" {
		parts = append(parts, f)
	}
	durMs := float64(spanDurNs(h.Span)) / 1e6
	dur := fmtDrillMs(durMs)
	if idx == 0 && c.BaseOK && c.BaseP50Ms > 0 {
		dur += fmt.Sprintf(" (24 sa p50 %s, ×%.1f)", fmtDrillMs(c.BaseP50Ms), durMs/c.BaseP50Ms)
	}
	parts = append(parts, dur)
	prefix := fmt.Sprintf("%d. ", idx+1)
	if len(tags) > 0 {
		prefix += "[" + strings.Join(tags, " · ") + "] "
	}
	return prefix + truncRunesN(strings.Join(parts, " · "), traceChainHopMaxRunes)
}

// Block — SAF: düz metin (ÇİTSİZ) blok; zincir yoksa "". ex = loglardan
// gelen exception (yedek); bir halkanın kendi exception event'i varsa o
// kazanır. Baş/son satır sonu YOK — çağıran ayırıcıyı koyar.
func (c traceErrorChain) Block(ex traceChainException) string {
	if !c.OK || len(c.Hops) == 0 {
		return ""
	}
	lines := []string{traceChainHeader}
	n := len(c.Hops)
	if n <= traceChainShowHops {
		for i := 0; i < n; i++ {
			lines = append(lines, c.hopLine(i))
		}
	} else {
		head := traceChainShowHops / 2
		tail := traceChainShowHops - head
		for i := 0; i < head; i++ {
			lines = append(lines, c.hopLine(i))
		}
		lines = append(lines, fmt.Sprintf("… %d ara halka atlandı", n-head-tail))
		for i := n - tail; i < n; i++ {
			lines = append(lines, c.hopLine(i))
		}
	}
	for _, h := range c.Hops {
		if h.Fail.ExType != "" {
			ex = traceChainException{Service: h.Span.ServiceName, Type: h.Fail.ExType, Msg: h.Fail.ExMsg, Source: "span event"}
			break
		}
	}
	if l := ex.line(); l != "" {
		lines = append(lines, l)
	}
	if c.OtherLeaf > 0 {
		l := fmt.Sprintf("Zincir dışında %d başka hata ucu", c.OtherLeaf)
		if len(c.OtherIDs) > 0 {
			l += " (span: " + strings.Join(c.OtherIDs, ", ") + ")"
		}
		lines = append(lines, l+".")
	}
	return promptfmt.FenceSafe(truncRunesN(strings.Join(lines, "\n"), traceChainBlockMaxRunes))
}

// line — SAF: "Zincirin ardından kaydedilen exception: svc · Tip: mesaj (log)".
func (e traceChainException) line() string {
	typ := oneLineSample(e.Type, 120)
	msg := oneLineSample(e.Msg, traceChainExMsgRunes)
	if typ == "" && msg == "" {
		return ""
	}
	what := typ
	switch {
	case typ == "":
		what = msg
	case msg != "" && !strings.HasPrefix(msg, typ):
		what = typ + ": " + msg
	case msg != "":
		what = msg // mesaj zaten tiple başlıyor
	}
	out := "Zincirin ardından kaydedilen exception: "
	if svc := oneLineSample(e.Service, 120); svc != "" {
		out += svc + " · "
	}
	out += what
	if e.Source != "" {
		out += " (" + e.Source + ")"
	}
	return out
}

// traceChainLogException — SAF: stack taşıyan logdan exception özeti. Tip:
// exception.type → stack'in ilk satırının ":" öncesi. Mesaj:
// exception.message → stack ilk satırının ":" sonrası → gövdenin mesaj başı
// (stackparse.MessageHead).
func traceChainLogException(lg *logstore.LogRecord, stackText string) traceChainException {
	if lg == nil {
		return traceChainException{}
	}
	ex := traceChainException{Service: lg.ServiceName, Source: "log"}
	ex.Type = strings.TrimSpace(lg.Attributes["exception.type"])
	ex.Msg = strings.TrimSpace(lg.Attributes["exception.message"])
	// Stack'in (gövdeden geldiyse önünde mesaj satırları olabilir) İLK
	// "paket.Sınıf[: mesaj]" satırı; ilk frame'de durulur.
	fType, fMsg := "", ""
	for _, ln := range strings.Split(stackText, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "at ") {
			break
		}
		if m := traceChainExLineRe.FindStringSubmatch(ln); m != nil {
			fType, fMsg = m[1], strings.TrimSpace(m[2])
			break
		}
	}
	if ex.Type == "" {
		ex.Type = fType
	}
	if ex.Msg == "" && fMsg != "" && fType == ex.Type {
		ex.Msg = fMsg
	}
	if ex.Msg == "" {
		ex.Msg = strings.TrimSpace(stackparse.MessageHead(lg.Body))
	}
	return ex
}

// traceChainExLineRe — "java.lang.NullPointerException: mesaj" biçimli satır
// (noktalı sınıf adı, boşluksuz); serbest metin satırı tip sayılmaz.
var traceChainExLineRe = regexp.MustCompile(`^([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)+)(?::\s*(.*))?$`)

// prependUnique — SAF: id'yi başa koyar, listedeki kopyasını atar.
func prependUnique(id string, ids []string) []string {
	if id == "" {
		return ids
	}
	out := make([]string, 0, len(ids)+1)
	out = append(out, id)
	for _, x := range ids {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}
