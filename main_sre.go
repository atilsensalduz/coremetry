package main

// main_sre.go — v0.10.1282 (AI SRE Faz 0a: sre rolü, kuyruk, drain, durum).
// COREMETRY_MODE=sre pod'unun boot yardımcıları. main.go büyümesin diye ayrı
// dosya; rol bayrakları ve kablolama main.go'da (parseRunModeValue,
// `if mode.sre { sre.StartDrain }`).

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// idleExitFor — drain'in boşta kapanma süresi. all modu: 0 (süreç içi drain
// asla tek başına kapanmaz). sre: COREMETRY_SRE_IDLE_EXIT — saniye (`120`) ya
// da Go süresi (`2m`); boş / geçersiz / 0 → 0 = asla. Varsayılan 0: tek
// başlatıcı always-on Deployment (operatör 2026-10-17); ileride bir Job
// başlatıcı bu env'i açıkça verir.
func idleExitFor(m runMode) time.Duration {
	if m.name == "all" {
		return 0
	}
	return parseIdleExit(os.Getenv("COREMETRY_SRE_IDLE_EXIT"))
}

// parseIdleExit — SAF.
func parseIdleExit(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if n, err := strconv.Atoi(raw); err == nil {
		if n <= 0 {
			return 0
		}
		return time.Duration(n) * time.Second
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	log.Printf("[sre] COREMETRY_SRE_IDLE_EXIT=%q geçersiz — idle exit kapalı", raw)
	return 0
}

// sreHealthHandler — sre-only pod'un HTTP yüzeyi: /livez (süreç canlı),
// /readyz (boot bitti — handler ancak boot sonrası bağlanır), /api/version.
// Başka her yol 404: sre pod'u UI / API / OTLP servis etmez, Service'i yok.
func sreHealthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": Version, "buildVersion": BuildVersion, "overridden": Version != BuildVersion, "mode": "sre",
		})
	})
	return mux
}

func startSREHealth(addr string) *http.Server {
	hs := &http.Server{Addr: addr, Handler: sreHealthHandler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[sre] health listener on %s: %v", addr, err)
		}
	}()
	log.Printf("[sre] health listener up on %s — /livez /readyz /api/version", addr)
	return hs
}
