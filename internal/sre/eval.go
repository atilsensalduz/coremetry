package sre

// eval.go — v0.10.1301 — AI SRE Faz 5: geçmiş problemlerle doğruluk ölçümü.
//
// Soru: ajanın hipotez sırası, insanın SONRADAN onayladığı kök nedenle ne
// sıklıkla örtüşüyor? Doğruluk etiketi YALNIZ onaylı incident_cases
// (status=confirmed: FinalCause + Category). Operatör kuralı "oylara çok
// güvenmeyelim": 👍/👎 etiket DEĞİL, puan girdisi DEĞİL — 👎 listesi /ai'de
// yalnız "incelemeye değer" adayı olarak ayrı okunur (api/sre_eval.go); bu
// dosya oya dokunmaz (eval_test.go TestEvalIgnoresFeedback + cosrelearn'deki
// oy → çalışma zamanı ratchet'i).
//
// Akış:
//  1. Admin POST /api/sre/eval/runs {from, to, limit≤50} → StartEvalRun:
//     onaylı problem vakaları (ListIncidentCases, sınırlı) → eşlenen her
//     kategori için Kind=replay kuyruk işi (EnqueueReplay; anahtar
//     "replay-<runId>-<problemId>" — canlı kayıt "sre-<problemId>" ASLA
//     yazılmaz), çıpa problemin StartedAt'i. Kuyruk payı MaxQueue/2: tekrar
//     canlı işlerin yerini doldurmaz; alış sırası da hepsinden sonra.
//  2. sre drain işi diğerleri gibi koşar (aynı okuyucular; plan anı AsOf;
//     anlatım / öneri yok → 0 LLM çağrısı). Bitince özeti koşu satırına
//     (saved_views page='sre-eval', id='sre-eval-<runId>') koşu başına Redis
//     kilidiyle birleşir. Kaybolan birleştirme okumada tekrar satırlarından
//     uzlaştırılır (reconcileEvalRun, SAF).
//  3. SAF scoreEval: top-1 / top-3 isabeti (hipotez türü → incident_cases
//     kategorisi, hypothesisTable), varlık eşleşmesi (en olası hipotezin öznesi
//     normalize FinalCause'da geçiyor mu), kapsam % (telemetri okuyucularının
//     veri döndürdüğü adım payı — saklaması dolmuş pencere "veri yok"), canlı
//     koşularda ilk hipoteze varış p50/p90.
//  4. v0.10.1450 (program 2a) — dondurulmuş küme: taban koşusunun Cases[]'i
//     kümenin kendisidir (yeni varlık yok). POST {rerunOf:<runId>} aynı vaka
//     listesini AYNI çıpalarla yeni bir koşu kimliğiyle yeniden kuyruğa alır
//     (rerunCases, SAF); koşu satırı sunucu sürümünü (Build) taşır. Vaka başına
//     AcceptedSuggestion = onaylayan CoSRE'nin önerisini değiştirmeden kabul
//     etti (chstore.SameCause) — etiket ajandan bağımsız değil; manşet ölçü
//     "bağımsız" vakalardır (operatör kararı 2). Karşılaştırma eval_compare.go.
//     Döngü (loop) bu sürümde YOK: istek loop:true → ErrEvalInvalid.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Koşu durumları (koşu satırının name kolonu).
const (
	EvalRunning = "running"
	EvalDone    = "done"
)

// Vaka durumları — tekrar kaydının durumları (done / partial / error /
// running / queued) + değerlendirmenin kendi iki durumu.
const (
	// CaseUnmapped — kategori hipotez türlerine eşlenmiyor (evalUnmappedCategories);
	// tekrar edilmez, isabet paydasına girmez.
	CaseUnmapped = "unmapped"
	// CaseSkipped — tekrar edilemedi (servis problemi değil / kuyruk payı / hata).
	CaseSkipped = "skipped"
)

const (
	// EvalMaxCases — koşu başına en çok vaka (POST limit tavanı).
	EvalMaxCases = 50
	// EvalDefaultCases — limit verilmezse.
	EvalDefaultCases = 20
	// evalMaxWindow — vaka penceresinin en uzun hâli.
	evalMaxWindow = 366 * 24 * time.Hour
	// evalStaleAfter — bitmemiş koşunun "sürüyor" sayıldığı süre (50 iş ×
	// 90 s / 2 eşzamanlı ≈ 40 dk; pay ×4). Sonrası yeni koşuyu engellemez,
	// kuyrukta satırı olmayan vaka "error" görünür.
	evalStaleAfter = 3 * time.Hour
	// evalLockPrefix — koşu satırı birleştirme kilidi (drain'ler arası).
	evalLockPrefix = "coremetry:lock:sre-eval:"
	evalLockLease  = 30 * time.Second
	// evalCauseRunes — koşu satırında saklanan FinalCause uzunluğu.
	evalCauseRunes = 300
	// evalTopN — vaka başına saklanan hipotez sayısı (top-3 ölçüsü).
	evalTopN = 3
	// evalLiveSample — ilk hipoteze varış örneklemi (en yeni canlı kayıtlar).
	evalLiveSample = 200
)

var (
	// ErrEvalBusy — bitmemiş bir koşu var.
	ErrEvalBusy = errors.New("değerlendirme sürüyor — önceki koşu bitmeden yenisi başlamaz")
	// ErrEvalInvalid — geçersiz pencere / limit.
	ErrEvalInvalid = errors.New("geçersiz değerlendirme isteği")
	// ErrEvalBaseMissing — rerunOf'un gösterdiği taban koşusu yok.
	ErrEvalBaseMissing = errors.New("taban koşusu bulunamadı")
)

// Dondurulmuş vaka nedenleri — tekrar edilemeyen vaka yeniden koşuda da
// edilemez (rerunCases bu öneklere bakar; kodlama tavanı nedeni 60 runeye
// kısaltsa da önek kalır).
const (
	evalReasonNotServiceKey = "servis problemi değil"
	evalReasonNotService    = evalReasonNotServiceKey + " (AI SRE yalnız servis problemlerini araştırır)"
	evalReasonNoAnchorKey   = "vakada servis / problem"
	evalReasonNoAnchor      = evalReasonNoAnchorKey + " / başlangıç yok"
	evalReasonQueueFull     = "kuyruk payı dolu (değerlendirme MaxQueue'nun yarısını aşamaz)"
	evalFrozenPrefix        = "dondurulmuş: "
)

