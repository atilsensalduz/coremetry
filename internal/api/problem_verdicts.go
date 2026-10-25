package api

// problem_verdicts.go — v0.10.1015 — Problems sekmesinde ÖĞRETME (operatör:
// "bütün hepsi gelsin, ben hangisi gerçek problem hangisi değil zamanla
// öğretelim"). Depolama ve kavram: internal/chstore/problem_verdict.go.
//
//	GET /api/problem-verdicts         her rol — tüm kararlar (≤5000) + politika
//	PUT /api/problem-verdicts         editor+ — {signature, verdict, label?, kind?, service?,
//	                                            reason?, note?, permanent?, priority?, magnitude?}
//	                                            verdict: "real" | "noise" | "" (kaldır)
//	                                            v0.10.1531 (R3): reason/note/permanent yalnız
//	                                            "noise"te; permanent=true YALNIZ admin (403)
//	PUT /api/problem-verdicts/policy  admin   — {muteNotifications} (v0.10.1016)
//
// Karar İMZAYA bağlıdır (aynı kural + servis / aynı exception grubu); FE imzayı
// üretir, sunucu biçimini doğrular. Varsayılan yalnız GÖRÜNÜM: Problem yaşam
// döngüsüne ve dedektörlere dokunmaz; bildirimlere de — yönetici politikada
// muteNotifications'ı açmadıkça (v0.10.1016, kapı: internal/notify/
// verdict_silence.go; varsayılan KAPALI). Viewer kararları GÖRÜR (salt okuma),
// yazamaz. Yazım denetim kaydına "problem.verdict" olarak düşer ve okuma
// önbelleğini düşürür; PUT cevabı taze listenin tamamıdır (FE onu doğrudan
// kullanır — yazdığını bir sonraki okumada bayat önbellekten geri kaybetmez).

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("problem-verdicts", (*Server).registerProblemVerdictRoutes) }

func (s *Server) registerProblemVerdictRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/problem-verdicts", s.getProblemVerdicts)
	mux.HandleFunc("PUT /api/problem-verdicts", auth.RequireAnyRole(editorRoles, s.putProblemVerdict))
	mux.HandleFunc("PUT /api/problem-verdicts/policy", auth.RequireRole(auth.RoleAdmin, s.putProblemVerdictPolicy))
}

const (
	problemVerdictCacheKey = "problem-verdicts:all"
	problemVerdictCachePfx = "problem-verdicts:"
	problemVerdictLabelMax = 200 // rune
	problemVerdictFieldMax = 120 // rune (kind / service)
	problemVerdictMaxBody  = 8 << 10
	problemVerdictNoteMax  = 280 // rune (v0.10.1531)
)

type problemVerdictsResponse struct {
	Verdicts []chstore.ProblemVerdict     `json:"verdicts"`
	Policy   chstore.ProblemVerdictPolicy `json:"policy"` // v0.10.1016
}

// problemVerdictPolicyInput — muteNotifications ZORUNLU (işaretçi: eksik alan
// "kapat" diye okunmasın).
type problemVerdictPolicyInput struct {
	MuteNotifications *bool `json:"muteNotifications"`
}

// problemVerdictsPayload — okuma cevabı: kararlar + politika.
func (s *Server) problemVerdictsPayload(ctx context.Context) (problemVerdictsResponse, error) {
	list, err := s.store.ListProblemVerdicts(ctx)
	if err != nil {
		return problemVerdictsResponse{}, err
	}
	pol, err := s.store.GetProblemVerdictPolicy(ctx)
	if err != nil {
		return problemVerdictsResponse{}, err
	}
	return problemVerdictsResponse{Verdicts: list, Policy: pol}, nil
}

type problemVerdictInput struct {
	Signature string `json:"signature"`
	Verdict   string `json:"verdict"` // real | noise | "" (kaldır)
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Service   string `json:"service"`
	// v0.10.1531 (Problem standardı R3, D5).
	Reason    string  `json:"reason"`
	Note      string  `json:"note"`
	Permanent bool    `json:"permanent"`
	Priority  string  `json:"priority"`
	Magnitude float64 `json:"magnitude"`
}

