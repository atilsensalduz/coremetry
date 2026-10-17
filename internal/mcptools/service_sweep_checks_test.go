package mcptools

// v0.10.1287 — AI SRE Faz 1b: tarama ek kontrolleri (deploys + k8s) ve
// get_problem'in kayıtlı araştırma alanı.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

func TestClassifyDeploys(t *testing.T) {
	anchor := time.Date(2026, 10, 17, 14, 0, 0, 0, time.UTC)
	at := func(d time.Duration) int64 { return anchor.Add(d).UnixNano() }
	rows := []ChangeRow{
		{Source: "rollout", TimeUnixNs: at(-12 * time.Minute), Workload: "checkout", Version: "rev 42", PrevVersion: "rev 41", Status: "completed"},
		{Source: "inferred", TimeUnixNs: at(3 * time.Minute), Service: "checkout", Version: "1.4.2"},
		{Source: "event", TimeUnixNs: at(-90 * time.Minute), Service: "checkout", Version: "1.4.0"},                          // uzak → yok
		{Source: "rollout", TimeUnixNs: at(-100 * time.Minute), Workload: "checkout", Version: "v9", Status: "stalled"},      // uzak ama başarısız → kritik
		{Source: "inferred", TimeUnixNs: at(10 * time.Minute), Service: "checkout", Version: "1.4.3"},                        // +10 dk → yok
		{Source: "rollout", TimeUnixNs: at(-12 * time.Minute), Workload: "checkout", Version: "rev 42", Status: "completed"}, // tekrar
	}
	got := classifyDeploys(rows, anchor)
	if len(got) != 3 {
		t.Fatalf("aday sayısı %d: %+v", len(got), got)
	}
	if got[0].Name != "checkout@rev 42" || got[0].Level != levelWarn ||
		got[0].Reasons[0] != "başlangıçtan 12 dk önce rollout (rev 42←rev 41)" {
		t.Fatalf("rollout gerekçesi: %+v", got[0])
	}
	if got[1].Reasons[0] != "başlangıçtan 3 dk sonra sürüm geçişi (1.4.2)" || got[1].Level != levelWarn {
		t.Fatalf("inferred: %+v", got[1])
	}
	if got[2].Level != levelCrit || got[2].Reasons[1] != "rollout stalled" {
		t.Fatalf("başarısız rollout kritik olmalı: %+v", got[2])
	}
	// Yakınlık etkisi: 3 dk sonra > 12 dk önce.
	if !(got[1].Impact > got[0].Impact) {
		t.Fatalf("yakınlık etkisi: %v vs %v", got[1].Impact, got[0].Impact)
	}
	if deployRelTR(anchor, anchor) != "başlangıçla aynı dakikada" {
		t.Fatal(deployRelTR(anchor, anchor))
	}
	if len(classifyDeploys(nil, anchor)) != 0 {
		t.Fatal("boş girdi")
	}
}