// evalUnmappedCategories — hiçbir hipotez türünün karşılamadığı incident_cases
// kategorileri ve NEDENİ (TestHypKindCategoryMap: cosrelearn.CaseCategories'in
// her girdisi ya bir türün kategorisi ya da burada).
var evalUnmappedCategories = map[string]string{
	"infra": "altyapı (node / ağ / depolama) — ajanın ayrı bir altyapı hipotezi yok (node baskısı doygunluk = capacity kartının kontrolü)",
	"data":  "veri sorunu (bozuk / eksik iş verisi) — telemetriden türetilen bir hipotez türü yok",
	"noise": "gürültü — gerçek kök neden yok; isabet ölçülemez",
	"other": "diğer — kapalı küme dışı serbest neden; eşlenecek tür yok",
}

// hypKindCategory — SAF: hipotez türü → incident_cases kategorisi (tek kaynak
// hypothesisTable).
func hypKindCategory() map[string]string {
	out := make(map[string]string, len(hypothesisTable))
	for _, s := range hypothesisTable {
		out[s.Kind] = s.Category
	}
	return out
}

// evalCategoryMapped — SAF: kategori en az bir hipotez türüne eşleniyor mu.
func evalCategoryMapped(cat string) bool {
	for _, s := range hypothesisTable {
		if s.Category == cat {
			return true
		}
	}
	return false
}

// ── kayıt şekilleri ─────────────────────────────────────────────────────────

// EvalHyp — vakanın tekrarındaki bir hipotez (sıra = skor sırası).
type EvalHyp struct {
	Kind     string  `json:"kind"`
	Category string  `json:"category"`
	Score    float64 `json:"score"`
	Band     string  `json:"band"`
	// Subject — hipotezin öznesi (bağımlılık / dış servis / DB → ilgili düğüm;
	// diğerleri → problemin servisi). "" = belirlenemedi.
	Subject string `json:"subject,omitempty"`
}

// EvalCoverage — tekrarın telemetri okuyucularının sonucu (adım sayısı).
type EvalCoverage struct {
	Data   int `json:"data"`   // veri döndü
	NoData int `json:"noData"` // baktı, "veri yok" (saklama dışı)
	Failed int `json:"failed"` // okunamadı
	// NoDataSteps — "veri yok" dönen adımlar (sweep, deps, pivot, log_spike, node).
	NoDataSteps []string `json:"noDataSteps,omitempty"`
}

// EvalCase — koşunun bir vakası: beklenen (insan) + tekrarın özeti.
type EvalCase struct {
	CaseID     string        `json:"caseId"`
	ProblemID  string        `json:"problemId"`
	DisplayID  string        `json:"displayId,omitempty"`
	Service    string        `json:"service"`
	Category   string        `json:"category"`   // beklenen (onaylı vaka)
	FinalCause string        `json:"finalCause"` // ≤300 rune
	StartedAt  int64         `json:"startedAt"`  // unix ns — çıpa
	State      string        `json:"state"`      // queued | running | done | partial | error | skipped | unmapped
	Reason     string        `json:"reason,omitempty"`
	Top        []EvalHyp     `json:"top,omitempty"` // ≤3
	Coverage   *EvalCoverage `json:"coverage,omitempty"`
	FinishedAt int64         `json:"finishedAt,omitempty"`
	// AcceptedSuggestion — v0.10.1450: onaylayan CoSRE'nin önerdiği nedeni
	// değiştirmeden kabul etti (chstore.SameCause(SuggestedCause, FinalCause))
	// → bu vakanın etiketi ajandan bağımsız değil. Başlatmada vakadan, yeniden
	// koşuda tabandan kopyalanır.
	AcceptedSuggestion bool `json:"acceptedSuggestion,omitempty"`
	// Pack — v0.10.1494: paketli tekrarın künye özeti (paketsiz → yok).
	Pack *EvalPackStat `json:"pack,omitempty"`
}

// EvalPackStat — v0.10.1494 (CoSRE 2d): bir vakanın Context Pack künyesi +
// paket okuyucularının kapsamı (baseline / anomaliler / hata özeti). Hipotez
// skoru paketi OKUMAZ — bu ölçü isabet değil, paketin boyutu ve dolgusudur.
type EvalPackStat struct {
	Bytes        int  `json:"bytes"`
	EstTokens    int  `json:"estTokens"`
	Trimmed      bool `json:"trimmed,omitempty"`
	TrimmedRows  int  `json:"trimmedRows,omitempty"`
	Dropped      int  `json:"dropped,omitempty"`
	ReaderData   int  `json:"readerData"`
	ReaderNoData int  `json:"readerNoData"`
	ReaderFailed int  `json:"readerFailed"`
}

// packReaderSteps — paket okuyucularının adım adları.
var packReaderSteps = map[string]bool{stepBaseline: true, stepAnomalies: true, stepErrors: true}

// evalPackStatOf — SAF: tekrar kaydı → paket özeti (künye yoksa nil).
func evalPackStatOf(inv Investigation) *EvalPackStat {
	if inv.ContextPack == nil {
		return nil
	}
	m := inv.ContextPack
	out := &EvalPackStat{Bytes: m.Bytes, EstTokens: m.EstTokens, Trimmed: m.Trimmed || m.Capped}
	for _, s := range m.Sections {
		out.TrimmedRows += s.TrimmedRows
		if s.Dropped {
			out.Dropped++
		}
	}
	for _, s := range inv.Steps {
		if !packReaderSteps[s.Name] {
			continue
		}
		switch {
		case s.State == StepError:
			out.ReaderFailed++
		case s.NoData:
			out.ReaderNoData++
		case s.State == StepOK || s.State == StepPartial:
			out.ReaderData++
		}
	}
	return out
}

