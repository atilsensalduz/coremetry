package api

// rag_ingest_async.go — v0.10.1173 — belge yükleme ARKA PLANDA indekslenir.
//
// Operator-reported: büyük belge (xlsx envanter) yüklerken "Request timed out
// after 60s" ve belge listeye girmiyor. Kök neden: yükleme isteği tüm
// parçaları SENKRON embed ediyordu (bge-m3) ve 1163'ten beri wiki indeksi için
// ikinci kez; istemci 60 sn'de keser, istek bağlamı iptal olunca sunucudaki
// yazım da yarıda kalır.
//
// Şimdi: dosya okunur + doğrulanır + denetlenir (senkron, hızlı), indeksleme
// istekten BAĞIMSIZ bir bağlamda (ragIngestTimeout) arkada koşar; cevap 202 +
// status "indexing". Durum (indexing / failed) bu pod'un belleğinde tutulur ve
// liste cevabında "pending" olarak döner (çok pod'da yalnız yükleyen pod
// bilir — liste başka pod'a giderse belge bitince yine görünür). Wiki bilgisi
// açıkken rag_chunks parçaları embed EDİLMEZ: arama wiki indeksinden yapılır,
// ikinci embed yalnız süre ekliyordu.

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"
)

const (
	ragIngestTimeout   = 20 * time.Minute
	ragPendingFailKeep = 30 * time.Minute
)

type ragPending struct {
	DocID string `json:"docId"`
	Name  string `json:"docName"`
	State string `json:"state"` // indexing | failed
	Error string `json:"error,omitempty"`
	At    int64  `json:"at"` // unix ns
}

var ragPendingMu sync.Mutex
var ragPendingByID = map[string]ragPending{}

func setRAGPending(p ragPending) {
	ragPendingMu.Lock()
	defer ragPendingMu.Unlock()
	ragPendingByID[p.DocID] = p
}

func clearRAGPending(docID string) {
	ragPendingMu.Lock()
	defer ragPendingMu.Unlock()
	delete(ragPendingByID, docID)
}

// ragPendingList — SAF-ish: süresi geçen başarısız kayıtları atar, kalanları yeniden eskiye.
func ragPendingList(now time.Time) []ragPending {
	ragPendingMu.Lock()
	defer ragPendingMu.Unlock()
	out := []ragPending{}
	for id, p := range ragPendingByID {
		if p.State == "failed" && now.Sub(time.Unix(0, p.At)) > ragPendingFailKeep {
			delete(ragPendingByID, id)
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out
}

// ragIngestInBackground — indekslemeyi istekten bağımsız koşturur; bitince
// (hata ya da başarı) durum kaydını günceller.
func (s *Server) ragIngestInBackground(docID, name, source, sourceRef, uploadedBy, text string) {
	setRAGPending(ragPending{DocID: docID, Name: name, State: "indexing", At: time.Now().UnixNano()})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[rag] arka plan indeksleme panikledi (%s): %v", name, r)
				setRAGPending(ragPending{DocID: docID, Name: name, State: "failed", Error: "iç hata", At: time.Now().UnixNano()})
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), ragIngestTimeout)
		defer cancel()
		start := time.Now()
		n, err := s.ragIngestDocument(ctx, docID, name, source, sourceRef, uploadedBy, text)
		if err != nil {
			log.Printf("[rag] %s indekslenemedi: %v", name, err)
			setRAGPending(ragPending{DocID: docID, Name: name, State: "failed", Error: err.Error(), At: time.Now().UnixNano()})
			return
		}
		clearRAGPending(docID)
		log.Printf("[rag] %s: %d parça indekslendi (%s)", name, n, time.Since(start).Round(time.Second))
	}()
}

// ragEmbedChunks — v0.10.1173: rag_chunks parçaları embed edilsin mi. Wiki
// bilgisi açıkken arama wiki indeksinden (orada embed edilir); burada ikinci
// kez embed yalnız süre ekler.
func (s *Server) ragEmbedChunks() bool {
	if s.rag == nil || !s.rag.Ready() {
		return false
	}
	w := wikiKB()
	return w == nil || !w.Enabled()
}