func TestClassifyK8s(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		name  string
		s     K8sPodState
		level string
		why   string
	}{
		{"temiz", K8sPodState{Pod: "p", Phase: "Running"}, levelOK, ""},
		{"CrashLoopBackOff", K8sPodState{Pod: "p", Phase: "Running", WaitingReason: "CrashLoopBackOff"}, levelCrit, "bekliyor: CrashLoopBackOff"},
		{"ImagePullBackOff", K8sPodState{Pod: "p", Phase: "Pending", WaitingReason: "ImagePullBackOff"}, levelCrit, "bekliyor: ImagePullBackOff"},
		{"ErrImagePull", K8sPodState{Pod: "p", WaitingReason: "ErrImagePull"}, levelCrit, "ErrImagePull"},
		{"CreateContainerConfigError", K8sPodState{Pod: "p", WaitingReason: "CreateContainerConfigError"}, levelCrit, "CreateContainerConfigError"},
		{"ContainerCreating olağan", K8sPodState{Pod: "p", Phase: "Running", WaitingReason: "ContainerCreating"}, levelOK, ""},
		{"pencerede OOM", K8sPodState{Pod: "p", Phase: "Running", LastTermReason: "OOMKilled", RestartsWindow: f(2)}, levelCrit, "pencerede OOMKilled (2 restart)"},
		{"eski OOM yok sayılır", K8sPodState{Pod: "p", Phase: "Running", LastTermReason: "OOMKilled", RestartsWindow: f(0)}, levelOK, ""},
		{"restart okunamadı → OOM puanlanmaz", K8sPodState{Pod: "p", Phase: "Running", LastTermReason: "OOMKilled"}, levelOK, ""},
		{"Pending uyarı", K8sPodState{Pod: "p", Phase: "Pending"}, levelWarn, "faz Pending"},
		{"Failed uyarı", K8sPodState{Pod: "p", Phase: "Failed"}, levelWarn, "faz Failed"},
	} {
		lvl, why, _ := classifyK8s(c.s)
		if lvl != c.level || (c.why != "" && !strings.Contains(strings.Join(why, ";"), c.why)) {
			t.Errorf("%s: %s %v", c.name, lvl, why)
		}
	}
}