// EvalRun — koşu satırının gövdesi.
type EvalRun struct {
	RunID      string     `json:"runId"`
	State      string     `json:"state"` // running | done
	From       int64      `json:"from"`  // unix ns — vaka açılış penceresi
	To         int64      `json:"to"`
	Limit      int        `json:"limit"`
	By         string     `json:"by,omitempty"`
	CreatedAt  int64      `json:"createdAt"`
	FinishedAt int64      `json:"finishedAt,omitempty"`
	Found      int        `json:"found"` // penceredeki onaylı problem vakası (≤limit)
	Cases      []EvalCase `json:"cases,omitempty"`
	// RerunOf — v0.10.1450: bu koşu taban koşusunun dondurulmuş kümesinin
	// yeniden koşusu ("" = pencereden okunan yeni küme).
	RerunOf string `json:"rerunOf,omitempty"`
	// Loop — döngü açık değerlendirme (sürüm 2c doldurur; 2a'da hep false).
	Loop bool `json:"loop,omitempty"`
	// Build — koşuyu başlatan sunucunun sürümü (/api/version buildVersion).
	Build string `json:"build,omitempty"`
	// SuggestionKnown — vakaların AcceptedSuggestion bayrağı hesaplandı
	// (v0.10.1450 öncesi koşularda false: "bağımsız" ayrımı bilinmez).
	SuggestionKnown bool `json:"suggestionKnown,omitempty"`
	// ContextPack — v0.10.1494 (CoSRE 2d, STOP C'): tekrarlar Context Pack
	// okuyucularıyla (deterministik, LLM'siz) koştu. Canlı bayraktan BAĞIMSIZ:
	// yalnız istekten gelir; taban B paketsiz kalır.
	ContextPack bool `json:"contextPack,omitempty"`
}

// ── başlatma ────────────────────────────────────────────────────────────────

// EvalRunStore — koşu satırının okuması/yazması (drain birleştirmesi).
type EvalRunStore interface {
	GetSREEvalRun(ctx context.Context, runID string) (*chstore.SavedView, error)
	PutSREEvalRun(ctx context.Context, runID, state, body string, createdAt int64) error
}

// EvalReadStore — okuma ucunun ihtiyacı (koşular + uzlaştırma + canlı örneklem).
type EvalReadStore interface {
	EvalRunStore
	ListSREEvalRuns(ctx context.Context, limit int) ([]chstore.SavedView, error)
	ListSREReplayRows(ctx context.Context, runID string, limit int) ([]chstore.SavedView, error)
	ListRecentSREInvestigations(ctx context.Context, states []string, limit int) ([]chstore.SavedView, error)
}

// EvalStore — StartEvalRun'ın ihtiyacı (*chstore.Store karşılar). Problem /
// vaka yalnız OKUNUR; tek yazımlar kendi satırları (tekrar işi + koşu).
type EvalStore interface {
	QueueStore
	EvalReadStore
	ListIncidentCases(ctx context.Context, q chstore.IncidentCaseQuery) ([]chstore.IncidentCase, error)
	GetProblem(ctx context.Context, id string) (*chstore.Problem, error)
	EnrichProblemsForRead(ctx context.Context, probs []chstore.Problem, lookback time.Duration) []chstore.Problem
}

// EvalRunRequest — POST /api/sre/eval/runs.
type EvalRunRequest struct {
	From, To time.Time
	Limit    int
	By       string
	Now      time.Time
	// RerunOf — taban koşusunun kimliği: pencere yerine onun dondurulmuş
	// vaka listesi (From/To/Limit verilmez).
	RerunOf string
	// Loop — döngü açık değerlendirme; 2a'da reddedilir (sahibi sürüm 2c).
	Loop bool
	// Build — sunucu sürümü (api doldurur).
	Build string
	// ContextPack — v0.10.1494: tekrarlar paket okuyucularıyla (deterministik).
	ContextPack bool
}

