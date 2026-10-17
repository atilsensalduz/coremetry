package sre

// status.go — v0.10.1282 — drain kalp atışı + süreç-içi durum. GET
// /api/sre/status ikisini okur: Redis'teki kalp atışları (sre pod'ları,
// ScanPrefix) ve all modunda aynı süreçte koşan drain'in atomic durumu
// (Redis'siz monolitte ScanPrefix boş döner).

import (
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// HeartbeatPrefix — "coremetry:sre:hb:<pod>".
	HeartbeatPrefix = "coremetry:sre:hb:"
	// HeartbeatTTL — kalp atışı anahtarının ömrü (10 s'de bir yenilenir).
	HeartbeatTTL = 60 * time.Second
	// HeartbeatFresh — "rol görüldü" penceresi (banner: 60 s'dir atış yok).
	HeartbeatFresh = 60 * time.Second
	// EventKind — SSE olay türü (FE eventInvalidations).
	EventKind = "sre.investigation"
)

// Launcher değerleri (GET /api/sre/status `launcher`).
const (
	LauncherDeployment = "deployment"
	LauncherInProcess  = "inprocess"
	LauncherNone       = "none"
)

// Heartbeat — tek drain pod'unun durumu.
type Heartbeat struct {
	Pod         string `json:"pod"`
	Version     string `json:"version"`
	Mode        string `json:"mode"` // sre | all
	StartedAt   int64  `json:"startedAt"`
	At          int64  `json:"at"`
	InFlight    int    `json:"inFlight"`
	Drained     int64  `json:"drained"`
	IdleExitSec int    `json:"idleExitSec"`
}

var local atomic.Pointer[Heartbeat]

// Local — bu süreçte koşan drain'in son durumu (yoksa ok=false).
func Local() (Heartbeat, bool) {
	if h := local.Load(); h != nil {
		return *h, true
	}
	return Heartbeat{}, false
}

// ParseHeartbeats — SAF: ham kalp atışları → taze olanlar, pod adına göre.
func ParseHeartbeats(raw [][]byte, now time.Time) []Heartbeat {
	out := []Heartbeat{}
	for _, b := range raw {
		var h Heartbeat
		if len(b) == 0 || json.Unmarshal(b, &h) != nil || h.Pod == "" {
			continue
		}
		if now.Sub(time.Unix(0, h.At)) > HeartbeatFresh {
			continue
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pod < out[j].Pod })
	return out
}

// ResolveLauncher — SAF: durum ekranının `launcher` alanı.
//   - drain bu süreçte koşuyor (all) → inprocess
//   - chart/manifest env'i (COREMETRY_SRE_LAUNCHER) deployment|none → o
//   - env yok: taze bir sre-modu kalp atışı varsa deployment, yoksa none
func ResolveLauncher(localDrain bool, env string, pods []Heartbeat) string {
	if localDrain {
		return LauncherInProcess
	}
	switch strings.ToLower(strings.TrimSpace(env)) {
	case LauncherDeployment:
		return LauncherDeployment
	case LauncherNone, "off":
		return LauncherNone
	}
	for _, p := range pods {
		if p.Mode == "sre" {
			return LauncherDeployment
		}
	}
	return LauncherNone
}
