package stackparse

// appframes.go — v0.10.1401: "servisin hangi kod satırı?" sorusunun SAF
// cevabı — temsilî stack'ten ilk UYGULAMA çerçeveleri.
//
// Operatör-bildirimli (prod /ai, çekmece kademesi, yerel küçük model):
// exception açıklamasından sonra "servisteki hangi kod satırından geliyor"
// sorusu kütüphane frame'leriyle (org.jboss… WrapperDataSource.java:275,
// WrappedConnection.java:2002) cevaplandı. Modelin önündeki stack 1800
// rune'luk prompt kopyasıydı: derin bir JBoss/IronJacamar + Spring + Feign
// stack'inde o kesim YALNIZ dış exception'ın kütüphane frame'lerini taşır,
// uygulama frame'i ve kök neden ("Caused by") bölümü kesimin altında kalır.
// Model gördüğünü söyledi.
//
// Karar (sunucu toplar, model anlatır): uygulama çerçevesini sunucu HAM
// stack'ten seçer, prompt'a etiketli blok olarak girer (anomaly
// RenderAppFramesBlock). Bu dosya yalnız seçimdir — ağ yok, prompt metni yok.
//
// # Neden IsAppClass'a dokunulmadı
//
// frameworkPrefixes "Kodu da incele"nin ve frame linklerinin aday kümesidir
// (spec'ten birebir); genişletmek o yolların sırasını bir hata düzeltmesinin
// içinde sessizce değiştirirdi. Buradaki tablo onun ÜST kümesidir (kuruluş
// gereği: önce frameworkPrefixes, sonra ekler), yani IsAppClass=false olan
// hiçbir sınıf burada uygulama sayılmaz.

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// extraLibraryPrefixes — frameworkPrefixes'e EK kütüphane/çerçeve önekleri.
// Tablo-güdümlü: yeni bir kütüphane = bir satır. Önek eşleşmesidir
// ("içeriyor" değil) ve nokta ile biter — "feign." feignclient.example.*'ı
// yakalamaz. JVM: JDK iç paketleri, uygulama sunucusu, ORM/HTTP/serileştirme,
// gözlemlenebilirlik ajanları, dil çalışma zamanları, JDBC sürücüleri/havuzları.
// .NET: BCL ve yaygın kütüphaneler (Java sınıfı büyük harfle başlamaz, yani
// "System." / "Microsoft." bir JVM paketini yanlışlıkla yakalamaz).
var extraLibraryPrefixes = []string{
	// JVM — JDK / uygulama sunucusu
	"com.sun.", "org.wildfly.", "org.xnio.", "org.picketbox.", "org.infinispan.",
	"org.jgroups.", "com.arjuna.", "org.glassfish.", "org.eclipse.", "org.omg.",
	"com.ibm.", "weblogic.",
	// JVM — çerçeve / ORM / HTTP / serileştirme / dayanıklılık
	"org.hibernate.", "feign.", "io.netty.", "okhttp3.", "okio.", "com.fasterxml.",
	"io.github.resilience4j.", "com.netflix.", "org.aspectj.", "net.bytebuddy.",
	"org.mybatis.", "org.quartz.", "io.quarkus.", "io.vertx.", "io.smallrye.",
	"io.grpc.", "com.google.", "io.micrometer.", "io.opentelemetry.", "io.reactivex.",
	"reactor.", "org.reactivestreams.", "org.slf4j.", "ch.qos.logback.", "com.zaxxer.",
	"com.mchange.", "org.keycloak.", "redis.clients.", "io.lettuce.", "com.mongodb.",
	"org.elasticsearch.", "co.elastic.",
	// JVM — dil çalışma zamanları
	"kotlin.", "kotlinx.", "scala.", "akka.", "groovy.", "org.codehaus.groovy.",
	// JVM — JDBC sürücüleri
	"oracle.jdbc.", "oracle.ucp.", "oracle.net.", "com.mysql.", "org.postgresql.",
	"com.microsoft.sqlserver.", "org.h2.", "org.mariadb.",
	// .NET — BCL + yaygın kütüphaneler
	"System.", "Microsoft.", "Npgsql.", "Oracle.ManagedDataAccess.", "Newtonsoft.",
	"Castle.", "Polly.", "StackExchange.", "Serilog.", "NLog.", "Grpc.", "Dapper.",
	"Swashbuckle.", "Hangfire.", "MassTransit.", "MediatR.", "AutoMapper.",
}

// IsLibraryClass — sınıf JDK / uygulama sunucusu / çerçeve / kütüphane mi?
// frameworkPrefixes ∪ extraLibraryPrefixes; önek eşleşmesi. Boş sınıf
// kütüphane sayılır (konumlandırılamaz). Saf; tablo-testli.
func IsLibraryClass(class string) bool {
	if class == "" {
		return true
	}
	for _, p := range frameworkPrefixes {
		if strings.HasPrefix(class, p) {
			return true
		}
	}
	for _, p := range extraLibraryPrefixes {
		if strings.HasPrefix(class, p) {
			return true
		}
	}
	return false
}