// ValidEvalRunID — SAF: koşu kimliği alfabesi (base36 küçük harf / rakam, '-'
// yok — tekrar anahtarı "replay-<runId>-<problemId>" tek koşuya ait kalır), ≤40.
func ValidEvalRunID(id string) bool {
	if id == "" || len(id) > 40 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// NormalizeEvalRequest — SAF: limit 0 → 20, >50 → hata değil kırpma; pencere
// ters / boş / gelecekte biten / 366 günden uzun → ErrEvalInvalid. rerunOf
// pencereyle birlikte verilmez; loop bu sürümde yok.
func NormalizeEvalRequest(req EvalRunRequest) (EvalRunRequest, error) {
	if req.Now.IsZero() {
		req.Now = time.Now()
	}
	if req.Loop {
		return req, fmt.Errorf("%w: döngü değerlendirmesi bu sürümde yok", ErrEvalInvalid)
	}
	if req.RerunOf != "" {
		switch {
		case !ValidEvalRunID(req.RerunOf):
			return req, fmt.Errorf("%w: geçersiz rerunOf", ErrEvalInvalid)
		case !req.From.IsZero() || !req.To.IsZero() || req.Limit != 0:
			return req, fmt.Errorf("%w: rerunOf ile pencere birlikte verilmez", ErrEvalInvalid)
		}
		return req, nil
	}
	switch {
	case req.Limit <= 0:
		req.Limit = EvalDefaultCases
	case req.Limit > EvalMaxCases:
		req.Limit = EvalMaxCases
	}
	if req.From.IsZero() || req.To.IsZero() || !req.To.After(req.From) {
		return req, fmt.Errorf("%w: from < to zorunlu", ErrEvalInvalid)
	}
	if req.To.After(req.Now) {
		req.To = req.Now
	}
	if req.To.Sub(req.From) > evalMaxWindow {
		return req, fmt.Errorf("%w: pencere en çok 366 gün", ErrEvalInvalid)
	}
	return req, nil
}

// newEvalRunID — base36 nano zaman damgası ('-' taşımaz: tekrar anahtarının
// öneki "replay-<runId>-" tek koşuya aittir).
func newEvalRunID(now time.Time) string { return "e" + strconv.FormatInt(now.UnixNano(), 36) }

// StartEvalRun — koşuyu açar: vakaları okur, tekrar işlerini kuyruğa alır,
// koşu satırını yazar. Kuyruğa alınan iş yoksa koşu hemen "done".
func StartEvalRun(ctx context.Context, st EvalStore, req EvalRunRequest, cfg Settings) (EvalRun, error) {
	if !cfg.IsEnabled() {
		return EvalRun{}, ErrDisabled
	}
	req, err := NormalizeEvalRequest(req)
	if err != nil {
		return EvalRun{}, err
	}
	now := req.Now
	if busy, err := evalBusy(ctx, st, now); err != nil {
		return EvalRun{}, err
	} else if busy {
		return EvalRun{}, ErrEvalBusy
	}
	if req.RerunOf != "" {
		return startEvalRerun(ctx, st, req, cfg)
	}
	cases, err := st.ListIncidentCases(ctx, chstore.IncidentCaseQuery{Status: chstore.CaseConfirmed, Source: "problem",
		From: req.From, To: req.To, Limit: req.Limit})
	if err != nil {
		return EvalRun{}, err
	}
	queued, err := st.CountSREQueued(ctx)
	if err != nil {
		return EvalRun{}, err
	}
	room := evalQueueRoom(cfg, queued)
	run := EvalRun{RunID: newEvalRunID(now), State: EvalRunning, From: req.From.UnixNano(), To: req.To.UnixNano(),
		Limit: req.Limit, By: req.By, CreatedAt: now.UnixNano(), Found: len(cases), Cases: make([]EvalCase, 0, len(cases)),
		Build: req.Build, SuggestionKnown: true, ContextPack: req.ContextPack}
	for _, c := range cases {
		ec := EvalCase{CaseID: c.CaseID, ProblemID: c.SourceID, Service: c.Service, Category: c.Category,
			FinalCause: truncRunes(strings.TrimSpace(c.FinalCause), evalCauseRunes), StartedAt: c.OpenedAt.UnixNano(), State: StateQueued,
			AcceptedSuggestion: chstore.SameCause(c.SuggestedCause, c.FinalCause)}
		if !evalCategoryMapped(c.Category) {
			ec.State, ec.Reason = CaseUnmapped, "kategori hipotez türlerine eşlenmiyor: "+firstNonEmpty(evalUnmappedCategories[c.Category], c.Category)
			run.Cases = append(run.Cases, ec)
			continue
		}
		priority := ""
		if p, err := st.GetProblem(ctx, c.SourceID); err == nil && p != nil && p.ID != "" {
			if p.Service == "" || chstore.ProblemSubjectKind(p.Kind) != chstore.ProblemKindService {
				ec.State, ec.Reason = CaseSkipped, evalReasonNotService
				run.Cases = append(run.Cases, ec)
				continue
			}
			ec.Service = p.Service
			if p.StartedAt > 0 {
				ec.StartedAt = p.StartedAt // çıpa: problemin StartedAt'i (vaka açılışıyla aynı kaynak)
			}
			if ps := st.EnrichProblemsForRead(ctx, []chstore.Problem{*p}, problemDeployLookback); len(ps) == 1 {
				ec.DisplayID, priority = ps[0].DisplayID, ps[0].Priority
			}
		} else {
			ec.Reason = "problem satırı yok — çıpa vakanın açılışı"
		}
		room = enqueueEvalCase(ctx, st, run.RunID, &ec, priority, req.By, now, cfg, room, req.ContextPack)
		run.Cases = append(run.Cases, ec)
	}
	run = finalizeEvalRun(run, now)
	if err := saveEvalRun(ctx, st, run); err != nil {
		return EvalRun{}, err
	}
	return run, nil
}

// enqueueEvalCase — vakanın tekrar işini kuyruğa alır (ya da nedeniyle
// atlar); kalan kuyruk payını döner. Başlatma ve yeniden koşu ortak yolu.
func enqueueEvalCase(ctx context.Context, st EvalStore, runID string, ec *EvalCase, priority, by string, now time.Time, cfg Settings, room int, pack bool) int {
	switch {
	case ec.Service == "" || ec.ProblemID == "" || ec.StartedAt <= 0:
		ec.State, ec.Reason = CaseSkipped, evalReasonNoAnchor
	case room <= 0:
		ec.State, ec.Reason = CaseSkipped, evalReasonQueueFull
	default:
		created, err := EnqueueReplay(ctx, st, ReplayRequest{RunID: runID, CaseID: ec.CaseID, ProblemID: ec.ProblemID,
			DisplayID: ec.DisplayID, Service: ec.Service, Priority: priority, ProblemStartedAt: ec.StartedAt, By: by, Now: now, ContextPack: pack}, cfg)
		switch {
		case err != nil:
			ec.State, ec.Reason = CaseSkipped, truncRunes("kuyruğa alınamadı: "+err.Error(), 200)
		case created:
			room--
		}
	}
	return room
}

// startEvalRerun — v0.10.1450: taban koşusunun dondurulmuş vaka listesini
// yeni koşu kimliğiyle, AYNI çıpalarla (StartedAt → AsOf, plan pencereleri)
// yeniden kuyruğa alır. Vakalar yeniden OKUNMAZ: etiket, kategori, öneri
// bayrağı tabandaki gibidir (sonradan düzeltilen bir vaka tabanı değiştirmez).
// Öncelik "" — tekrarın alış sırası zaten hepsinden sonra (drain).
func startEvalRerun(ctx context.Context, st EvalStore, req EvalRunRequest, cfg Settings) (EvalRun, error) {
	v, err := st.GetSREEvalRun(ctx, req.RerunOf)
	if err != nil {
		return EvalRun{}, err
	}
	if v == nil {
		return EvalRun{}, fmt.Errorf("%w: %s", ErrEvalBaseMissing, req.RerunOf)
	}
	base, ok := decodeEvalRun(*v)
	if !ok {
		return EvalRun{}, fmt.Errorf("%w: %s", ErrEvalBaseMissing, req.RerunOf)
	}
	queued, err := st.CountSREQueued(ctx)
	if err != nil {
		return EvalRun{}, err
	}
	now := req.Now
	room := evalQueueRoom(cfg, queued)
	run := EvalRun{RunID: newEvalRunID(now), State: EvalRunning, From: base.From, To: base.To, Limit: base.Limit, By: req.By,
		CreatedAt: now.UnixNano(), Found: base.Found, Cases: rerunCases(base), RerunOf: base.RunID, Build: req.Build,
		SuggestionKnown: base.SuggestionKnown, ContextPack: req.ContextPack}
	for i := range run.Cases {
		if ec := &run.Cases[i]; ec.State == StateQueued {
			room = enqueueEvalCase(ctx, st, run.RunID, ec, "", req.By, now, cfg, room, req.ContextPack)
		}
	}
	run = finalizeEvalRun(run, now)
	if err := saveEvalRun(ctx, st, run); err != nil {
		return EvalRun{}, err
	}
	return run, nil
}

// frozenSkip — SAF: tabanda tekrar EDİLEMEYEN vaka (servis problemi değil /
// çıpa yok) — yeniden koşuda da edilemez. Kuyruk payı / kuyruk hatası
// geçicidir: yeniden kuyruğa girer.
func frozenSkip(c EvalCase) bool {
	if c.State != CaseSkipped {
		return false
	}
	r := strings.TrimPrefix(c.Reason, evalFrozenPrefix)
	return c.Service == "" || c.ProblemID == "" || c.StartedAt <= 0 ||
		strings.HasPrefix(r, evalReasonNotServiceKey) || strings.HasPrefix(r, evalReasonNoAnchorKey)
}

// rerunCases — SAF: taban koşusunun vakaları → yeniden koşunun vakaları.
// Kimlik + beklenen + çıpa + öneri bayrağı aynen; eşlenmeyen ve dondurulmuş
// atlanan vaka durumunu "dondurulmuş: <neden>" ile korur; geri kalan her vaka
// (bitmiş / hata / sürüyor / geçici atlanmış) "queued" ve sonuçsuz başlar.
func rerunCases(base EvalRun) []EvalCase {
	out := make([]EvalCase, 0, len(base.Cases))
	for _, c := range base.Cases {
		ec := EvalCase{CaseID: c.CaseID, ProblemID: c.ProblemID, DisplayID: c.DisplayID, Service: c.Service, Category: c.Category,
			FinalCause: c.FinalCause, StartedAt: c.StartedAt, AcceptedSuggestion: c.AcceptedSuggestion, State: StateQueued}
		if c.State == CaseUnmapped || frozenSkip(c) {
			ec.State, ec.Reason = c.State, evalFrozenPrefix+strings.TrimPrefix(c.Reason, evalFrozenPrefix)
		}
		out = append(out, ec)
	}
	return out
}

// evalQueueRoom — SAF: tekrara ayrılan kuyruk payı = MaxQueue/2 − kuyruktaki.
func evalQueueRoom(cfg Settings, queued int) int {
	room := cfg.MaxQueue/2 - queued
	if room < 0 {
		return 0
	}
	return room
}

// evalBusy — taze ve (uzlaştırılmış hâliyle) bitmemiş koşu var mı.
func evalBusy(ctx context.Context, st EvalReadStore, now time.Time) (bool, error) {
	rows, err := st.ListSREEvalRuns(ctx, 20)
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		run, ok := decodeEvalRun(r)
		if !ok || run.State == EvalDone || now.Sub(time.Unix(0, run.CreatedAt)) >= evalStaleAfter {
			continue
		}
		rep, err := st.ListSREReplayRows(ctx, run.RunID, chstore.SREReplayRowsMax)
		if err != nil {
			return false, err
		}
		if reconcileEvalRun(run, rep, now).State != EvalDone {
			return true, nil
		}
	}
	return false, nil
}