// TestSweepChecksSections — standart kontroller: deploy bölümü + k8s bölümü;
// aynı pod'un k8s gerekçesi pod bulgusuna katılır, yeni pod k8s bulgusu olur.
func TestSweepChecksSections(t *testing.T) {
	r := baseSweepReaders()
	anchor := sweepTo.Add(-20 * time.Minute)
	r.deploys = func(_ context.Context, svc string, from, to time.Time) ([]ChangeRow, string, error) {
		if svc != "checkout" || !from.Equal(anchor.Add(-2*time.Hour)) {
			t.Errorf("deploy okuması: %s %v", svc, from)
		}
		return []ChangeRow{{Source: "rollout", TimeUnixNs: anchor.Add(-5 * time.Minute).UnixNano(), Workload: "checkout", Version: "rev 7", PrevVersion: "rev 6"}}, "ok", nil
	}
	var asked ServicePodsOpts
	inner := r.servicePods
	r.servicePods = func(ctx context.Context, svc string, refs []string, from, to time.Time, opt ServicePodsOpts) (ServicePodsResult, error) {
		asked = opt
		res, err := inner(ctx, svc, refs, from, to, opt)
		res.Clusters[0].Rows = append(res.Clusters[0].Rows, thanos.PodRow{Namespace: "shop", Pod: "checkout-3", Phase: "Running"})
		res.Clusters[0].Waiting = map[string]string{"shop\x00checkout-3": "CrashLoopBackOff", "shop\x00checkout-2": "CrashLoopBackOff"}
		res.Clusters[0].WaitingOK = true
		return res, err
	}
	req := sweepReq()
	req.Anchor = anchor
	req.TopN = 8
	res, err := runServiceSweep(context.Background(), r, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !asked.WaitingReasons {
		t.Fatal("k8s kontrolü bekleme sebebini istemedi")
	}
	dep := sectionByName(res, sweepHopDeploys)
	if dep.State != hopOK || !strings.Contains(dep.Summary, "checkout@rev 7") {
		t.Fatalf("deploy bölümü: %+v", dep)
	}
	k := sectionByName(res, sweepHopK8s)
	if k.State != hopOK || !strings.Contains(k.Summary, "2× CrashLoopBackOff") {
		t.Fatalf("k8s bölümü: %+v", k)
	}
	var pod2, pod3, deploy *SweepFinding
	for i := range res.Findings {
		f := &res.Findings[i]
		switch {
		case f.Name == "checkout-2":
			pod2 = f
		case f.Name == "checkout-3":
			pod3 = f
		case f.Kind == sweepKindDeploy:
			deploy = f
		}
	}
	if pod2 == nil || pod2.Kind != sweepKindPod || !strings.Contains(strings.Join(pod2.Reasons, ";"), "bekliyor: CrashLoopBackOff") {
		t.Fatalf("checkout-2 k8s gerekçesini pod bulgusunda taşımalı: %+v", pod2)
	}
	if strings.Count(strings.Join(pod2.Reasons, ";"), "OOMKilled") != 1 {
		t.Fatalf("OOM gerekçesi tekrarlandı: %v", pod2.Reasons)
	}
	if pod3 == nil || pod3.Kind != sweepKindK8s || pod3.Level != levelCrit {
		t.Fatalf("checkout-3 k8s bulgusu: %+v", pod3)
	}
	if deploy == nil || deploy.Level != levelWarn || (deploy.Logs != nil && deploy.Logs.State != hopSkipped) {
		t.Fatalf("deploy bulgusu (log okunmaz): %+v", deploy)
	}
	if res.Partial {
		t.Fatalf("temiz okumada kısmi olmamalı: %+v", res.Sections)
	}

	// Checks boş dilim → kontrol yok; nil → standart.
	req.Checks = []string{}
	res2, _ := runServiceSweep(context.Background(), r, req, nil)
	if sectionByName(res2, sweepHopDeploys).Name != "" || sectionByName(res2, sweepHopK8s).Name != "" {
		t.Fatal("Checks=[] iken ek bölüm koştu")
	}
	// Bekleme sebebi okunamadı → k8s kısmi (okunamadı ≠ temiz).
	r.servicePods = func(ctx context.Context, svc string, refs []string, from, to time.Time, opt ServicePodsOpts) (ServicePodsResult, error) {
		return inner(ctx, svc, refs, from, to, opt)
	}
	req.Checks = []string{SweepCheckK8s}
	res3, _ := runServiceSweep(context.Background(), r, req, nil)
	if s := sectionByName(res3, sweepHopK8s); s.State != hopPartial || !strings.Contains(s.Note, "bekleme sebebi okunamadı") || !res3.Partial {
		t.Fatalf("okunamayan bekleme sebebi: %+v partial=%v", s, res3.Partial)
	}
	if sectionByName(res3, sweepHopDeploys).Name != "" {
		t.Fatal("yalnız k8s istendi")
	}
}

func TestReadDeployCheck(t *testing.T) {
	anchor := sweepTo
	r := sweepReaders{deploys: func(context.Context, string, time.Time, time.Time) ([]ChangeRow, string, error) {
		return nil, "", errors.New("ch: connection refused")
	}}
	dc, err := readDeployCheck(context.Background(), r, "checkout", anchor, anchor)
	if err != nil || dc.Section.State != hopError || len(dc.Findings) != 0 || len(dc.Evidences) != 1 {
		t.Fatalf("hata bölüme çevrilmeli: %+v %v", dc, err)
	}
	r.deploys = func(context.Context, string, time.Time, time.Time) ([]ChangeRow, string, error) {
		return []ChangeRow{{Source: "inferred", TimeUnixNs: anchor.Add(-time.Minute).UnixNano(), Service: "checkout", Version: "2.0"}}, "unavailable", nil
	}
	dc, _ = readDeployCheck(context.Background(), r, "checkout", anchor, anchor)
	if dc.Section.State != hopOK || len(dc.Findings) != 1 || dc.Findings[0].EvidenceRefs[0] != "evidences[0]" || len(dc.Rows) != 1 ||
		!strings.Contains(dc.Section.Note, "rollouts katmanı") {
		t.Fatalf("deploy kontrolü: %+v", dc)
	}
	dc, _ = readDeployCheck(context.Background(), sweepReaders{}, "checkout", anchor, anchor)
	if dc.Section.State != hopSkipped {
		t.Fatalf("okuyucusuz: %+v", dc.Section)
	}
}

func TestScoreFindingsKinds(t *testing.T) {
	in := []sweepCand{
		{Kind: sweepKindOperation, Name: "op", Level: levelCrit, Impact: 1},
		{Kind: sweepKindDeploy, Name: "d", Level: levelCrit, Impact: 1},
		{Kind: sweepKindK8s, Name: "k", Level: levelCrit, Impact: 1},
		{Kind: sweepKindPod, Name: "p", Level: levelCrit, Impact: 1},
		{Kind: sweepKindK8s, Name: "kw", Level: levelWarn, Impact: 9},
	}
	got, _ := scoreFindings(in, 8)
	order := []string{}
	for _, f := range got {
		order = append(order, f.Name)
	}
	if strings.Join(order, ",") != "k,d,p,op,kw" {
		t.Fatalf("sıra: %v", order)
	}
}

func TestSweepChecksKey(t *testing.T) {
	base := sweepReq()
	k0 := sweepCacheKey(base, nil, chstore.DefaultRuntimeAlerts())
	for name, m := range map[string]func(*SweepRequest){
		"checks boş":    func(r *SweepRequest) { r.Checks = []string{} },
		"checks k8s":    func(r *SweepRequest) { r.Checks = []string{SweepCheckK8s} },
		"anchor":        func(r *SweepRequest) { r.Anchor = r.To.Add(-time.Hour) },
		"checks deploy": func(r *SweepRequest) { r.Checks = []string{SweepCheckDeploys} },
	} {
		r := base
		m(&r)
		if sweepCacheKey(r, nil, chstore.DefaultRuntimeAlerts()) == k0 {
			t.Errorf("%s anahtara girmiyor", name)
		}
	}
	a, b := base, base
	a.Checks = []string{SweepCheckK8s, SweepCheckDeploys}
	b.Checks = []string{SweepCheckDeploys, SweepCheckK8s}
	if sweepCacheKey(a, nil, chstore.DefaultRuntimeAlerts()) != sweepCacheKey(b, nil, chstore.DefaultRuntimeAlerts()) {
		t.Error("kontrol sırası anahtara sızıyor")
	}
}

// TestGetProblemIncludesInvestigation — get_problem kayıtlı araştırmayı
// (≤3 bulgu, özet) taşır; okuyucu yok / kayıt yok / hata → alan yok.
func TestGetProblemIncludesInvestigation(t *testing.T) {
	at := time.Date(2026, 10, 17, 14, 5, 0, 0, time.UTC).UnixNano()
	d := Deps{Investigation: func(_ context.Context, id string) (*InvestigationSummary, error) {
		switch id {
		case "p1":
			return &InvestigationSummary{At: at, State: "done", Findings: []string{"a", "b", "c", "d"}, Summary: "Ne oldu: x",
				// v0.10.1293 — öneriler sohbete ≤2.
				Recommendations: []InvestigationRecommendation{{Text: "r1", State: "öneri — uygulanmadı"}, {Text: "r2"}, {Text: "r3"}}}, nil
		case "p2":
			return nil, errors.New("ch down")
		}
		return nil, nil
	}}
	got, ok := problemInvestigationField(context.Background(), d, "p1")
	if !ok || got.State != "done" || len(got.Findings) != 3 || got.Summary != "Ne oldu: x" || got.AtISO != "2026-10-17T14:05:00Z" ||
		len(got.Recommendations) != 2 || got.Recommendations[0].Text != "r1" {
		t.Fatalf("alan: %+v %v", got, ok)
	}
	for _, id := range []string{"p2", "p3", ""} {
		if _, ok := problemInvestigationField(context.Background(), d, id); ok {
			t.Errorf("%q alan üretmemeli", id)
		}
	}
	if _, ok := problemInvestigationField(context.Background(), Deps{}, "p1"); ok {
		t.Error("okuyucusuz alan üretmemeli")
	}
	// Kablolama: get_problem işleyicisi alanı ekliyor.
	src, err := os.ReadFile("problem_tools.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `out["investigation"] = inv`) {
		t.Fatal("get_problem investigation alanını eklemiyor")
	}
}
