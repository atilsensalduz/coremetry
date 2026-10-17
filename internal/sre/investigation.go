package sre

// investigation.go — v0.10.1282 — araştırma kaydı (= kuyruk satırının
// gövdesi) ve 64 KB tavanlı kodlayıcı.
//
// 0a yalnız bugün doldurulan alanları taşır; sonraki fazların alanları
// (hipotezler, zaman çizelgesi, öneriler, Zoom zinciri, anlatım) JSON'a
// geriye uyumlu eklenir — eski satır yeni alanı boş okur.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Durumlar (name kolonu).
const (
	StateQueued  = "queued"
	StateRunning = "running"
	StateDone    = "done"
	StatePartial = "partial"
	StateShared  = "shared"
	StateError   = "error"
	StateSkipped = "skipped"
)

// Türler.
const (
	KindAuto    = "auto"
	KindManual  = "manual"
	KindVerify  = "verify"
	KindReplay  = "replay"
	KindResolve = "resolve"
)

// Adım durumları.
const (
	StepOK      = "ok"
	StepPartial = "partial"
	StepError   = "error"
	StepSkipped = "skipped"
)

const (
	// maxStepPayload — adım yükü tavanı.
	maxStepPayload = 12 << 10
	// maxFindings — kayıtta tutulan en çok bulgu.
	maxFindings = 8
	maxWarnings = 10
)

// Anchor — araştırmanın penceresi.
type Anchor struct {
	From    int64  `json:"from"` // unix ns
	To      int64  `json:"to"`   // unix ns
	Symptom string `json:"symptom,omitempty"`
}

// Step — tek okuma adımı (önbellek anahtarlı, idempotent; resume ok adımı
// tazeyse atlar).
type Step struct {
	Name         string          `json:"name"`
	State        string          `json:"state"`
	Summary      string          `json:"summary,omitempty"`
	DurationMs   int64           `json:"durationMs"`
	At           int64           `json:"at"` // unix ns, adımın bittiği an
	EvidenceRefs []string        `json:"evidenceRefs,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}

// Finding — sıralı bulgu (0a: servis taramasının bulguları).
type Finding struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Level        string   `json:"level"`
	Reasons      []string `json:"reasons,omitempty"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

// Investigation — saved_views query_string gövdesi.
type Investigation struct {
	ProblemID  string    `json:"problemId"`
	DisplayID  string    `json:"displayId,omitempty"`
	Service    string    `json:"service"`
	Env        string    `json:"env,omitempty"`
	Kind       string    `json:"kind"`
	State      string    `json:"state"`
	Priority   string    `json:"priority,omitempty"` // salt-okunur kopya (enqueue anı)
	Attempts   int       `json:"attempts"`
	Run        int       `json:"run"` // kaçıncı koşu (elle yeniden araştırma ++)
	ClaimedBy  string    `json:"claimedBy,omitempty"`
	EnqueuedAt int64     `json:"enqueuedAt"`
	ClaimedAt  int64     `json:"claimedAt,omitempty"`
	StartedAt  int64     `json:"startedAt,omitempty"`
	FinishedAt int64     `json:"finishedAt,omitempty"`
	SharedFrom string    `json:"sharedFrom,omitempty"`
	RequestBy  string    `json:"requestBy,omitempty"`
	ProblemAt  int64     `json:"problemStartedAt"` // problemin başlangıcı (unix ns)
	Anchor     Anchor    `json:"anchor"`
	Steps      []Step    `json:"steps"`
	Findings   []Finding `json:"findings"`
	Error      string    `json:"error,omitempty"`
	Warnings   []string  `json:"warnings,omitempty"`
}

// step — ada göre adım (yoksa nil).
func (inv *Investigation) step(name string) *Step {
	for i := range inv.Steps {
		if inv.Steps[i].Name == name {
			return &inv.Steps[i]
		}
	}
	return nil
}

// setStep — adımı ekler ya da yerinde değiştirir.
func (inv *Investigation) setStep(s Step) {
	if cur := inv.step(s.Name); cur != nil {
		*cur = s
		return
	}
	inv.Steps = append(inv.Steps, s)
}

// Encode — SAF: kaydı ≤ SREInvestigationMaxBytes JSON'a kodlar. Sığmazsa
// sırayla küçültür: adım yükleri → uyarılar → bulgular ≤3 → adım özetleri
// kırpılır; yine sığmazsa hata (yazım reddedilir, kayıt yarım yazılmaz).
func Encode(inv Investigation) (string, error) {
	if inv.Steps == nil {
		inv.Steps = []Step{}
	}
	if inv.Findings == nil {
		inv.Findings = []Finding{}
	}
	if len(inv.Findings) > maxFindings {
		inv.Findings = inv.Findings[:maxFindings]
	}
	if len(inv.Warnings) > maxWarnings {
		inv.Warnings = inv.Warnings[:maxWarnings]
	}
	for i := range inv.Steps {
		if len(inv.Steps[i].Payload) > maxStepPayload {
			inv.Steps[i].Payload = nil
		}
	}
	shrink := []func(){
		func() {
			for i := range inv.Steps {
				inv.Steps[i].Payload = nil
			}
		},
		func() { inv.Warnings = nil },
		func() {
			if len(inv.Findings) > 3 {
				inv.Findings = inv.Findings[:3]
			}
			for i := range inv.Findings {
				if len(inv.Findings[i].Reasons) > 3 {
					inv.Findings[i].Reasons = inv.Findings[i].Reasons[:3]
				}
				inv.Findings[i].EvidenceRefs = nil
			}
		},
		func() {
			for i := range inv.Steps {
				inv.Steps[i].Summary = truncRunes(inv.Steps[i].Summary, 400)
				inv.Steps[i].EvidenceRefs = nil
			}
			inv.Error = truncRunes(inv.Error, 400)
		},
	}
	for i := 0; ; i++ {
		raw, err := json.Marshal(inv)
		if err != nil {
			return "", err
		}
		if len(raw) <= chstore.SREInvestigationMaxBytes {
			return string(raw), nil
		}
		if i >= len(shrink) {
			return "", fmt.Errorf("sre: kayıt %d B, tavan %d B — küçültülemedi", len(raw), chstore.SREInvestigationMaxBytes)
		}
		shrink[i]()
	}
}

// Decode — SAF: satır → kayıt. Gövdedeki problemId satır kimliğiyle
// eşleşmiyorsa ok=false (başka yoldan yazılmış sahte satır kayıt sayılmaz).
func Decode(v chstore.SavedView) (Investigation, bool) {
	var inv Investigation
	if v.Page != chstore.SREInvestigationPage || v.OwnerID != "" {
		return Investigation{}, false
	}
	if err := json.Unmarshal([]byte(v.QueryString), &inv); err != nil || inv.ProblemID == "" {
		return Investigation{}, false
	}
	if v.ID != chstore.SREInvestigationID(inv.ProblemID) {
		return Investigation{}, false
	}
	// name kolonu otoritedir (yazım tek satırda ikisini birlikte taşır).
	inv.State = v.Name
	if inv.EnqueuedAt == 0 {
		inv.EnqueuedAt = v.CreatedAt
	}
	return inv, true
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}