// ── kayıt kodlama ───────────────────────────────────────────────────────────

func caseTerminal(state string) bool {
	switch state {
	case StateDone, StatePartial, StateError, CaseSkipped, CaseUnmapped:
		return true
	}
	return false
}

// finalizeEvalRun — SAF: bütün vakalar bittiyse koşu "done".
func finalizeEvalRun(run EvalRun, now time.Time) EvalRun {
	for _, c := range run.Cases {
		if !caseTerminal(c.State) {
			run.State = EvalRunning
			return run
		}
	}
	if run.State != EvalDone || run.FinishedAt == 0 {
		run.State, run.FinishedAt = EvalDone, now.UnixNano()
	}
	return run
}

// encodeEvalRun — SAF: ≤ SREEvalMaxBytes; sığmazsa nedenler kısalır, gerekçe
// ve "veri yok" adım listeleri düşer.
func encodeEvalRun(run EvalRun) (string, error) {
	run.Cases = append([]EvalCase(nil), run.Cases...)
	for i := 0; ; i++ {
		raw, err := json.Marshal(run)
		if err != nil {
			return "", err
		}
		if len(raw) <= chstore.SREEvalMaxBytes {
			return string(raw), nil
		}
		if i >= 2 {
			return "", fmt.Errorf("sre eval: koşu %d B, tavan %d B", len(raw), chstore.SREEvalMaxBytes)
		}
		for j := range run.Cases {
			c := &run.Cases[j]
			c.FinalCause = truncRunes(c.FinalCause, 80)
			c.Reason = truncRunes(c.Reason, 60)
			if c.Coverage != nil && i == 1 {
				cov := *c.Coverage
				cov.NoDataSteps = nil
				c.Coverage = &cov
			}
		}
	}
}

func saveEvalRun(ctx context.Context, st EvalRunStore, run EvalRun) error {
	body, err := encodeEvalRun(run)
	if err != nil {
		return err
	}
	return st.PutSREEvalRun(ctx, run.RunID, run.State, body, run.CreatedAt)
}

// decodeEvalRun — SAF: satır → koşu (kimlik gövdeyle eşleşmeli).
func decodeEvalRun(v chstore.SavedView) (EvalRun, bool) {
	if v.Page != chstore.SREEvalPage || v.OwnerID != "" {
		return EvalRun{}, false
	}
	var run EvalRun
	if err := json.Unmarshal([]byte(v.QueryString), &run); err != nil || run.RunID == "" || v.ID != chstore.SREEvalID(run.RunID) {
		return EvalRun{}, false
	}
	run.State = v.Name
	return run, true
}

// ── tekrar sonucu → vaka ────────────────────────────────────────────────────

