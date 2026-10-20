package anomaly

// exception_app_frames.go — v0.10.1401: exception kanıtının "UYGULAMA KODU"
// bloğu (operatör-bildirimli: "servisteki hangi kod satırından geliyor"
// sorusu kütüphane frame'iyle cevaplandı — org.jboss… WrapperDataSource).
//
// Kök neden: prompt'taki temsilî stack 1800 rune'luk bir KOPYADIR
// (pickExceptionStack). Derin JBoss/IronJacamar + Spring + Feign stack'inde o
// kesim yalnız dış exception'ın kütüphane frame'lerini taşır; uygulama
// frame'i ve kök neden bölümü kesimin altında kalır. Explain'in kapanış
// cümlesi "ilk uygulama-frame'ine dayandır" diyordu ama model o frame'i hiç
// görmüyordu; çekmece takibi de aynı User'ı (≤6000 rune) aldığı için aynı
// körlükle cevapladı.
//
// Karar (sunucu toplar, model anlatır): uygulama çerçeveleri HAM stack'ten
// (in.Stack) stackparse.SummarizeAppFrames ile seçilir ve etiketli blok
// olarak stack'in ÖNÜNE basılır — kırpmadan ve çekmece bütçesinden etkilenmez.
// Stack yoksa blok "" ve User bayt bayt eski.

import (
	"strconv"
	"strings"

	"github.com/cilcenk/coremetry/internal/stackparse"
)

// AppFramesLimit — blokta adlandırılan en çok uygulama çerçevesi (ilk frame
// + iki çağıran). "Kodu da incele"nin pencere tavanıyla aynı mertebe.
const AppFramesLimit = 3

// appFramesHeader — bloğun sabit başlığı; çekmece prompt kuralı
// (systemDrawerChat) bloğu bu adla anar.
const appFramesHeader = "UYGULAMA KODU"

// AppFramesFor — HAM stack → özet. appPrefixes = operatörün DevOps uygulama
// önekleri (yoksa nil). Saf.
func AppFramesFor(stack string, appPrefixes []string) stackparse.AppFrameSummary {
	return stackparse.SummarizeAppFrames(stack, AppFramesLimit, appPrefixes)
}

// RenderAppFramesBlock — "\n\n" önekli UYGULAMA KODU bloğu ya da "" (stack
// yok / hiç frame çözümlenemedi). links[i] = sum.App[i]'nin depo bağlantısı
// ("" = yok; nil geçilebilir). Saf; tablo-testli.
func RenderAppFramesBlock(sum stackparse.AppFrameSummary, links []string) string {
	if sum.Frames == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n")
	if len(sum.App) == 0 {
		b.WriteString(appFramesHeader + " — stack'te uygulama çerçevesi BULUNAMADI: çözümlenen " +
			strconv.Itoa(sum.Frames) + " frame'in tamamı JDK / uygulama sunucusu / çerçeve / kütüphane frame'i. " +
			"Hata havuz / sürücü / çerçeve katmanında doğmuş olabilir ya da stack kırpılmıştır; servisin hangi kod satırı olduğu bu stack'ten söylenemez.")
		if sum.HasThrown {
			b.WriteString("\nHatanın fırladığı kütüphane çerçevesi (yalnız bağlam, servisin kodu DEĞİL): " + sum.Thrown.Short())
		}
		return b.String()
	}
	b.WriteString(appFramesHeader + " — sunucunun HAM stack'ten seçtiği ilk uygulama çerçeveleri (kök neden bölümü önce; JDK / uygulama sunucusu / çerçeve / kütüphane frame'leri atlandı):")
	if !sum.RootHasApp {
		b.WriteString("\nKök neden bölümünde uygulama çerçevesi YOK — hata kütüphane içinde doğdu; aşağıdaki ilk satır en yakın uygulama ÇAĞIRANIDIR.")
	}
	for i, f := range sum.App {
		b.WriteString("\n" + strconv.Itoa(i+1) + ". ")
		switch {
		case i == 0 && sum.RootHasApp:
			b.WriteString(f.Short() + " — hatanın servis kodundaki noktası")
		case i == 0:
			b.WriteString(f.Short() + " — en yakın uygulama çağıranı")
		case f.Segment == sum.App[i-1].Segment:
			b.WriteString("← çağıran: " + f.Short())
		default:
			b.WriteString("← dış exception bölümünden: " + f.Short())
		}
		if i < len(links) && strings.TrimSpace(links[i]) != "" {
			b.WriteString("\n   kod: " + strings.TrimSpace(links[i]))
		}
	}
	if sum.HasThrown && !sum.ThrownApp {
		b.WriteString("\nHatanın fırladığı kütüphane çerçevesi (yalnız bağlam, servisin kodu DEĞİL): " + sum.Thrown.Short())
	}
	b.WriteString("\n\"Hangi kod satırı / nerede patlıyor\" sorusunun cevabı yukarıdaki uygulama çerçeveleridir; stack'teki kütüphane frame'leri (java., org.jboss., org.springframework., feign. …) yalnız bağlamdır.")
	return b.String()
}