// isGeneratedClass — çalışma zamanının ürettiği vekil sınıflar (Spring CGLIB
// "$$SpringCGLIB$$", JDK "$Proxy12", lambda gövdesi "$$Lambda"). Kaynakta
// karşılığı yoktur; gerçek gövde bir sonraki frame'dir.
func isGeneratedClass(class string) bool {
	if strings.Contains(class, "$$") {
		return true
	}
	if i := strings.LastIndex(class, "."); i >= 0 {
		class = class[i+1:]
	}
	return strings.HasPrefix(class, "$Proxy")
}

// ── .NET ──────────────────────────────────────────────────────────────────

// dotnetInnerEnd — .NET'in iç exception sınırı. ToString() iç (kök neden)
// exception'ın frame'lerini ÖNCE basar, bu satırdan sonra dıştakini.
const dotnetInnerEnd = "--- End of inner exception stack trace ---"

var (
	// "   at Ns.Class.Method(Int32 id) in C:\src\File.cs:line 42" — " in …"
	// kısmı pdb yoksa hiç gelmez.
	dotnetAtRe = regexp.MustCompile(`^\s*at\s+([^\s(]+)\(([^()]*)\)(?:\s+in\s+(.+?):line\s+(\d+))?\s*$`)
	// .NET'e özgü kaynak eki — Java stack'inde asla geçmez.
	dotnetLineRe = regexp.MustCompile(`(?m)\)\s+in\s+.+:line\s+\d+\s*$`)
	// Java kaynak parantezi — varsa JVM stack'idir.
	javaSourceRe = regexp.MustCompile(`\((?:[\w$]+\.(?:java|kt|scala|groovy)(?::\d+)?|Unknown Source|Native Method|<generated>)\)`)
	// pdb'siz .NET stack'inin tek işareti: BCL frame'i.
	dotnetBCLAtRe = regexp.MustCompile(`(?m)^\s*at\s+(?:System|Microsoft)\.`)
	// async durum makinesi: "Ns.Class.<PlaceAsync>d__4" + MoveNext.
	dotnetAsyncRe = regexp.MustCompile(`^(.+)\.<([^>]+)>d__\d+$`)
)

// looksDotnet — stack .NET biçiminde mi? Saf. Kesin işaretler önce (iç
// exception sınırı, ":line N"); JVM kaynak parantezi varsa JVM; yoksa BCL
// frame'i .NET'tir.
func looksDotnet(stack string) bool {
	if strings.Contains(stack, dotnetInnerEnd) || dotnetLineRe.MatchString(stack) {
		return true
	}
	if javaSourceRe.MatchString(stack) {
		return false
	}
	return dotnetBCLAtRe.MatchString(stack)
}