// normalizeProblemVerdictInput — SAF (tablo testli): gövde → yazılacak karar.
// clear=true: karar kaldırılır. Hata mesajı Türkçe ve alanı söyler.
// v0.10.1531 (R3): gerekçe kapalı kümeden (chstore.NoiseReasons) ya da boş;
// "other" not ister; gerekçe/not/kalıcılık yalnız "noise"te (gerçek kararda
// sessizce düşer); kalıcılık yönetici yetkisi (isAdmin) ister — rol kararı
// burada ki tablo testi kapıyı da pinlesin. Öncelik yalnız P1/P2/P3,
// büyüklük sonlu ve ≥ 0 (baypas kıyası; bozuk değer 0 = bilinmiyor).
func normalizeProblemVerdictInput(in problemVerdictInput, by string, now time.Time, isAdmin ...bool) (v chstore.ProblemVerdict, clear bool, msg string) {
	sig := strings.TrimSpace(in.Signature)
	if !chstore.ValidProblemSignature(sig) {
		return v, false, "signature geçersiz: p: (alarm kuralı), e: (exception grubu) ya da a: (anomali) önekiyle başlamalı"
	}
	verdict := strings.TrimSpace(in.Verdict)
	if verdict == "" {
		return chstore.ProblemVerdict{Signature: sig}, true, ""
	}
	if !chstore.ValidProblemVerdict(verdict) {
		return v, false, "verdict 'real', 'noise' ya da boş (kaldır) olmalı"
	}
	out := chstore.ProblemVerdict{
		Signature: sig, Verdict: verdict,
		Label:   truncRunes(strings.TrimSpace(in.Label), problemVerdictLabelMax),
		Kind:    truncRunes(strings.TrimSpace(in.Kind), problemVerdictFieldMax),
		Service: truncRunes(strings.TrimSpace(in.Service), problemVerdictFieldMax),
		By:      by, At: now.UnixNano(),
	}
	switch p := strings.TrimSpace(in.Priority); p {
	case "P1", "P2", "P3":
		out.Priority = p
	}
	if m := in.Magnitude; m > 0 && !math.IsInf(m, 0) && !math.IsNaN(m) {
		out.Magnitude = m
	}
	if verdict != chstore.ProblemVerdictNoise {
		return out, false, ""
	}
	reason := strings.TrimSpace(in.Reason)
	if !chstore.ValidNoiseReason(reason) {
		return v, false, "reason geçersiz: " + strings.Join(chstore.NoiseReasons, " | ") + " ya da boş olmalı"
	}
	note := truncRunes(strings.TrimSpace(in.Note), problemVerdictNoteMax)
	if reason == chstore.NoiseReasonOther && note == "" {
		return v, false, "reason 'other' bir not (note) ister"
	}
	admin := len(isAdmin) > 0 && isAdmin[0]
	if in.Permanent && !admin {
		return v, false, "permanent (kalıcı gizleme) yalnız yönetici"
	}
	out.Reason, out.Note, out.Permanent = reason, note, in.Permanent
	return out, false, ""
}

func (s *Server) getProblemVerdicts(w http.ResponseWriter, r *http.Request) {
	s.serveCached(w, r, problemVerdictCacheKey, 10*time.Second, func(ctx context.Context) (any, error) {
		res, err := s.problemVerdictsPayload(ctx)
		if err != nil {
			return nil, err
		}
		return res, nil
	})
}

