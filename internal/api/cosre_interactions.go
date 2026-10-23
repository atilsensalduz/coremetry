package api

// cosre_interactions.go — CoSRE öğrenme Faz 1: tur başına etkileşim satırı
// (chstore.CosreInteraction → cosre_interactions). Kaynak chatExchange
// (chat_exchange.go): emit akışı tool çiplerini, kanıt önizlemelerini, ilk
// görünür çıktıyı ve cevabı zaten taşıyor — tap bunları okur, hiçbir kademe
// ayrıca kayıt çağırmaz (drawer/guided/döngü aynı yoldan).
//
// Yazım ASLA sohbeti bloklamaz: sınırlı kanal + tek yazıcı gorutin, dolu
// kanalda satır DÜŞER (sayaç + seyrek log). Toplu INSERT: 64 satır ya da
// 2 s, hangisi önce.
//
// v0.10.1444 — satır turun blok zarfını (≤256 KiB) taşıyabilir; 2048'lik
// kanal 512 MiB'ı rehin tutamasın diye BAYT bütçesi (64 MiB): aşımda yalnız
// Blocks boşalır (satır yine yazılır, sayaç + seyrek log).

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	cosreQueueCap      = 2048
	cosreFlushRows     = 64
	cosreFlushEvery    = 2 * time.Second
	cosreQuestionMax   = 4 << 10
	cosreAnswerMax     = 8 << 10
	cosreToolArgsMax   = 1 << 10
	cosreToolErrMax    = 512
	cosreToolOutputMax = 4 << 10
	cosreQueueBytesCap = 64 << 20 // v0.10.1444 — kuyruktaki blok zarflarının toplam tavanı
)

type cosreInteractionSink interface {
	InsertCosreInteractions(ctx context.Context, rows []chstore.CosreInteraction) error
}

type cosreInteractionWriter struct {
	ch      chan chstore.CosreInteraction
	sink    cosreInteractionSink
	dropped atomic.Uint64
	failed  atomic.Uint64

	// v0.10.1444 — blok zarfı bayt bütçesi (kanal + yazıcı tamponu).
	bytesCap       int64
	pendingBytes   atomic.Int64
	blocksStripped atomic.Uint64
}

var (
	cosreWriterOnce sync.Once
	cosreWriter     *cosreInteractionWriter
)

func newCosreInteractionWriter(sink cosreInteractionSink) *cosreInteractionWriter {
	return &cosreInteractionWriter{ch: make(chan chstore.CosreInteraction, cosreQueueCap), sink: sink, bytesCap: cosreQueueBytesCap}
}

// stripBlocksForBudget — SAF (v0.10.1444): n baytlık zarf bütçeyi aşar mı.
func stripBlocksForBudget(pending, capBytes int64, n int) bool {
	return n > 0 && pending+int64(n) > capBytes
}

// enqueue — bloklamaz; kuyruk doluysa satır düşer. v0.10.1444 — bayt
// bütçesi aşılırsa yalnız Blocks düşer.
func (w *cosreInteractionWriter) enqueue(r chstore.CosreInteraction) bool {
	if stripBlocksForBudget(w.pendingBytes.Load(), w.bytesCap, len(r.Blocks)) {
		r.Blocks = ""
		if n := w.blocksStripped.Add(1); n == 1 || n%1000 == 0 {
			log.Printf("cosre_interactions: kuyruk bayt bütçesi dolu, %d satırın blokları düştü", n)
		}
	}
	n := int64(len(r.Blocks))
	w.pendingBytes.Add(n) // gönderimden ÖNCE: yazıcı flush'ı iadeyi önce yapamasın
	select {
	case w.ch <- r:
		return true
	default:
		w.pendingBytes.Add(-n)
		if n := w.dropped.Add(1); n == 1 || n%1000 == 0 {
			log.Printf("cosre_interactions: kuyruk dolu, %d satır düştü", n)
		}
		return false
	}
}

