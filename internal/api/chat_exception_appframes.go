package api

// chat_exception_appframes.go — v0.10.1401 (operatör-bildirimli, prod /ai
// çekmece kademesi): exception açıklamasından sonra "servisteki hangi kod
// satırından geliyor" sorusu kütüphane frame'iyle (org.jboss…
// WrapperDataSource.java:275) cevaplandı.
//
// Explain girdisi artık HAM stack'ten seçilmiş "UYGULAMA KODU" bloğunu
// taşıyor (anomaly/exception_app_frames.go). Çekmece takibi aynı bloğu iki
// sunucu sinyaliyle zenginleştirir — ikisi de explain'de yok, çünkü anomaly
// paketi DevOps ayarını bilmez ve arka plan açıklayıcısı ağa çıkmamalı:
//
//   - operatörün DevOps uygulama önekleri (AppPrefixes — depo eşleşmesinin
//     sinyali): RankFrames sözleşmesiyle kütüphane tablosunu ezer ve önce gelir;
//   - frame → depo linki: /api/devops/stack-frames ucunun AYNI çözücüsü
//     (devops.ResolveFrameLinks; pin okuması pinReadDecision ile fail-CLOSED).
//
// Blok User'da YERİNDE değiştirilir (in.AppFramesBlock alt-dize sözleşmesi);
// DevOps yoksa ya da blok yoksa User bayt bayt aynıdır. Link çözümü kısa bir
// alt-tavanla çitlenir: takip cevabı DevOps'u beklemez, link gelmezse blok
// linksiz kalır.

import (
	"context"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/stackparse"
)

// appFrameLinkTimeout — takip başına link çözümünün tavanı (çekmece kanıtının
// 12 s'lik toplam tavanının içinde). Depo ağacı devops paketinde 10 dk
// cache'li; sıcak yolda milisaniyeler.
const appFrameLinkTimeout = 4 * time.Second

// drawerExceptionUser — çekmece kanıtının exception User'ı: UYGULAMA KODU
// bloğu operatörün önekleri + depo linkleriyle yeniden basılmış hâli.
func (s *Server) drawerExceptionUser(ctx context.Context, g *chstore.ExceptionGroup, in anomaly.ExceptionExplainInput) string {
	if in.AppFramesBlock == "" || g == nil || s.devops == nil {
		return in.User
	}
	sum := anomaly.AppFramesFor(in.Stack, s.devops.Snapshot().AppPrefixes)
	links := s.appFrameLinks(ctx, in.CodeService(g.Service), in.StackVersion, sum.App)
	return replaceAppFramesBlock(in.User, in.AppFramesBlock, anomaly.RenderAppFramesBlock(sum, links))
}

// appFrameLinks — uygulama çerçevelerinin depo linkleri (frames ile hizalı)
// ya da nil (DevOps yapılandırılmamış / çerçeve yok / çözüm başarısız).
func (s *Server) appFrameLinks(ctx context.Context, service, version string, frames []stackparse.Frame) []string {
	if len(frames) == 0 || s.devops == nil || !s.devops.Configured() {
		return nil
	}
	var pin devops.PinRead
	if s.store != nil {
		md, err := s.store.GetServiceMetadataStrict(ctx, service)
		mdRepo := ""
		if md != nil {
			mdRepo = md.Repository
		}
		pin.Repo, pin.Abort = pinReadDecision(mdRepo, md != nil, err)
	}
	lctx, cancel := context.WithTimeout(ctx, appFrameLinkTimeout)
	defer cancel()
	return frameLinkURLs(s.devops.ResolveFrameLinks(lctx, service, pin, frames, version), len(frames))
}

// frameLinkURLs — SAF: çözüm → frames ile hizalı URL dilimi; hiç URL yoksa
// nil (blok "kod:" satırı basmaz).
func frameLinkURLs(fl devops.FrameLinks, n int) []string {
	if !fl.Configured {
		return nil
	}
	out := make([]string, n)
	hit := false
	for i := 0; i < n && i < len(fl.Links); i++ {
		out[i] = fl.Links[i].URL
		hit = hit || out[i] != ""
	}
	if !hit {
		return nil
	}
	return out
}

// replaceAppFramesBlock — SAF: User'daki eski bloğu yenisiyle TEK kez
// değiştirir. Eski blok boşsa ya da User'da yoksa User aynen.
func replaceAppFramesBlock(user, old, blk string) string {
	if old == "" || blk == "" || !strings.Contains(user, old) {
		return user
	}
	return strings.Replace(user, old, blk, 1)
}
