package stackparse

import (
	"strconv"
	"strings"
	"testing"
)

// appframes_test.go — v0.10.1401, operatör-bildirimli (prod /ai, çekmece
// kademesi, yerel küçük model): exception açıklamasından sonra "servisteki
// hangi kod satırından geliyor" sorusuna cevap kütüphane çerçevesi verdi
// (org.jboss… WrapperDataSource.java:275 / WrappedConnection.java:2002).
// Prompt'taki stack 1800 rune'luk bir kopyaydı; derin JBoss/IronJacamar +
// Spring + Feign stack'inde o kesim yalnız kütüphane frame'i taşıyor.
//
// Bu dosya SAF seçimi çiviler: HAM stack → ilk uygulama çerçeveleri (≤3),
// kök neden bölümü önce, kütüphane önekleri atlanır. Adlar SENTETİK
// (com.example.orders, orders-api) — gerçek kurum/paket adı bu depoya girmez.

// libFrames — n adet kütüphane frame'i (derin bir stack'in dolgusu).
func libFrames(prefix string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("\tat " + prefix + ".Layer" + string(rune('A'+i%26)) + ".invoke(Layer.java:" + strconv.Itoa(100+i) + ")\n")
	}
	return b.String()
}

// deepJBossStack — tek bölüm: IronJacamar + Hibernate + Spring ÜSTTE (1800
// rune'u tek başına aşar), uygulama frame'leri ORTADA, Feign + Undertow altta.
var deepJBossStack = "java.sql.SQLException: IJ031070: Transaction cannot proceed: STATUS_MARKED_ROLLBACK\n" +
	"\tat org.jboss.jca.adapters.jdbc.WrapperDataSource.checkTransactionActive(WrapperDataSource.java:275)\n" +
	"\tat org.jboss.jca.adapters.jdbc.WrappedConnection.checkTransactionActive(WrappedConnection.java:2002)\n" +
	libFrames("org.hibernate.engine.jdbc.internal", 8) +
	libFrames("org.springframework.orm.jpa", 10) +
	libFrames("org.springframework.aop.framework", 6) +
	"\tat deployment.orders-api.war//com.example.orders.service.OrderService$$SpringCGLIB$$0.place(<generated>)\n" +
	"\tat deployment.orders-api.war//com.example.orders.repo.OrderRepository.save(OrderRepository.java:57)\n" +
	"\tat deployment.orders-api.war//com.example.orders.service.OrderService.place(OrderService.java:88)\n" +
	"\tat deployment.orders-api.war//com.example.orders.api.OrderResource.create(OrderResource.java:41)\n" +
	"\tat deployment.orders-api.war//com.example.orders.api.OrderResource.create(OrderResource.java:41)\n" +
	libFrames("feign", 4) +
	libFrames("io.undertow.servlet.handlers", 6)

// causedByStack — dış (wrapper) bölümde uygulama frame'i, kök neden en
// derin "Caused by" bölümünde.
const causedByStack = "jakarta.ejb.EJBException: order failed\n" +
	"\tat deployment.ORDERSAPI.war//com.example.orders.api.OrderFacade.submit(OrderFacade.java:120)\n" +
	"\tat org.jboss.as.ee.component.ManagedReference.invoke(ManagedReference.java:60)\n" +
	"Caused by: org.springframework.web.client.ResourceAccessException: I/O error\n" +
	"\tat org.springframework.web.client.RestTemplate.doExecute(RestTemplate.java:785)\n" +
	"\tat deployment.ORDERSAPI.war//com.example.orders.client.StockClient.reserve(StockClient.java:33)\n" +
	"\t... 12 more\n" +
	"Caused by: java.net.SocketTimeoutException: Read timed out\n" +
	"\tat java.base/java.net.SocketInputStream.socketRead0(Native Method)\n" +
	"\tat okhttp3.internal.http2.Http2Stream.waitForIo(Http2Stream.kt:711)\n" +
	"\tat deployment.ORDERSAPI.war//com.example.orders.client.StockHttp.read(StockHttp.java:77)\n" +
	"\t... 20 more\n"

// libraryRootStack — kök neden bölümü YALNIZ kütüphane (havuz/sürücü);
// uygulama çağıranı dış bölümde.
const libraryRootStack = "org.springframework.transaction.CannotCreateTransactionException: Could not open connection\n" +
	"\tat org.springframework.orm.jpa.JpaTransactionManager.doBegin(JpaTransactionManager.java:466)\n" +
	"\tat deployment.orders-api.war//com.example.orders.service.OrderService.place(OrderService.java:88)\n" +
	"Caused by: java.sql.SQLException: IJ000453: Unable to get managed connection\n" +
	"\tat org.jboss.jca.adapters.jdbc.WrapperDataSource.getConnection(WrapperDataSource.java:159)\n" +
	"\tat com.zaxxer.hikari.pool.HikariPool.getConnection(HikariPool.java:181)\n" +
	"\t... 30 more\n"

