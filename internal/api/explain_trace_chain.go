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
// trace'in kanıtı bayt-bayt aynı). Yardımcılar (peerOf, spanFailure,
// drillCallLabel, fmtDrillMs, drillKindRank) v0.10.1379'dan beri
// internal/tracechain'de — chat_trace_drill.go ince sarmalayıcı tutar; tek tanım.

import (
	"regexp"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/stackparse"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

// v0.10.1379 — saf çekirdek (kaynak kuralı, halka satırı, Block, exception
// satırı) internal/tracechain'e taşındı: exception açıklaması (anomaly) da
// aynı zinciri basar ve anomaly api'yi import edemez. Buradaki adlar takma
// ad / ince sarmalayıcıdır — çıktı bayt bayt aynı (parite pini
// internal/tracechain/testdata/parity_v0.10.1379.json; bu paketin testleri
// değişmeden yeşil).
const (
	traceChainBlockMaxRunes   = tracechain.BlockMaxRunes
	traceChainBaselineTimeout = 2 * time.Second // kaynak operasyonun 24 sa p50'si
)

// traceChainHeader — bloğun ilk satırı (testler ve istem bu adı anar).
const traceChainHeader = tracechain.Header

// traceErrorChain / traceChainException — tracechain tipleri.
type (
	traceErrorChain     = tracechain.Chain
	traceChainException = tracechain.Exception
)

// traceErrorChainOf — SAF: span'ler → hata zinciri (tracechain.Of).
func traceErrorChainOf(spans []chstore.SpanRow) traceErrorChain { return tracechain.Of(spans) }

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

// prependUnique — SAF: id'yi başa koyar, listedeki kopyasını atar (tracechain).
func prependUnique(id string, ids []string) []string { return tracechain.PrependUnique(id, ids) }