func (w *cosreInteractionWriter) run() {
	t := time.NewTicker(cosreFlushEvery)
	defer t.Stop()
	buf := make([]chstore.CosreInteraction, 0, cosreFlushRows)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		var held int64 // v0.10.1444 — tampon boşalınca bütçeye iade
		for i := range buf {
			held += int64(len(buf[i].Blocks))
		}
		defer w.pendingBytes.Add(-held)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := w.sink.InsertCosreInteractions(ctx, buf)
		cancel()
		if err != nil {
			if n := w.failed.Add(uint64(len(buf))); n == uint64(len(buf)) || n%1000 < uint64(len(buf)) {
				log.Printf("cosre_interactions: insert hatası (%d satır, toplam %d): %v", len(buf), n, err)
			}
		}
		buf = buf[:0]
	}
	for {
		select {
		case r := <-w.ch:
			buf = append(buf, r)
			if len(buf) >= cosreFlushRows {
				flush()
			}
		case <-t.C:
			flush()
		}
	}
}

// cosreInteractionSeam — v0.10.1280 TEST DİKİŞİ (prod'da nil): kademe
// kapsama testi gerçek copilotChat'in ürettiği satırı CH'siz yakalar
// (chat_exchange_tiers_test.go). Doluyken satır kuyruğa da gider (store varsa).
var cosreInteractionSeam func(chstore.CosreInteraction)

// enqueueCosreInteraction — süreç başına tek yazıcı (ilk turda başlar).
func (s *Server) enqueueCosreInteraction(r chstore.CosreInteraction) {
	if cosreInteractionSeam != nil {
		cosreInteractionSeam(r)
	}
	if s == nil || s.store == nil {
		return
	}
	cosreWriterOnce.Do(func() {
		cosreWriter = newCosreInteractionWriter(s.store)
		go cosreWriter.run()
	})
	cosreWriter.enqueue(r)
}

// clipBytes — SAF: bayt tavanına rune sınırında kırpar.
func clipBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// ── kanıtsız-cevap tespiti ────────────────────────────────────────────
//
// Kullanıcı geri bildirimi seyrek; "kanıtlarda belirtilmemiştir … loglara
// bakmanızı öneririm" gibi bir cevap 👎 almadan da başarısızdır (operatör
// örneği, 2026-10-15: MCA-00029 "Bu neden oluyor?" / "Nasıl düzeltirim?").
// SAF ve sezgisel: cevabın GİRİŞİNDE (ilk cevapNoEvidenceLead bayt) aynı
// cümlede bir kanıt-ismi VE bir yokluk ifadesi geçiyorsa işaretlenir.
// Kanıta dayanıp yokluğu ara cümlede anan cevap ("… log yok ama trace
// gösteriyor ki …") girişte değilse işaretlenmez.

const answerNoEvidenceLead = 400

var noEvidenceNouns = []string{
	"kanıt", "veri", "bilgi", "detay", "evidence", "data", "information",
}

var noEvidenceMarkers = []string{
	"belirtilmemiş", "yer almıyor", "yer almadığı", "yer almamakta", "bulunmuyor",
	"bulunmamakta", "bulunamadı", "mevcut değil", "sunulamamakta", "belirlenemedi",
	"belirlenemiyor", "tespit edilemedi", "yetersiz", " yok",
	"not mentioned", "not found", "not available", "not present", "no evidence",
	"insufficient", "cannot determine", "can't determine", "unable to determine",
}

func answerAdmitsNoEvidence(answer string) bool {
	lead := strings.ToLower(clipBytes(strings.TrimSpace(answer), answerNoEvidenceLead))
	if lead == "" {
		return false
	}
	for _, sent := range strings.FieldsFunc(lead, func(r rune) bool { return r == '.' || r == '\n' || r == '!' || r == '?' || r == ';' }) {
		sent = " " + sent + " "
		if !containsAny(sent, noEvidenceNouns) {
			continue
		}
		for _, m := range noEvidenceMarkers {
			if strings.Contains(sent, m) {
				if m == " yok" && !wordFollows(sent, m) {
					continue
				}
				return true
			}
		}
	}
	return false
}

func containsAny(s string, subs []string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// wordFollows — m'den sonra harf gelmiyor mu ("yok" ≠ "yoksa"/"yoktur" kabul).
func wordFollows(s, m string) bool {
	i := strings.Index(s, m)
	rest := s[i+len(m):]
	return rest == "" || strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, ",") ||
		strings.HasPrefix(rest, "tu") // "yoktur"/"yoktu"
}