// libraryOnlyStack — hiç uygulama frame'i yok.
const libraryOnlyStack = "java.sql.SQLException: IJ031070: Transaction cannot proceed\n" +
	"\tat org.jboss.jca.adapters.jdbc.WrapperDataSource.checkTransactionActive(WrapperDataSource.java:275)\n" +
	"\tat org.jboss.jca.adapters.jdbc.WrappedConnection.checkTransactionActive(WrappedConnection.java:2002)\n" +
	"\tat io.undertow.servlet.handlers.ServletHandler.handleRequest(ServletHandler.java:74)\n" +
	"\tat java.base/java.lang.Thread.run(Thread.java:840)\n"

// dotnetStack — iç exception ÖNCE basılır; sınırdan sonra dış exception.
const dotnetStack = "System.InvalidOperationException: Order store unavailable\n" +
	" ---> Npgsql.NpgsqlException (0x80004005): Exception while reading from stream\n" +
	"   at Npgsql.Internal.NpgsqlReadBuffer.Ensure(Int32 count)\n" +
	"   at Example.Orders.Data.OrderRepository.<SaveAsync>d__7.MoveNext() in /src/Orders/Data/OrderRepository.cs:line 42\n" +
	"   --- End of inner exception stack trace ---\n" +
	"   at Example.Orders.Services.OrderService.PlaceAsync(OrderRequest req) in C:\\src\\Orders\\Services\\OrderService.cs:line 88\n" +
	"   at Microsoft.AspNetCore.Mvc.Infrastructure.ActionMethodExecutor.Execute(ObjectMethodExecutor executor)\n" +
	"   at System.Threading.Tasks.Task.Wait()\n"

func shorts(fs []Frame) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Short()
	}
	return out
}

func TestSummarizeAppFrames(t *testing.T) {
	tests := []struct {
		name        string
		stack       string
		prefixes    []string
		wantApp     []string
		wantThrown  string
		wantRootApp bool
	}{
		{
			name:  "derin JBoss/IronJacamar + Spring + Feign — ortadaki uygulama frame'i",
			stack: deepJBossStack,
			wantApp: []string{
				"com.example.orders.repo.OrderRepository.save (OrderRepository.java:57)",
				"com.example.orders.service.OrderService.place (OrderService.java:88)",
				"com.example.orders.api.OrderResource.create (OrderResource.java:41)",
			},
			wantThrown:  "org.jboss.jca.adapters.jdbc.WrapperDataSource.checkTransactionActive (WrapperDataSource.java:275)",
			wantRootApp: true,
		},
		{
			name:  "Caused by — kök neden bölümü önce, sonra dışa",
			stack: causedByStack,
			wantApp: []string{
				"com.example.orders.client.StockHttp.read (StockHttp.java:77)",
				"com.example.orders.client.StockClient.reserve (StockClient.java:33)",
				"com.example.orders.api.OrderFacade.submit (OrderFacade.java:120)",
			},
			wantThrown:  "java.net.SocketInputStream.socketRead0 (satır bilgisi yok)",
			wantRootApp: true,
		},
		{
			name:        "kök neden yalnız kütüphane — en yakın uygulama çağıranı dış bölümden",
			stack:       libraryRootStack,
			wantApp:     []string{"com.example.orders.service.OrderService.place (OrderService.java:88)"},
			wantThrown:  "org.jboss.jca.adapters.jdbc.WrapperDataSource.getConnection (WrapperDataSource.java:159)",
			wantRootApp: false,
		},
		{
			name:        "yalnız kütüphane frame'leri — uygulama çerçevesi yok",
			stack:       libraryOnlyStack,
			wantApp:     nil,
			wantThrown:  "org.jboss.jca.adapters.jdbc.WrapperDataSource.checkTransactionActive (WrapperDataSource.java:275)",
			wantRootApp: false,
		},
		{
			name:  ".NET — iç exception (kök neden) önce, async durum makinesi çözülür",
			stack: dotnetStack,
			wantApp: []string{
				"Example.Orders.Data.OrderRepository.SaveAsync (OrderRepository.cs:42)",
				"Example.Orders.Services.OrderService.PlaceAsync (OrderService.cs:88)",
			},
			wantThrown:  "Npgsql.Internal.NpgsqlReadBuffer.Ensure (satır bilgisi yok)",
			wantRootApp: true,
		},
		{
			name: "deployment.APP.war// öneki soyulur",
			stack: "java.lang.IllegalStateException: boom\n" +
				"\tat deployment.orders-api.war//org.springframework.util.Assert.state(Assert.java:76)\n" +
				"\tat deployment.orders-api.war//com.example.orders.service.OrderService.place(OrderService.java:88)\n",
			wantApp:     []string{"com.example.orders.service.OrderService.place (OrderService.java:88)"},
			wantThrown:  "org.springframework.util.Assert.state (Assert.java:76)",
			wantRootApp: true,
		},
		{
			name: "uygulama öneki (DevOps AppPrefixes) önce gelir ve kütüphane tablosunu ezer",
			stack: "java.lang.RuntimeException: x\n" +
				"\tat com.example.platform.RestFilter.doFilter(RestFilter.java:12)\n" +
				"\tat org.apache.example.orders.Handler.handle(Handler.java:5)\n",
			prefixes: []string{"org.apache.example."},
			wantApp: []string{
				"org.apache.example.orders.Handler.handle (Handler.java:5)",
				"com.example.platform.RestFilter.doFilter (RestFilter.java:12)",
			},
			wantThrown:  "com.example.platform.RestFilter.doFilter (RestFilter.java:12)",
			wantRootApp: true,
		},
		{
			name:  "boş stack",
			stack: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SummarizeAppFrames(tt.stack, 3, tt.prefixes)
			if g := shorts(got.App); strings.Join(g, "|") != strings.Join(tt.wantApp, "|") {
				t.Errorf("App=\n  %q\nistenen\n  %q", g, tt.wantApp)
			}
			thrown := ""
			if got.HasThrown {
				thrown = got.Thrown.Short()
			}
			if thrown != tt.wantThrown {
				t.Errorf("Thrown=%q, istenen %q", thrown, tt.wantThrown)
			}
			if got.RootHasApp != tt.wantRootApp {
				t.Errorf("RootHasApp=%v, istenen %v", got.RootHasApp, tt.wantRootApp)
			}
		})
	}
}