// evalCoverageSteps — kapsamı ölçülen TELEMETRİ okuyucuları. deploys / events
// hariç: deploy ve problem satırlarının saklaması uzun, "yok" çoğunlukla
// gerçek yokluktur (pencerede deploy olmadı), veri yaşlanması değil.
var evalCoverageSteps = map[string]bool{stepSweep: true, stepDeps: true, stepPivot: true, stepLogSpike: true, stepNode: true}

// caseCoverage — SAF: tekrar adımlarından kapsam. skipped (okuyucu bağlı
// değil / planlanmadı) sayılmaz.
func caseCoverage(steps []Step) *EvalCoverage {
	cov := &EvalCoverage{}
	for _, s := range steps {
		if !evalCoverageSteps[s.Name] {
			continue
		}
		switch s.State {
		case StepError:
			cov.Failed++
		case StepOK, StepPartial:
			if s.NoData {
				cov.NoData++
				cov.NoDataSteps = append(cov.NoDataSteps, s.Name)
			} else {
				cov.Data++
			}
		}
	}
	return cov
}

// worstEdge — SAF: en yüksek hata, eşitlikte en yavaş kenarın hedefi.
func worstEdge(edges []EdgeFact) string {
	best := -1
	for i, e := range edges {
		if best < 0 || e.ErrorRate > edges[best].ErrorRate || (e.ErrorRate == edges[best].ErrorRate && e.AvgMs > edges[best].AvgMs) {
			best = i
		}
	}
	if best < 0 {
		return ""
	}
	return edges[best].Target
}

// hypothesisSubject — SAF: hipotezin öznesi. Bağımlılık → kenarı doğrulanmış
// kötüleşen aşağı akış komşu (yoksa sentezleyicinin aşağı akış şüphelisi);
// dış servis / DB → en kötü ext: / db: kenarı; diğerleri → problemin servisi.
func hypothesisSubject(kind string, ev Evidence) string {
	switch kind {
	case HypDependency:
		if df := ev.Facts.Deps; df != nil {
			for _, c := range df.Causes {
				if (c.Relation == chstore.RelationDownstream || c.Relation == chstore.RelationBoth) &&
					(c.Direction == chstore.ChangeWorse || c.Direction == chstore.ChangeLost) {
					return c.Service
				}
			}
		}
		if ev.Synth != nil && ev.Synth.TopSuspect != "" && ev.Synth.TopSuspect != ev.Service {
			for _, e := range ev.serviceEdges() {
				if e.Target == ev.Synth.TopSuspect {
					return e.Target
				}
			}
		}
		return ""
	case HypExternal:
		return worstEdge(ev.edgesWithPrefix("ext:"))
	case HypDBSlow:
		return worstEdge(ev.edgesWithPrefix("db:"))
	}
	return ev.Service
}

// caseTop — SAF: tekrarın ilk 3 hipotezi (kayıt skor sıralı) + özneleri.
func caseTop(inv Investigation) []EvalHyp {
	ev := evidenceFrom(&inv)
	var out []EvalHyp
	for i, h := range inv.Hypotheses {
		if i >= evalTopN {
			break
		}
		out = append(out, EvalHyp{Kind: h.Kind, Category: h.Category, Score: h.Score, Band: h.Band, Subject: hypothesisSubject(h.Kind, ev)})
	}
	return out
}

// mergeEvalCase — SAF: tekrar kaydını koşunun vakasına işler (problemId ile).
// Bitmemiş tekrar (queued / running) yalnız durumu taşır.
func mergeEvalCase(run EvalRun, inv Investigation, now time.Time) EvalRun {
	run.Cases = append([]EvalCase(nil), run.Cases...)
	for i := range run.Cases {
		c := &run.Cases[i]
		if c.ProblemID != inv.ProblemID || c.State == CaseUnmapped || c.State == CaseSkipped {
			continue
		}
		switch inv.State {
		case StateDone, StatePartial, StateError:
			c.State = inv.State
			c.Top = caseTop(inv)
			c.Coverage = caseCoverage(inv.Steps)
			c.Pack = evalPackStatOf(inv) // v0.10.1494 — paketsiz tekrar → nil
			c.FinishedAt = inv.FinishedAt
			if inv.State == StateError {
				c.Reason = truncRunes(firstNonEmpty(inv.Error, "tekrar tamamlanamadı"), 200)
			}
		case StateRunning, StateQueued:
			c.State = inv.State
		}
	}
	return finalizeEvalRun(run, now)
}

// reconcileEvalRun — SAF: okuma anında bitmemiş vakaları kuyruktaki tekrar
// satırlarından tamamlar (kaybolan birleştirme). Satırı hiç olmayan vaka
// koşu evalStaleAfter'dan eskiyse "error". YAZMAZ.
func reconcileEvalRun(run EvalRun, replay []chstore.SavedView, now time.Time) EvalRun {
	pending := false
	for _, c := range run.Cases {
		if !caseTerminal(c.State) {
			pending = true
		}
	}
	if !pending {
		return finalizeEvalRun(run, now)
	}
	seen := map[string]bool{}
	for _, r := range replay {
		inv, ok := Decode(r)
		if !ok || inv.Kind != KindReplay || inv.EvalRun != run.RunID {
			continue
		}
		seen[inv.ProblemID] = true
		run = mergeEvalCase(run, inv, now)
	}
	if now.Sub(time.Unix(0, run.CreatedAt)) >= evalStaleAfter {
		run.Cases = append([]EvalCase(nil), run.Cases...)
		for i := range run.Cases {
			if c := &run.Cases[i]; !caseTerminal(c.State) && !seen[c.ProblemID] {
				c.State, c.Reason = StateError, "tekrar işi kuyrukta yok (süpürüldü ya da yazılamadı)"
			}
		}
	}
	return finalizeEvalRun(run, now)
}

// mergeEvalInto — drain: tekrar sonucunu koşu satırına işler (çağıran koşu
// kilidini tutar). Koşu satırı yoksa no-op (okuma uzlaştırır).
func mergeEvalInto(ctx context.Context, st EvalRunStore, inv Investigation, now time.Time) error {
	v, err := st.GetSREEvalRun(ctx, inv.EvalRun)
	if err != nil || v == nil {
		return err
	}
	run, ok := decodeEvalRun(*v)
	if !ok {
		return nil
	}
	return saveEvalRun(ctx, st, mergeEvalCase(run, inv, now))
}

