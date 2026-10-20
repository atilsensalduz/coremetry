package anomaly

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// exception_app_frames_test.go — v0.10.1401, operatör-bildirimli (prod /ai,
// çekmece kademesi): "servisteki hangi kod satırından geliyor" sorusuna cevap
// kütüphane çerçevesi verdi (org.jboss… WrapperDataSource.java:275). Prompt'un
// stack'i 1800 rune'luk kopyaydı; derin JBoss stack'inde uygulama frame'i
// kesimin altında kalıyordu. Artık sunucu HAM stack'ten ilk uygulama
// çerçevelerini seçip etiketli "UYGULAMA KODU" bloğu basıyor.
// Adlar SENTETİK (com.example.orders, orders-api).

// deepOrdersStack — 1800 rune'u kütüphane frame'leriyle dolduran tek bölümlü
// derin stack; uygulama frame'leri ortada.
func deepOrdersStack() string {
	var b strings.Builder
	b.WriteString("java.sql.SQLException: IJ031070: Transaction cannot proceed: STATUS_MARKED_ROLLBACK\n")
	b.WriteString("\tat org.jboss.jca.adapters.jdbc.WrapperDataSource.checkTransactionActive(WrapperDataSource.java:275)\n")
	b.WriteString("\tat org.jboss.jca.adapters.jdbc.WrappedConnection.checkTransactionActive(WrappedConnection.java:2002)\n")
	for i := 0; i < 30; i++ {
		b.WriteString("\tat org.springframework.orm.jpa.JpaTransactionManager.doBegin(JpaTransactionManager.java:466)\n")
	}
	b.WriteString("\tat deployment.orders-api.war//com.example.orders.repo.OrderRepository.save(OrderRepository.java:57)\n")
	b.WriteString("\tat deployment.orders-api.war//com.example.orders.service.OrderService.place(OrderService.java:88)\n")
	b.WriteString("\tat feign.SynchronousMethodHandler.invoke(SynchronousMethodHandler.java:99)\n")
	b.WriteString("\tat io.undertow.servlet.handlers.ServletHandler.handleRequest(ServletHandler.java:74)\n")
	return b.String()
}

const ordersLibraryOnlyStack = "java.sql.SQLException: IJ031070: Transaction cannot proceed\n" +
	"\tat org.jboss.jca.adapters.jdbc.WrapperDataSource.checkTransactionActive(WrapperDataSource.java:275)\n" +
	"\tat org.jboss.jca.adapters.jdbc.WrappedConnection.checkTransactionActive(WrappedConnection.java:2002)\n" +
	"\tat java.base/java.lang.Thread.run(Thread.java:840)\n"

const ordersLibraryRootStack = "org.springframework.transaction.CannotCreateTransactionException: Could not open connection\n" +
	"\tat org.springframework.orm.jpa.JpaTransactionManager.doBegin(JpaTransactionManager.java:466)\n" +
	"\tat deployment.orders-api.war//com.example.orders.service.OrderService.place(OrderService.java:88)\n" +
	"Caused by: java.sql.SQLException: IJ000453: Unable to get managed connection\n" +
	"\tat org.jboss.jca.adapters.jdbc.WrapperDataSource.getConnection(WrapperDataSource.java:159)\n" +
	"\t... 30 more\n"