func (s *Server) putProblemVerdict(w http.ResponseWriter, r *http.Request) {
	var in problemVerdictInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, problemVerdictMaxBody)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	claims := auth.FromContext(r.Context())
	isAdmin := claims != nil && claims.Role == auth.RoleAdmin
	v, clear, msg := normalizeProblemVerdictInput(in, claimEmail(claims), time.Now(), isAdmin)
	if msg != "" {
		code := http.StatusBadRequest
		if in.Permanent && !isAdmin {
			code = http.StatusForbidden
		}
		writeJSONError(w, code, msg)
		return
	}
	ctx := r.Context()
	if clear {
		if err := s.store.ClearProblemVerdict(ctx, v.Signature); err != nil {
			writeErr(w, err)
			return
		}
	} else {
		// Tavan: yeni bir imza 5000'i aşıracaksa reddet (mevcut imzanın
		// kararını değiştirmek her zaman serbest).
		cur, err := s.store.ListProblemVerdicts(ctx)
		if err != nil {
			writeErr(w, err)
			return
		}
		known := false
		for _, c := range cur {
			if c.Signature == v.Signature {
				known = true
				break
			}
		}
		if !known && len(cur) >= chstore.ProblemVerdictMax {
			writeJSONError(w, http.StatusBadRequest, "karar tavanı doldu (5000 imza) — eski kararları kaldırın")
			return
		}
		if err := s.store.SetProblemVerdict(ctx, v); err != nil {
			writeErr(w, err)
			return
		}
	}
	s.cacheInvalidatePrefix(ctx, problemVerdictCachePfx)
	details, _ := json.Marshal(map[string]any{"signature": v.Signature, "verdict": v.Verdict, "kind": v.Kind, "service": v.Service, "label": v.Label,
		"reason": v.Reason, "note": v.Note, "permanent": v.Permanent}) // v0.10.1531 — gerekçe + kalıcılık denetimde
	s.audit(r, "problem.verdict", "problem", v.Signature, string(details))
	res, err := s.problemVerdictsPayload(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Yeni yazılan satır okumaya henüz yansımadıysa (eşzamansız ekleme) cevabı
	// yazılanla uzlaştır: FE yazdığını geri kaybetmesin.
	res.Verdicts = reconcileProblemVerdicts(res.Verdicts, v, clear)
	writeJSON(w, res)
}

// putProblemVerdictPolicy — v0.10.1016 — "problem değil" bildirimi de sustursun
// mu. Yalnız admin; denetim kaydı "problem.verdict.policy". Bildirim tarafı
// politikayı 30 sn önbellekler — değişiklik en geç o kadar sonra etkir.
func (s *Server) putProblemVerdictPolicy(w http.ResponseWriter, r *http.Request) {
	var in problemVerdictPolicyInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, problemVerdictMaxBody)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if in.MuteNotifications == nil {
		writeJSONError(w, http.StatusBadRequest, "muteNotifications zorunlu (true | false)")
		return
	}
	ctx := r.Context()
	pol := chstore.ProblemVerdictPolicy{
		MuteNotifications: *in.MuteNotifications,
		UpdatedBy:         claimEmail(auth.FromContext(ctx)),
		UpdatedAt:         time.Now().UnixNano(),
	}
	if err := s.store.PutProblemVerdictPolicy(ctx, pol); err != nil {
		writeErr(w, err)
		return
	}
	s.cacheInvalidatePrefix(ctx, problemVerdictCachePfx)
	details, _ := json.Marshal(map[string]bool{"muteNotifications": pol.MuteNotifications})
	s.audit(r, "problem.verdict.policy", "settings", chstore.ProblemVerdictPolicyKey, string(details))
	res, err := s.problemVerdictsPayload(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	res.Policy = pol // yazılan kazanır (okuma henüz görmediyse)
	writeJSON(w, res)
}

// reconcileProblemVerdicts — SAF: okunan liste + az önce yazılan karar → cevap.
// Okuma yazımı henüz görmediyse (ya da eskisini gördüyse) yazılan kazanır.
func reconcileProblemVerdicts(list []chstore.ProblemVerdict, written chstore.ProblemVerdict, cleared bool) []chstore.ProblemVerdict {
	out := make([]chstore.ProblemVerdict, 0, len(list)+1)
	if !cleared {
		out = append(out, written)
	}
	for _, v := range list {
		if v.Signature != written.Signature {
			out = append(out, v)
		}
	}
	return out
}