// recordEval — drain: biten (ya da tavanı dolan) tekrar işinin özeti koşu
// satırına; koşu başına kilit (eşzamanlı drain'ler tam-satır yazımı ezmesin).
func (dr *drainer) recordEval(inv Investigation) {
	if dr.d.Eval == nil || dr.d.Lock == nil || inv.Kind != KindReplay || inv.EvalRun == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key := evalLockPrefix + inv.EvalRun
	for i := 0; ; i++ {
		ok, err := dr.d.Lock.TryAcquire(ctx, key, evalLockLease)
		if err != nil {
			dr.logErr("değerlendirme kilidi", err)
			return
		}
		if ok {
			break
		}
		if i >= 20 {
			log.Printf("[sre] değerlendirme %s: koşu kilidi alınamadı — okuma uzlaştırır", inv.EvalRun)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	defer func() { _ = dr.d.Lock.Release(context.WithoutCancel(ctx), key) }()
	if err := mergeEvalInto(ctx, dr.d.Eval, inv, dr.d.Now()); err != nil {
		dr.logErr("değerlendirme birleştirmesi", err)
	}
}

// ── okuma ───────────────────────────────────────────────────────────────────

// LoadEvalRun — koşu + uzlaştırma (bitmemişse tekrar satırları okunur).
func LoadEvalRun(ctx context.Context, st EvalReadStore, runID string, now time.Time) (EvalRun, bool, error) {
	v, err := st.GetSREEvalRun(ctx, runID)
	if err != nil || v == nil {
		return EvalRun{}, false, err
	}
	run, ok := decodeEvalRun(*v)
	if !ok {
		return EvalRun{}, false, nil
	}
	if run.State != EvalDone {
		rep, err := st.ListSREReplayRows(ctx, run.RunID, chstore.SREReplayRowsMax)
		if err != nil {
			return EvalRun{}, false, err
		}
		run = reconcileEvalRun(run, rep, now)
	}
	return run, true, nil
}

// ListEvalRuns — en yeni koşular; bitmemiş en çok 3 koşu uzlaştırılır.
func ListEvalRuns(ctx context.Context, st EvalReadStore, limit int, now time.Time) ([]EvalRun, error) {
	rows, err := st.ListSREEvalRuns(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]EvalRun, 0, len(rows))
	reconciled := 0
	for _, r := range rows {
		run, ok := decodeEvalRun(r)
		if !ok {
			continue
		}
		if run.State != EvalDone && reconciled < 3 {
			reconciled++
			rep, err := st.ListSREReplayRows(ctx, run.RunID, chstore.SREReplayRowsMax)
			if err != nil {
				return nil, err
			}
			run = reconcileEvalRun(run, rep, now)
		}
		out = append(out, run)
	}
	return out, nil
}

// LiveSample — bir canlı araştırmanın ilk hipoteze varış süresi.
type LiveSample struct {
	ProblemID string `json:"problemId"`
	TTFHMs    int64  `json:"ttfhMs"`
}

// liveSamples — SAF: canlı kayıtlar (tekrar / paylaşılan / hipotezsiz hariç)
// → ilk hipoteze varış. FirstHypothesisAt yoksa (v0.10.1301 öncesi kayıt)
// yalnız ilk koşunun bitişi kullanılır (yeniden koşu bitişi ilk varış değil).
func liveSamples(invs []Investigation) []LiveSample {
	var out []LiveSample
	for _, inv := range invs {
		if inv.Kind == KindReplay || inv.State == StateShared || len(inv.Hypotheses) == 0 || inv.ProblemAt <= 0 {
			continue
		}
		at := inv.FirstHypothesisAt
		if at == 0 && inv.Run <= 1 {
			at = inv.FinishedAt
		}
		if at <= 0 || at < inv.ProblemAt {
			continue
		}
		out = append(out, LiveSample{ProblemID: inv.ProblemID, TTFHMs: (at - inv.ProblemAt) / int64(time.Millisecond)})
	}
	return out
}

// LoadLiveSamples — en yeni ≤200 canlı (done / partial) araştırmanın örneklemi.
func LoadLiveSamples(ctx context.Context, st EvalReadStore) ([]LiveSample, error) {
	rows, err := st.ListRecentSREInvestigations(ctx, []string{StateDone, StatePartial}, evalLiveSample)
	if err != nil {
		return nil, err
	}
	invs := make([]Investigation, 0, len(rows))
	for _, r := range rows {
		if inv, ok := Decode(r); ok {
			invs = append(invs, inv)
		}
	}
	return liveSamples(invs), nil
}

// ── puanlama (SAF) ──────────────────────────────────────────────────────────

// evalInput — scoreEval'in TEK girdisi: vakalar (beklenen = onaylı vaka,
// gözlenen = tekrarın hipotez sırası) + canlı varış örneklemi. Oy / geri
// bildirim / insan işareti alanı YOK ve olmayacak (TestEvalIgnoresFeedback).
// SuggestionKnown — vakaların öneri bayrağı hesaplandı mı (koşu satırından).
type evalInput struct {
	Cases           []EvalCase
	Live            []LiveSample
	SuggestionKnown bool
}

// EvalLive — canlı koşularda ilk hipoteze varış.
type EvalLive struct {
	Runs  int    `json:"runs"`
	P50Ms *int64 `json:"p50Ms"`
	P90Ms *int64 `json:"p90Ms"`
}

// EvalScore — koşunun ölçüleri. Oran alanları payda 0 iken null.
type EvalScore struct {
	Cases    int `json:"cases"`
	Pending  int `json:"pending"`  // tekrar sürüyor
	Unmapped int `json:"unmapped"` // kategori eşlenmiyor — isabet dışı
	Skipped  int `json:"skipped"`
	Errored  int `json:"errored"` // tekrar tamamlanamadı
	NoData   int `json:"noData"`  // tekrar bitti ama hiçbir telemetri okuyucusu veri döndürmedi
	// Scored — isabet paydası: eşlenen kategori + biten tekrar + kapsam > 0.
	Scored       int      `json:"scored"`
	Top1Hits     int      `json:"top1Hits"`
	Top3Hits     int      `json:"top3Hits"`
	Top1Acc      *float64 `json:"top1Acc"`
	Top3Acc      *float64 `json:"top3Acc"`
	EntityScored int      `json:"entityScored"` // en olası hipotezin öznesi belli olan puanlı vaka
	EntityHits   int      `json:"entityHits"`
	EntityAcc    *float64 `json:"entityAcc"`
	// CoveragePct — biten tekrarlarda veri döndüren telemetri adımı payı (%).
	CoveragePct *float64 `json:"coveragePct"`
	Live        EvalLive `json:"live"`
	// v0.10.1450 — "bağımsız" vakalar (operatör kararı 2: MANŞET). Onaylayan
	// CoSRE'nin önerisini değiştirmeden kabul ettiyse (AcceptedSuggestion)
	// etiket ajandan bağımsız değildir; bu alt küme yalnız öteki vakalardır.
	// AcceptedSuggestion = bayraklı vaka sayısı (tümü); Independent* puanlı
	// alt küme. Bayrak bilinmiyorsa (eski koşu) Independent*Acc null.
	AcceptedSuggestion  int      `json:"acceptedSuggestion"`
	IndependentScored   int      `json:"independentScored"`
	IndependentTop1Hits int      `json:"independentTop1Hits"`
	IndependentTop3Hits int      `json:"independentTop3Hits"`
	IndependentTop1Acc  *float64 `json:"independentTop1Acc"`
	IndependentTop3Acc  *float64 `json:"independentTop3Acc"`
}

func ratio(n, d int) *float64 {
	if d <= 0 {
		return nil
	}
	v := math.Round(float64(n)/float64(d)*10000) / 10000
	return &v
}

// percentileMs — SAF: en-yakın-sıra yüzdeliği (sıralı kopya üzerinde).
func percentileMs(v []int64, p float64) *int64 {
	if len(v) == 0 {
		return nil
	}
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	k := int(math.Ceil(p*float64(len(s)))) - 1
	if k < 0 {
		k = 0
	}
	if k >= len(s) {
		k = len(s) - 1
	}
	out := s[k]
	return &out
}

// normEntity — SAF: küçük harf, düğüm öneki (db: / ext: / q:) düşer, harf /
// rakam dışı her dizi tek boşluk ("payment-service'in" → "payment service in").
func normEntity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"db:", "ext:", "q:"} {
		s = strings.TrimPrefix(s, p)
	}
	var b strings.Builder
	space := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// entityMatch — SAF: özne, normalize FinalCause'da sözcük sınırıyla geçiyor mu.
