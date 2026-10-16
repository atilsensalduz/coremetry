package api

// admin_function_code_shape.go — v0.10.1226: fonksiyon kodu biçim ölçümü
// (Ayarlar › Kod entegrasyonu). Yalnız admin; okuma 5 dk önbellekli —
// pencere "son 1 saat", girdi yok (anahtar sabit).

import (
	"context"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("function-code-shape", (*Server).registerFunctionCodeShapeRoutes) }

func (s *Server) registerFunctionCodeShapeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/function-code-shape", auth.RequireRole(auth.RoleAdmin, s.getFunctionCodeShape))
}

func (s *Server) getFunctionCodeShape(w http.ResponseWriter, r *http.Request) {
	s.serveCached(w, r, "admin:function-code-shape:v1", 5*time.Minute, func(ctx context.Context) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return s.store.FunctionCodeShapeStats(ctx, time.Now())
	})
}
