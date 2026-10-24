package api

// rootcause_bubble_mode.go — v0.10.1495 (operatör, prod Problem detayı):
// `runtime:jvm-gc:<svc>:<pod>` problemi "WHERE ERRORS CONCENTRATE — top
// attribute over the error spans" gösterdi. Ama GC problemi hatayla ilgili
// değil ve sunucu o problemde HATALI span alt kümesini değil, zaman-kaydırmalı
// kıyası koşuyordu — başlık yanlış, bölüm anlamsız.
//
// Bölüm problem TÜRÜNDEN seçilir, TEK yerde (bu SAF işlev) ve cevapla
// birlikte istemciye gider (`bubbleMode`); panel kendi tahminini yapmaz:
//
//   - errors  — hata problemi: aynı pencerede hatalı span'ler tüm span'lere
//     karşı ("hatalar nerede yoğunlaşıyor"). Eski davranış.
//   - window  — gecikme / trafik / diğer servis metrikleri: pencere, ÖNCEKİ
//     eş-boy pencereye karşı ("bu pencerede ne öne çıktı"). Hata dili YOK.
//   - none    — span attribute'larının açıklayamadığı türler: runtime (JVM GC,
//     heap, thread — tek pod'un süreci), veritabanı kapasite/sağlık, monitor,
//     self-health, servis olmayan özne. Ham spans taraması HİÇ koşmaz.

import (
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	bubbleModeErrors = "errors"
	bubbleModeWindow = "window"
	bubbleModeNone   = "none"
)

// bubbleNonePrefixes — span attribute kıyası anlamsız olan kural/problem id
// önekleri (aiops §3 aileleri).
var bubbleNonePrefixes = []string{"runtime:", "db-capacity:", "db-health:", "monitor:", "self-"}

// rootCauseBubbleMode — SAF (rootcause_bubble_mode_test.go tablo).
func rootCauseBubbleMode(p *chstore.Problem) string {
	if p == nil || p.Service == "" || (p.Kind != "" && p.Kind != chstore.ProblemKindService) {
		return bubbleModeNone
	}
	for _, id := range []string{p.ID, p.RuleID} {
		for _, pre := range bubbleNonePrefixes {
			if strings.HasPrefix(id, pre) {
				return bubbleModeNone
			}
		}
	}
	if exemplarKindForMetric(p.Metric) == chstore.ExemplarError {
		return bubbleModeErrors
	}
	return bubbleModeWindow
}

// anomalyBubbleMode — SAF: anomali çıpası. trace_op hata oranı anomalisidir.
func anomalyBubbleMode(kind string) string {
	if exemplarKindForAnomaly(kind) == chstore.ExemplarError {
		return bubbleModeErrors
	}
	return bubbleModeWindow
}
