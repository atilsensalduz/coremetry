package api

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/devops"
)

// chat_exception_appframes_test.go — v0.10.1401, operatör-bildirimli (prod
// /ai, çekmece kademesi "Çekmece", yerel küçük model): exception
// açıklamasından sonra "servisteki hangi kod satırından geliyor" sorusuna
// cevap kütüphane çerçevesi verdi (org.jboss… WrapperDataSource.java:275).
// Çekmece kanıtı artık "UYGULAMA KODU" bloğunu taşıyor; DevOps uygulama
// önekleri ve depo linkleri varsa blok onlarla YERİNDE yeniden basılıyor.
// Adlar SENTETİK (com.example.orders, orders-api).

const appFramesPrefixStack = "java.lang.RuntimeException: order rejected\n" +
	"\tat deployment.orders-api.war//com.example.platform.RestFilter.doFilter(RestFilter.java:12)\n" +
	"\tat deployment.orders-api.war//org.apache.example.orders.OrderHandler.handle(OrderHandler.java:57)\n" +
	"\tat io.undertow.servlet.handlers.ServletHandler.handleRequest(ServletHandler.java:74)\n"

// appFramesInput — explain girdisinin kurucusuyla AYNI montaj: blok User'ın
// alt dizesi, AppFramesBlock ayrıca taşınır.
func appFramesInput(stack string) *anomaly.ExceptionExplainInput {
	blk := anomaly.RenderAppFramesBlock(anomaly.AppFramesFor(stack, nil), nil)
	return &anomaly.ExceptionExplainInput{
		User:           "Exception GRUBU: {}" + blk + "\n\nTemsilî STACKTRACE:\n```\n" + stack + "\n```",
		AppFramesBlock: blk,
		Stack:          stack,
	}
}

func TestDrawerExceptionEvidenceAppFrames(t *testing.T) {
	tests := []struct {
		name     string
		prefixes []string // nil → s.devops yok
		want     []string // sırayla
		wantNot  []string
	}{
		{
			name: "DevOps yok — explain'in bloğu aynen",
			want: []string{
				"UYGULAMA KODU",
				"1. com.example.platform.RestFilter.doFilter (RestFilter.java:12) — hatanın servis kodundaki noktası",
			},
			// org.apache. kütüphane tablosunda: önek ayarı olmadan uygulama değil.
			wantNot: []string{"1. org.apache.example.orders.OrderHandler"},
		},
		{
			name:     "DevOps uygulama öneki — önce gelir, blok yerinde değişir",
			prefixes: []string{"org.apache.example."},
			want: []string{
				"UYGULAMA KODU",
				"1. org.apache.example.orders.OrderHandler.handle (OrderHandler.java:57)",
				"2. ← çağıran: com.example.platform.RestFilter.doFilter (RestFilter.java:12)",
				"Temsilî STACKTRACE",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installExceptionInput(t, appFramesInput(appFramesPrefixStack))
			s := &Server{}
			if tt.prefixes != nil {
				dv := devops.New()
				dv.Configure(devops.Settings{AppPrefixes: tt.prefixes}) // BaseURL yok → link çözümü yok
				s.devops = dv
			}
			ev, _ := s.drawerSubjectEvidence(context.Background(), drawerSubject{Kind: "exception", ID: exFP}, nil)
			if strings.Count(ev, "UYGULAMA KODU") != 1 {
				t.Fatalf("blok tam bir kez olmalı:\n%s", ev)
			}
			at := 0
			for _, w := range tt.want {
				i := strings.Index(ev[at:], w)
				if i < 0 {
					t.Fatalf("eksik ya da sıra dışı %q\nkanıt:\n%s", w, ev)
				}
				at += i + len(w)
			}
			for _, w := range tt.wantNot {
				if strings.Contains(ev, w) {
					t.Errorf("olmamalı %q\nkanıt:\n%s", w, ev)
				}
			}
		})
	}
}

func TestReplaceAppFramesBlock(t *testing.T) {
	tests := []struct {
		name, user, old, blk, want string
	}{
		{"yerinde tek kez", "A\n\nOLD\n\nB OLD", "\n\nOLD", "\n\nNEW", "A\n\nNEW\n\nB OLD"},
		{"eski blok yok → aynen", "A B", "", "\n\nNEW", "A B"},
		{"User'da yok → aynen", "A B", "\n\nOLD", "\n\nNEW", "A B"},
		{"yeni blok boş → aynen", "A\n\nOLD", "\n\nOLD", "", "A\n\nOLD"},
	}
	for _, tt := range tests {
		if got := replaceAppFramesBlock(tt.user, tt.old, tt.blk); got != tt.want {
			t.Errorf("%s: %q, istenen %q", tt.name, got, tt.want)
		}
	}
}

func TestFrameLinkURLs(t *testing.T) {
	tests := []struct {
		name string
		fl   devops.FrameLinks
		n    int
		want []string
	}{
		{"yapılandırılmamış → nil", devops.FrameLinks{}, 2, nil},
		{"hiç URL yok → nil", devops.FrameLinks{Configured: true, Links: []devops.FrameLink{{Reason: devops.FrameReasonNoPath}, {}}}, 2, nil},
		{"hizalı", devops.FrameLinks{Configured: true, Links: []devops.FrameLink{{URL: "u1"}, {Reason: devops.FrameReasonNoPath}}}, 2, []string{"u1", ""}},
		{"kısa çözüm dilimi — taşma yok", devops.FrameLinks{Configured: true, Links: []devops.FrameLink{{URL: "u1"}}}, 3, []string{"u1", "", ""}},
	}
	for _, tt := range tests {
		got := frameLinkURLs(tt.fl, tt.n)
		if strings.Join(got, "|") != strings.Join(tt.want, "|") || (got == nil) != (tt.want == nil) {
			t.Errorf("%s: %q, istenen %q", tt.name, got, tt.want)
		}
	}
}
