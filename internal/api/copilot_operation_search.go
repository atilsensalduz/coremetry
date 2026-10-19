package api

// copilot_operation_search.go — v0.10.1390 (operatör, CoSRE boş sohbet
// ekran görüntüsü: "filo geneli ve böyle birçok servis yazmasın, search kısmı
// çıksa yeter. Aynısı hata veren teknik operasyonlar için de geçerli").
//
//	GET /api/copilot/operation-search?q=<metin>&limit=
//
// CoSRE başlangıç kartının "hata veren teknik operasyonlar" satırındaki arama
// kutusunun sunucu araması. Seçilen ad HEMEN "<OP> operasyonu neden hata
// veriyor?" olarak gönderilir; bu yüzden kaynak, sohbetin operasyon
// ön-getirmesinin okuduğu AYNI katalog (cosre_operation_catalog: span +
// Oracle `ora:` operasyon kodları) ve dönen her ad ön-getirme belirteç
// kuralından TEK belirteç olarak geçer (opSearchRoutable) → katalogda birebir
// eşleşir, LLM'siz yönlenir (copilot_starters_test TestStarterQuestionsRoute).
//
// Neden /api/operation-names DEĞİL: yalnız span operasyonlarını bilir, Oracle
// operasyon kodlarını (oracle_error_log) bulmaz. Neden /api/oracle/operations
// DEĞİL: yalnız Oracle, ham oracle_error_log'u 7 gün tarar ve span
// operasyonlarını bulmaz. Katalog ikisini de taşır ve 10 dk'da bir hazırlanır
// (sohbet anında ham tablo taranmaz).
//
// Picker kuralı: istemci kataloğu ÇEKMEZ; q < 2 karakterse CH'ye hiç gidilmez
// (boş liste), en çok opSearchLimitMax ad. requireCopilot / rol kapısı YOK —
// LLM kullanmıyor, salt okuma (copilot/starters emsali; viewer de görür).
// serveCached 60 s, anahtar q (küçük harf; arama harf duyarsız, sıralama da
// lowerUTF8 ile) + limit. api.go/ai_routes.go BÜYÜMEZ: route defteri (init).

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func init() {
	registerRoutesExtra("copilot_operation_search", (*Server).registerCopilotOperationSearchRoutes)
}

func (s *Server) registerCopilotOperationSearchRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/copilot/operation-search", s.copilotOperationSearch)
}

const (
	opSearchMinQuery     = 2
	opSearchMaxQuery     = 80
	opSearchLimitDefault = 20
	opSearchLimitMax     = 50
	// opSearchOverfetch — SQL'in kaba süzgecinden geçip opSearchRoutable'a
	// takılan adlar (uçları noktalama, HTTP fiili…) listeyi boşaltmasın.
	opSearchOverfetch = 3
)

// opSearchQuery — v0.10.1390 SAF: ?q kırpılır; kısa ya da aşırı uzun → ""
// (CH'ye gidilmez).
func opSearchQuery(raw string) string {
	q := strings.TrimSpace(raw)
	if len([]rune(q)) < opSearchMinQuery || len(q) > opSearchMaxQuery {
		return ""
	}
	return q
}

// opSearchLimit — v0.10.1390 SAF: limit tavanı.
func opSearchLimit(raw string) int {
	n := parseInt(raw, opSearchLimitDefault)
	if n <= 0 {
		return opSearchLimitDefault
	}
	if n > opSearchLimitMax {
		return opSearchLimitMax
	}
	return n
}

// opSearchKey — v0.10.1390 SAF (hash-all-inputs): q harf duyarsız aranır →
// anahtarda küçük harf; limit cevabın uzunluğunu değiştirir.
func opSearchKey(q string, limit int) string {
	return fmt.Sprintf("copilot:op-search:v1:q=%s:lim=%d", fnvStr(strings.ToLower(q)), limit)
}

// opSearchRoutable — v0.10.1390 SAF: ad, ön-getirmenin belirteç kuralından
// (opTokens) TAM OLARAK kendisi olarak çıkıyor mu. Çıkmıyorsa (boşluk, uçta
// noktalama, <4 karakter, HTTP fiili, ayraç karakteri) "<ad> operasyonu neden
// hata veriyor?" cümlesi katalogda birebir eşleşmez → aramada önerilmez.
func opSearchRoutable(name string) bool {
	toks := opTokens(name)
	return len(toks) == 1 && toks[0] == name
}

// opSearchNames — v0.10.1390 SAF: isabetler → yönlenebilir tekil adlar, en
// çok limit (SQL sırası korunur).
func opSearchNames(ops []string, limit int) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, op := range ops {
		if len(out) >= limit {
			break
		}
		if seen[op] || !opSearchRoutable(op) {
			continue
		}
		seen[op] = true
		out = append(out, op)
	}
	return out
}

func (s *Server) copilotOperationSearch(w http.ResponseWriter, r *http.Request) {
	q := opSearchQuery(r.URL.Query().Get("q"))
	limit := opSearchLimit(r.URL.Query().Get("limit"))
	if q == "" || s.store == nil {
		writeJSON(w, map[string]any{"names": []string{}, "total": 0})
		return
	}
	s.serveCached(w, r, opSearchKey(q, limit), 60*time.Second, func(ctx context.Context) (any, error) {
		ops, err := s.store.SearchCatalogOperations(ctx, q, limit*opSearchOverfetch)
		if err != nil {
			return nil, err
		}
		names := opSearchNames(ops, limit)
		return map[string]any{"names": names, "total": len(names)}, nil
	})
}