// TestDeepStackPromptCopyHasNoAppFrame — kök nedenin kendisi: 1800 rune'luk
// prompt kopyası derin stack'te HİÇ uygulama frame'i taşımıyor; seçim HAM
// stack'ten yapılmalı.
func TestDeepStackPromptCopyHasNoAppFrame(t *testing.T) {
	r := []rune(deepJBossStack)
	if len(r) <= 1800 {
		t.Fatalf("fikstür 1800 rune'u aşmalı, %d", len(r))
	}
	if strings.Contains(string(r[:1800]), "com.example.orders") {
		t.Fatal("fikstür bozuk: 1800'lük kesimde uygulama frame'i var — regresyon senaryosu değil")
	}
	if got := SummarizeAppFrames(deepJBossStack, 3, nil); len(got.App) == 0 {
		t.Fatal("HAM stack'te uygulama frame'i bulunamadı")
	}
}

func TestIsLibraryClass(t *testing.T) {
	tests := []struct {
		class string
		want  bool
	}{
		{"java.util.concurrent.FutureTask", true},
		{"org.jboss.jca.adapters.jdbc.WrapperDataSource", true},
		{"io.undertow.servlet.handlers.ServletHandler", true},
		{"org.wildfly.extension.undertow.Handler", true},
		{"org.springframework.web.servlet.DispatcherServlet", true},
		{"org.hibernate.engine.jdbc.internal.ResultSetReturnImpl", true},
		{"feign.SynchronousMethodHandler", true},
		{"io.netty.channel.AbstractChannel", true},
		{"okhttp3.internal.connection.RealCall", true},
		{"com.fasterxml.jackson.databind.ObjectMapper", true},
		{"io.opentelemetry.javaagent.Instrumenter", true},
		{"reactor.core.publisher.Mono", true},
		{"kotlin.coroutines.Continuation", true},
		{"scala.concurrent.Future", true},
		{"com.sun.proxy.Proxy12", true},
		{"System.Data.SqlClient.SqlConnection", true},
		{"Microsoft.AspNetCore.Mvc.ControllerBase", true},
		{"", true},
		{"com.example.orders.service.OrderService", false},
		{"Example.Orders.Services.OrderService", false},
		// önek eşleşmesi, "içeriyor" değil
		{"com.example.feign.OrderClient", false},
		{"feignclient.example.OrderClient", false},
	}
	for _, tt := range tests {
		if got := IsLibraryClass(tt.class); got != tt.want {
			t.Errorf("IsLibraryClass(%q)=%v, istenen %v", tt.class, got, tt.want)
		}
	}
}

// TestLibraryTableIsSupersetOfFramework — IsAppClass=false olan hiçbir sınıf
// burada uygulama sayılmaz ("Kodu da incele" kümesiyle çelişki yok).
func TestLibraryTableIsSupersetOfFramework(t *testing.T) {
	for _, p := range frameworkPrefixes {
		if !IsLibraryClass(p + "X") {
			t.Errorf("frameworkPrefixes öğesi %q kütüphane sayılmıyor", p)
		}
	}
}

func TestLooksDotnet(t *testing.T) {
	tests := []struct {
		name  string
		stack string
		want  bool
	}{
		{".NET :line", dotnetStack, true},
		{"JVM", causedByStack, false},
		{"pdb'siz .NET", "System.Exception: x\n   at System.Data.SqlClient.SqlConnection.Open()\n   at Example.Orders.Repo.Load(Int32 id)\n", true},
		{"JVM boş parantezsiz kaynak", "java.lang.Error\n\tat java.lang.Thread.run(Unknown Source)\n", false},
	}
	for _, tt := range tests {
		if got := looksDotnet(tt.stack); got != tt.want {
			t.Errorf("%s: looksDotnet=%v, istenen %v", tt.name, got, tt.want)
		}
	}
}
