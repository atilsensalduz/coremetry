package api

// cosre_greeting.go — v0.10.1255 (operatör: CoSRE karşılamasında P1 sayısı
// yerine "son 1 saatteki exception / hata sayısı"). İki ucuz okuma: filo
// hatalı span sayısı (service_summary_5m MV) + son 1 saatte görülen
// exception grubu sayısı (exception_groups state tablosu). 60 sn önbellek;
// girdi yok (pencere sabit "son 1 saat", anahtar dakika kovası).

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("cosre-greeting", (*Server).registerCosreGreetingRoutes) }

func (s *Server) registerCosreGreetingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cosre/greeting", s.getCosreGreeting)
}

type cosreGreeting struct {
	Errors1h          uint64 `json:"errors1h"`
	ExceptionGroups1h int64  `json:"exceptionGroups1h"`
}

func (s *Server) getCosreGreeting(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	key := fmt.Sprintf("cosre-greeting:v1:%d", now.Unix()/60)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		from := now.Add(-time.Hour)
		var out cosreGreeting
		n, err := s.store.FleetErrorCount(ctx, from, now)
		if err != nil {
			return nil, err
		}
		out.Errors1h = n
		g, gerr := s.store.CountExceptionGroups(ctx, chstore.ExceptionGroupFilter{ActiveFromNs: from.UnixNano()})
		if gerr == nil {
			out.ExceptionGroups1h = g
		}
		return out, nil
	})
}