func entityMatch(subject, cause string) bool {
	s := normEntity(subject)
	if s == "" {
		return false
	}
	return strings.Contains(" "+normEntity(cause)+" ", " "+s+" ")
}

// scoreEval — SAF puanlama. Yalnız evalInput'u okur.
func scoreEval(in evalInput) EvalScore {
	sc := EvalScore{Cases: len(in.Cases)}
	data, total := 0, 0
	for _, c := range in.Cases {
		if c.AcceptedSuggestion {
			sc.AcceptedSuggestion++
		}
		switch c.State {
		case CaseUnmapped:
			sc.Unmapped++
			continue
		case CaseSkipped:
			sc.Skipped++
			continue
		case StateError:
			sc.Errored++
			if c.Coverage != nil {
				data += c.Coverage.Data
				total += c.Coverage.Data + c.Coverage.NoData + c.Coverage.Failed
			}
			continue
		case StateDone, StatePartial:
		default:
			sc.Pending++
			continue
		}
		if c.Coverage != nil {
			data += c.Coverage.Data
			total += c.Coverage.Data + c.Coverage.NoData + c.Coverage.Failed
		}
		if c.Coverage == nil || c.Coverage.Data == 0 || len(c.Top) == 0 {
			sc.NoData++
			continue
		}
		sc.Scored++
		top1, top3 := c.Top[0].Category == c.Category, false
		for i, h := range c.Top {
			if i >= evalTopN {
				break
			}
			if h.Category == c.Category {
				top3 = true
				break
			}
		}
		if top1 {
			sc.Top1Hits++
		}
		if top3 {
			sc.Top3Hits++
		}
		if !c.AcceptedSuggestion {
			sc.IndependentScored++
			if top1 {
				sc.IndependentTop1Hits++
			}
			if top3 {
				sc.IndependentTop3Hits++
			}
		}
		if subj := c.Top[0].Subject; subj != "" {
			sc.EntityScored++
			if entityMatch(subj, c.FinalCause) {
				sc.EntityHits++
			}
		}
	}
	sc.Top1Acc, sc.Top3Acc = ratio(sc.Top1Hits, sc.Scored), ratio(sc.Top3Hits, sc.Scored)
	sc.EntityAcc = ratio(sc.EntityHits, sc.EntityScored)
	if in.SuggestionKnown {
		sc.IndependentTop1Acc, sc.IndependentTop3Acc = ratio(sc.IndependentTop1Hits, sc.IndependentScored), ratio(sc.IndependentTop3Hits, sc.IndependentScored)
	}
	if total > 0 {
		v := math.Round(float64(data)/float64(total)*1000) / 10
		sc.CoveragePct = &v
	}
	ms := make([]int64, 0, len(in.Live))
	for _, l := range in.Live {
		ms = append(ms, l.TTFHMs)
	}
	sc.Live = EvalLive{Runs: len(ms), P50Ms: percentileMs(ms, 0.5), P90Ms: percentileMs(ms, 0.9)}
	return sc
}

// ScoreEvalRun — dışa açık sarmalayıcı (api ucu): koşunun vakaları + canlı
// örneklem → ölçüler.
func ScoreEvalRun(run EvalRun, live []LiveSample) EvalScore {
	return scoreEval(evalInput{Cases: run.Cases, Live: live, SuggestionKnown: run.SuggestionKnown})
}

// EvalUnmappedReason — kategorinin eşlenmeme nedeni ("" = eşleniyor).
func EvalUnmappedReason(cat string) string { return evalUnmappedCategories[cat] }

// EvalCategoryMap — tür → kategori (FE "beklenen kategori vs hipotez" etiketi).
func EvalCategoryMap() map[string]string { return hypKindCategory() }
