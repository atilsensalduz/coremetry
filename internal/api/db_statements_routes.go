package api

// db_statements_routes.go — v0.10.1162: bir veritabanının TÜM ifadeleri.
// GET /api/databases/statements?from&to&db_system&db_name[&q][&limit][&offset]
// → {rows, hasMore}. /api/databases/slow-queries ile aynı MV okuması ve satır
// şekli; fark sayfalama + örnek SQL'de arama (operatör: "sadece slow queries
// değil, bir veritabanının tüm sorgularına ulaşabilmeliyim"). Kimlikli her rol.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("db-statements", (*Server).registerDBStatementsRoutes) }

func (s *Server) registerDBStatementsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/databases/statements", auth.RequireAnyRole([]string{auth.RoleAdmin, auth.RoleEditor, auth.RoleViewer}, s.getDBStatements))
}

// dbStatementsSearchMax — arama metni tavanı (önbellek anahtarına girer).
const dbStatementsSearchMax = 200

func (s *Server) getDBStatements(w http.ResponseWriter, r *http.Request) {
	from, to := parseFromTo(r, time.Hour)
	q := r.URL.Query()
	dbSystem := strings.TrimSpace(q.Get("db_system"))
	dbName := strings.TrimSpace(q.Get("db_name"))
	search := strings.TrimSpace(q.Get("q"))
	if rs := []rune(search); len(rs) > dbStatementsSearchMax {
		search = string(rs[:dbStatementsSearchMax])
	}
	limit, offset := chstore.ClampDBStatementsPage(parseInt(q.Get("limit"), 100), parseInt(q.Get("offset"), 0))
	// Anahtar TÜM girdileri taşır (v0.5.187 çapraz zehirlenme sınıfı).
	key := fmt.Sprintf("db-statements:from=%d:to=%d:sys=%q:db=%q:q=%q:limit=%d:offset=%d",
		from.UnixNano()/int64(time.Minute), to.UnixNano()/int64(time.Minute),
		dbSystem, dbName, strings.ToLower(search), limit, offset)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		return s.store.GetDBStatements(ctx, from, to, dbSystem, dbName, search, limit, offset)
	})
}