// ParseDotnet — .NET stack trace'ini frame'lere çevirir. Segment JVM ile
// AYNI anlamdadır (0 = en dış, büyük = kök nedene yakın): .NET iç exception'ı
// önce bastığı için sayaç sınır sayısından başlar ve her sınırda azalır.
// "--- End of stack trace from previous location ---" (async yeniden
// fırlatma) bir sebep sınırı DEĞİLDİR, tanınmaz satır olarak atlanır.
func ParseDotnet(stack string) []Frame {
	if strings.TrimSpace(stack) == "" {
		return nil
	}
	seg := strings.Count(stack, dotnetInnerEnd)
	var out []Frame
	for _, line := range strings.Split(stack, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.Contains(line, dotnetInnerEnd) {
			if seg > 0 {
				seg--
			}
			continue
		}
		m := dotnetAtRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ref := m[1]
		dot := strings.LastIndex(ref, ".")
		if dot <= 0 || dot == len(ref)-1 {
			continue
		}
		f := Frame{Class: ref[:dot], Method: ref[dot+1:], Segment: seg}
		if am := dotnetAsyncRe.FindStringSubmatch(f.Class); am != nil && f.Method == "MoveNext" {
			f.Class, f.Method = am[1], am[2]
		}
		if m[3] != "" {
			p := m[3]
			if i := strings.LastIndexAny(p, `/\`); i >= 0 {
				p = p[i+1:]
			}
			if n, err := strconv.Atoi(m[4]); err == nil && n > 0 && p != "" {
				f.File, f.Line = p, n
			}
		}
		f.IsApp = IsAppClass(f.Class)
		out = append(out, f)
	}
	return out
}

// ParseAny — biçimi algılayıp .NET ya da JVM çözümleyicisine yönlendirir.
func ParseAny(stack string) []Frame {
	if looksDotnet(stack) {
		return ParseDotnet(stack)
	}
	return ParseJava(stack)
}

// ── Seçim ─────────────────────────────────────────────────────────────────

// AppFrameSummary — temsilî stack'in "servisin kodu nerede?" özeti.
type AppFrameSummary struct {
	// App — en çok n uygulama çerçevesi: operatörün uygulama önekleri önce
	// (verildiyse), sonra kök neden ("Caused by") bölümünden dışa, bölüm
	// içinde metin sırası (fırlatma noktası → çağıranları).
	App []Frame
	// Thrown — kök neden bölümünün EN ÜST frame'i: hatanın fırladığı yer.
	// Kütüphaneyse yalnız bağlamdır. HasThrown=false → çözümlenen frame yok.
	Thrown    Frame
	HasThrown bool
	// ThrownApp — Thrown bir uygulama çerçevesi mi (IsAppFrame, aynı önekler).
	ThrownApp bool
	// RootHasApp — kök neden bölümünde en az bir uygulama çerçevesi var mı.
	// false + len(App)>0 → hata kütüphane içinde doğdu, App[0] en yakın
	// uygulama çağıranı (dış bölümden).
	RootHasApp bool
	// Frames — çözümlenen toplam frame (blok "N frame'in tamamı kütüphane"
	// derken).
	Frames int
}

// IsAppFrame — frame servisin kendi kodu mu? Operatörün uygulama öneki
// (DevOps AppPrefixes — depo eşleşmesinin sinyali) kütüphane tablosunu EZER
// (RankFrames sözleşmesi); üretilmiş vekil sınıflar asla uygulama değildir.
func IsAppFrame(f Frame, appPrefixes []string) bool {
	if isGeneratedClass(f.Class) {
		return false
	}
	if HasAppPrefix(f.Class, appPrefixes) {
		return true
	}
	return !IsLibraryClass(f.Class)
}

// SummarizeAppFrames — HAM stack → ilk uygulama çerçeveleri (≤n). Saf.
//
// Sıra: (1) appPrefixes'ten biriyle başlayan önce (verildiyse; boşsa tek
// kademe), (2) derin segment önce — kök neden en derin "Caused by"
// bölümündedir (AppFrames, v0.9.1235), (3) metin sırası. Satırsız uygulama
// frame'i ELENMEZ (pdb'siz .NET, "Unknown Source"): servisin hangi
// metodundan geçtiği satır olmadan da cevaptır; blok "satır bilgisi yok" der.
func SummarizeAppFrames(stack string, n int, appPrefixes []string) AppFrameSummary {
	frames := ParseAny(stack)
	var out AppFrameSummary
	out.Frames = len(frames)
	if len(frames) == 0 || n <= 0 {
		return out
	}
	root := 0
	for _, f := range frames {
		if f.Segment > root {
			root = f.Segment
		}
	}
	type cand struct {
		f        Frame
		tier, ix int
	}
	var cs []cand
	for i, f := range frames {
		app := IsAppFrame(f, appPrefixes)
		if f.Segment == root && !out.HasThrown {
			out.Thrown, out.HasThrown, out.ThrownApp = f, true, app
		}
		if !app {
			continue
		}
		if f.Segment == root {
			out.RootHasApp = true
		}
		tier := 1
		if HasAppPrefix(f.Class, appPrefixes) {
			tier = 0
		}
		f.Tier = tier
		cs = append(cs, cand{f: f, tier: tier, ix: i})
	}
	sort.SliceStable(cs, func(a, b int) bool {
		if cs[a].tier != cs[b].tier {
			return cs[a].tier < cs[b].tier
		}
		if cs[a].f.Segment != cs[b].f.Segment {
			return cs[a].f.Segment > cs[b].f.Segment
		}
		return cs[a].ix < cs[b].ix
	})
	seen := make(map[string]bool, n)
	for _, c := range cs {
		// Özyinelemede aynı satır art arda gelir; tek kez say.
		k := c.f.Class + "\x00" + c.f.Method + "\x00" + c.f.File + "\x00" + strconv.Itoa(c.f.Line)
		if seen[k] {
			continue
		}
		seen[k] = true
		out.App = append(out.App, c.f)
		if len(out.App) >= n {
			break
		}
	}
	return out
}

// Short — "pkg.Class.method (File.java:N)"; satırsızsa "(satır bilgisi yok)".
// Prompt bloğunun frame yazımı.
func (f Frame) Short() string {
	loc := "satır bilgisi yok"
	if f.File != "" && f.Line > 0 {
		loc = f.File + ":" + strconv.Itoa(f.Line)
	} else if f.File != "" {
		loc = f.File + ", satır bilgisi yok"
	}
	return f.Class + "." + f.Method + " (" + loc + ")"
}