func TestRenderAppFramesBlock(t *testing.T) {
	tests := []struct {
		name    string
		stack   string
		links   []string
		want    []string // sırayla geçmeli
		wantNot []string
	}{
		{
			name:  "derin stack — ilk uygulama frame'i + çağıran, kütüphane yalnız bağlam",
			stack: deepOrdersStack(),
			want: []string{
				"UYGULAMA KODU — sunucunun HAM stack'ten seçtiği ilk uygulama çerçeveleri",
				"1. com.example.orders.repo.OrderRepository.save (OrderRepository.java:57) — hatanın servis kodundaki noktası",
				"2. ← çağıran: com.example.orders.service.OrderService.place (OrderService.java:88)",
				"Hatanın fırladığı kütüphane çerçevesi (yalnız bağlam, servisin kodu DEĞİL): org.jboss.jca.adapters.jdbc.WrapperDataSource.checkTransactionActive (WrapperDataSource.java:275)",
				"\"Hangi kod satırı / nerede patlıyor\" sorusunun cevabı yukarıdaki uygulama çerçeveleridir",
			},
			wantNot: []string{"kod: ", "BULUNAMADI", "Kök neden bölümünde uygulama çerçevesi YOK"},
		},
		{
			name:  "depo linki çerçevenin altına",
			stack: deepOrdersStack(),
			links: []string{"https://devops.example.test/orders-api?path=/src/OrderRepository.java&line=57", ""},
			want: []string{
				"1. com.example.orders.repo.OrderRepository.save (OrderRepository.java:57)",
				"\n   kod: https://devops.example.test/orders-api?path=/src/OrderRepository.java&line=57\n2. ← çağıran:",
			},
		},
		{
			name:  "yalnız kütüphane — dürüst 'bulunamadı'",
			stack: ordersLibraryOnlyStack,
			want: []string{
				"UYGULAMA KODU — stack'te uygulama çerçevesi BULUNAMADI: çözümlenen 3 frame'in tamamı",
				"servisin hangi kod satırı olduğu bu stack'ten söylenemez",
				"Hatanın fırladığı kütüphane çerçevesi (yalnız bağlam, servisin kodu DEĞİL): org.jboss.jca.adapters.jdbc.WrapperDataSource.checkTransactionActive (WrapperDataSource.java:275)",
			},
			wantNot: []string{"1. "},
		},
		{
			name:  "kök neden kütüphanede — en yakın uygulama çağıranı",
			stack: ordersLibraryRootStack,
			want: []string{
				"Kök neden bölümünde uygulama çerçevesi YOK — hata kütüphane içinde doğdu",
				"1. com.example.orders.service.OrderService.place (OrderService.java:88) — en yakın uygulama çağıranı",
				"Hatanın fırladığı kütüphane çerçevesi (yalnız bağlam, servisin kodu DEĞİL): org.jboss.jca.adapters.jdbc.WrapperDataSource.getConnection (WrapperDataSource.java:159)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderAppFramesBlock(AppFramesFor(tt.stack, nil), tt.links)
			if !strings.HasPrefix(got, "\n\n") {
				t.Fatalf("blok \"\\n\\n\" önekli olmalı: %q", got)
			}
			at := 0
			for _, w := range tt.want {
				i := strings.Index(got[at:], w)
				if i < 0 {
					t.Fatalf("eksik ya da sıra dışı %q\nblok:\n%s", w, got)
				}
				at += i + len(w)
			}
			for _, w := range tt.wantNot {
				if strings.Contains(got, w) {
					t.Errorf("olmamalı %q\nblok:\n%s", w, got)
				}
			}
		})
	}
}

// TestRenderAppFramesBlockEmptyStack — stack yoksa blok "" (User bayt bayt eski).
func TestRenderAppFramesBlockEmptyStack(t *testing.T) {
	for _, st := range []string{"", "   ", "java.lang.Error: frame satırı yok"} {
		if got := RenderAppFramesBlock(AppFramesFor(st, nil), nil); got != "" {
			t.Errorf("stack %q → blok %q, istenen \"\"", st, got)
		}
	}
}

// TestExceptionPromptAppFramesBeforeTruncatedStack — kök nedenin pini: prompt
// kopyası (pickExceptionStack, 1800 rune) uygulama frame'i taşımıyor; blok
// HAM stack'ten geliyor ve User'da stack'in ÖNÜNDE duruyor (çekmecenin 6000
// rune'luk kanıt kırpmasında da korunur).
func TestExceptionPromptAppFramesBeforeTruncatedStack(t *testing.T) {
	raw := deepOrdersStack()
	forPrompt, gotRaw, _, _ := pickExceptionStack([]string{raw}, "", "")
	if gotRaw != raw {
		t.Fatal("ham stack taşınmalı")
	}
	if strings.Contains(forPrompt, "com.example.orders") {
		t.Fatal("fikstür bozuk: prompt kopyasında uygulama frame'i var — regresyon senaryosu değil")
	}
	blk := RenderAppFramesBlock(AppFramesFor(raw, nil), nil)
	g := &chstore.ExceptionGroup{Type: "java.sql.SQLException", Message: "IJ031070", Service: "orders-api"}
	user := assembleExceptionPromptChain(g, nil, "", blk, forPrompt, "", "", "", "")
	bi, si := strings.Index(user, "UYGULAMA KODU"), strings.Index(user, "Temsilî STACKTRACE")
	if bi < 0 || si < 0 || bi > si {
		t.Fatalf("UYGULAMA KODU bloğu stack'in önünde olmalı (blok=%d stack=%d)", bi, si)
	}
	if !strings.Contains(user, "com.example.orders.repo.OrderRepository.save (OrderRepository.java:57)") {
		t.Fatal("User ilk uygulama frame'ini adlandırmıyor")
	}
}

// TestDrawerPromptNamesAppFramesBlock — çekmece kuralı bloğu ADIYLA anar;
// başlık değişirse kural boşa düşmesin.
func TestDrawerPromptNamesAppFramesBlock(t *testing.T) {
	p := copilot.SystemPromptDrawerChat()
	for _, w := range []string{`"` + appFramesHeader + `"`, "hangi kod satırı", "BULUNAMADI"} {
		if !strings.Contains(p, w) {
			t.Errorf("SystemPromptDrawerChat %q içermiyor", w)
		}
	}
}
