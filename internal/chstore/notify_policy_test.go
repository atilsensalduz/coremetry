package chstore

// notify_policy_test.go — v0.10.1523 (Problem standardı R1): NotifiableAt tablosu
// (D2/D3/D5/D6/D10), politika blobu doğrulama/normalize ve görünüm önceliği.

import (
	"strings"
	"testing"
	"time"
)

func npStd(family, conf string, fl StdFloorMeasures, ratio float64) ProblemStandard {
	return ProblemStandard{Family: family,
		Impact:     StdImpact{Primary: StdMeasure{Ratio: ratio, Value: ratio}, Known: true, Floor: fl},
		Confidence: StdConfidence{Level: conf, Gates: []SignalGate{{ID: "G1", Status: GatePass}}}}
}

func TestNotifiableAtTable(t *testing.T) {
	now := time.Date(2026, 10, 24, 14, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) int64 { return now.Add(-d).UnixNano() }
	pol := DefaultNotifyPolicy()
	quiet := DefaultNotifyPolicy()
	quiet.QuietHours = &NotifyQuietHours{From: "22:00", To: "07:00"}
	night := time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC)
	p3imm := DefaultNotifyPolicy()
	p3imm.P3 = NotifyP3Immediate
	p3off := DefaultNotifyPolicy()
	p3off.P3 = NotifyP3Off
	lowStd := npStd(StdFamilyError, ConfidenceLow, StdFloorMeasures{}, 5)
	lowStd.Confidence.Gates = []SignalGate{{ID: "G1", Status: GatePass}, {ID: "G2", Status: GateFail}}

	cases := []struct {
		name   string
		std    ProblemStandard
		prio   string
		hist   NotifyHistory
		pol    NotifyPolicy
		at     time.Time
		ok     bool
		hold   string
		bypass string
	}{
		// D6 hata tabanı: sayı YA DA yüzde (yüzde kolu yalnız çağrı tabanı ölçülüyken).
		{"hata: 30 başarısız / 5 dk yeter", npStd(StdFamilyError, ConfidenceMedium, StdFloorMeasures{Failed5m: f64(30), ErrorPct: f64(0.2), Calls5m: f64(15000)}, 3), "P1", NotifyHistory{}, pol, now, true, "", ""},
		{"hata: %1 + ≥30 çağrı yeter (sayı az)", npStd(StdFamilyError, ConfidenceMedium, StdFloorMeasures{Failed5m: f64(3), ErrorPct: f64(10), Calls5m: f64(30)}, 3), "P1", NotifyHistory{}, pol, now, true, "", ""},
		{"hata: iki kol da tutmadı", npStd(StdFamilyError, ConfidenceMedium, StdFloorMeasures{Failed5m: f64(5), ErrorPct: f64(0.5), Calls5m: f64(1000)}, 3), "P1", NotifyHistory{}, pol, now, false, NotifyHoldImpactErr, ""},
		{"hata: asla yalnız yüzde (%50 ama 4 çağrı)", npStd(StdFamilyError, ConfidenceMedium, StdFloorMeasures{Failed5m: f64(2), ErrorPct: f64(50), Calls5m: f64(4)}, 3), "P1", NotifyHistory{}, pol, now, false, NotifyHoldImpactErr, ""},
		{"hata: yüzde var, trafik bilinmiyor → geçer", npStd(StdFamilyError, ConfidenceMedium, StdFloorMeasures{ErrorPct: f64(0.3)}, 3), "P1", NotifyHistory{}, pol, now, true, "", ""},
		{"hata: sayı az, çağrı bilinmiyor → geçer", npStd(StdFamilyError, ConfidenceMedium, StdFloorMeasures{Failed5m: f64(4), ErrorPct: f64(2)}, 3), "P1", NotifyHistory{}, pol, now, true, "", ""},
		// D6 gecikme: +300 ms VE ≥30 çağrı.
		{"gecikme: +300 ms ve 30 çağrı", npStd(StdFamilyLatency, ConfidenceMedium, StdFloorMeasures{P99DeltaMs: f64(300), Calls5m: f64(30)}, 3), "P2", NotifyHistory{}, pol, now, true, "", ""},
		{"gecikme: +250 ms", npStd(StdFamilyLatency, ConfidenceMedium, StdFloorMeasures{P99DeltaMs: f64(250), Calls5m: f64(5000)}, 3), "P2", NotifyHistory{}, pol, now, false, NotifyHoldImpactLat, ""},
		{"gecikme: +2 sn ama 10 çağrı", npStd(StdFamilyLatency, ConfidenceMedium, StdFloorMeasures{P99DeltaMs: f64(2000), Calls5m: f64(10)}, 3), "P2", NotifyHistory{}, pol, now, false, NotifyHoldImpactLat, ""},
		// D6 trafik düşüşü: ≥%40 VE ≥100 taban çağrı; artışa taban yok.
		{"trafik: %60 düşüş, 500 taban", npStd(StdFamilyTraffic, ConfidenceMedium, StdFloorMeasures{DropPct: f64(60), BaselineCalls5m: f64(500)}, 3), "P1", NotifyHistory{}, pol, now, true, "", ""},
		{"trafik: %30 düşüş", npStd(StdFamilyTraffic, ConfidenceMedium, StdFloorMeasures{DropPct: f64(30), BaselineCalls5m: f64(500)}, 3), "P1", NotifyHistory{}, pol, now, false, NotifyHoldImpactTrf, ""},
		{"trafik: %90 düşüş ama 50 taban", npStd(StdFamilyTraffic, ConfidenceMedium, StdFloorMeasures{DropPct: f64(90), BaselineCalls5m: f64(50)}, 3), "P1", NotifyHistory{}, pol, now, false, NotifyHoldImpactTrf, ""},
		{"trafik artışı tabansız", npStd(StdFamilyTraffic, ConfidenceMedium, StdFloorMeasures{}, 3), "P1", NotifyHistory{}, pol, now, true, "", ""},
		// D2 / D7: P3 → özet (varsayılan), kapalı ya da anında.
		{"P3 özet", npStd(StdFamilyRule, ConfidenceHigh, StdFloorMeasures{}, 3), "P3", NotifyHistory{}, pol, now, false, NotifyHoldP3Digest, ""},
		{"P3 kapalı", npStd(StdFamilyRule, ConfidenceHigh, StdFloorMeasures{}, 3), "P3", NotifyHistory{}, p3off, now, false, NotifyHoldP3Off, ""},
		{"P3 anında", npStd(StdFamilyRule, ConfidenceHigh, StdFloorMeasures{}, 3), "P3", NotifyHistory{}, p3imm, now, true, "", ""},
		// D10: düşük güven görünür ama bildirilmez — tutma kodu kapıyı söyler.
		{"low güven", lowStd, "P1", NotifyHistory{}, pol, now, false, NotifyHoldGatePrefix + "G2", ""},
		// D3 / D5: "problem değil" penceresi 12 sa, baypaslı.
		{"işaret 1 sa önce", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{Noise: &NoiseMark{At: ago(time.Hour)}}, pol, now, false, NotifyHoldNoise, ""},
		{"işaret 13 sa önce → bildirir (zaman sınırlı)", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{Noise: &NoiseMark{At: ago(13 * time.Hour)}}, pol, now, true, "", ""},
		{"işaret gelecekte (replay) sayılmaz", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{Noise: &NoiseMark{At: now.Add(time.Hour).UnixNano()}}, pol, now, true, "", ""},
		{"işaret + öncelik yükseldi", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P1", NotifyHistory{Noise: &NoiseMark{At: ago(time.Hour), Priority: "P2"}}, pol, now, true, "", "priority_up"},
		{"kalıcı gizleme (yönetici)", npStd(StdFamilyRule, ConfidenceHigh, StdFloorMeasures{}, 9), "P1", NotifyHistory{Noise: &NoiseMark{At: ago(30 * 24 * time.Hour), Permanent: true}}, pol, now, false, NotifyHoldHidden, ""},
		// D3 tekrar penceresi ve üç baypas.
		{"tekrar 2 sa önce", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{LastSend: &NotifyRecord{At: ago(2 * time.Hour), Priority: "P2", Magnitude: 3}}, pol, now, false, NotifyHoldDedupe, ""},
		{"tekrar 12 sa sonra açık", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{LastSend: &NotifyRecord{At: ago(12 * time.Hour), Priority: "P2", Magnitude: 3}}, pol, now, true, "", ""},
		{"tekrar + öncelik P2→P1", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P1", NotifyHistory{LastSend: &NotifyRecord{At: ago(time.Hour), Priority: "P2", Magnitude: 3}}, pol, now, true, "", "priority_up"},
		{"tekrar + etki ≥ 2×", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 6), "P2", NotifyHistory{LastSend: &NotifyRecord{At: ago(time.Hour), Priority: "P2", Magnitude: 3}}, pol, now, true, "", "impact_2x"},
		{"tekrar + etki 1,9× (baypas yok)", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 5.7), "P2", NotifyHistory{LastSend: &NotifyRecord{At: ago(time.Hour), Priority: "P2", Magnitude: 3}}, pol, now, false, NotifyHoldDedupe, ""},
		{"tekrar + yüksek güvenli P1", npStd(StdFamilyRule, ConfidenceHigh, StdFloorMeasures{}, 3), "P1", NotifyHistory{LastSend: &NotifyRecord{At: ago(time.Hour), Priority: "P1", Magnitude: 3}}, pol, now, true, "", "high_conf_p1"},
		// Sessiz saat yalnız P2.
		{"sessiz saatte P2", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{}, quiet, night, false, NotifyHoldQuiet, ""},
		{"sessiz saatte P1", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P1", NotifyHistory{}, quiet, night, true, "", ""},
		{"sessiz saat dışında P2", npStd(StdFamilyRule, ConfidenceMedium, StdFloorMeasures{}, 3), "P2", NotifyHistory{}, quiet, now, true, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := NotifiableAt(c.std, c.prio, c.hist, c.pol, c.at)
			if d.Notifiable != c.ok || d.Hold != c.hold || d.Bypass != c.bypass {
				t.Fatalf("got notifiable=%v hold=%q bypass=%q; want %v %q %q", d.Notifiable, d.Hold, d.Bypass, c.ok, c.hold, c.bypass)
			}
			if d.Notifiable && d.Hold != "" {
				t.Fatal("bildirilebilir kararın tutma kodu olamaz")
			}
		})
	}
}

func TestNotifyPolicyDefaultsAndValidation(t *testing.T) {
	d := DefaultNotifyPolicy()
	if d.Mode != NotifyModeShadow || d.P3 != NotifyP3Digest || strings.Join(d.DigestTimes, ",") != "09:00,16:00" || d.NoiseDedupeHours != 12 {
		t.Fatalf("varsayılan: %+v", d)
	}
	f := d.ImpactFloors
	if f.ErrorFailed5m != 30 || f.ErrorPct != 1 || f.MinCalls5m != 30 || f.LatencyP99DeltaMs != 300 || f.TrafficDropPct != 40 || f.TrafficMinBaselineCalls5m != 100 {
		t.Fatalf("D6 tabanları: %+v", f)
	}
	if err := ValidateNotifyPolicy(d); err != nil {
		t.Fatalf("varsayılan geçersiz: %v", err)
	}
	if CurrentNotifyPolicy().Mode != NotifyModeShadow {
		t.Fatal("hidrate edilmemiş süreç shadow görmeli")
	}
	bad := []func(*NotifyPolicy){
		func(p *NotifyPolicy) { p.Mode = "on" },
		func(p *NotifyPolicy) { p.P3 = "mail" },
		func(p *NotifyPolicy) { p.DigestTimes = []string{"25:00"} },
		func(p *NotifyPolicy) { p.DigestTimes = []string{"1", "2", "3", "4", "5", "6", "7"} },
		func(p *NotifyPolicy) { p.QuietHours = &NotifyQuietHours{From: "22:00", To: "x"} },
		func(p *NotifyPolicy) { p.NoiseDedupeHours = 0 },
		func(p *NotifyPolicy) { p.NoiseDedupeHours = 1000 },
		func(p *NotifyPolicy) { p.ImpactFloors.ErrorPct = 101 },
		func(p *NotifyPolicy) { p.ImpactFloors.TrafficDropPct = -1 },
	}
	for i, mut := range bad {
		p := DefaultNotifyPolicy()
		mut(&p)
		if ValidateNotifyPolicy(p) == nil {
			t.Errorf("bozuk girdi %d kabul edildi: %+v", i, p)
		}
	}
	// decode: eksik alan varsayılanda; bozuk değer normalize'da varsayılana döner.
	p, err := decodeNotifyPolicy([]byte(`{"mode":"enforce","noiseDedupeHours":0,"digestTimes":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != NotifyModeEnforce || p.P3 != NotifyP3Digest || p.NoiseDedupeHours != 12 || p.DigestTimes == nil || len(p.DigestTimes) != 0 {
		t.Fatalf("decode: %+v", p)
	}
	if p.ImpactFloors != d.ImpactFloors {
		t.Fatalf("tabanlar korunmadı: %+v", p.ImpactFloors)
	}
	if _, err := decodeNotifyPolicy([]byte(`{`)); err == nil {
		t.Fatal("bozuk JSON hata vermeli")
	}
	if p, err := decodeNotifyPolicy(nil); err != nil || p.Mode != NotifyModeShadow {
		t.Fatalf("boş blob: %+v %v", p, err)
	}
}

func TestLoadNotifyPolicyKeepsLastGood(t *testing.T) {
	t.Cleanup(func() { notifyPolicyCfg.Store(nil) })
	on := DefaultNotifyPolicy()
	on.Mode = NotifyModeOff
	LoadNotifyPolicyWith(func() (NotifyPolicy, error) { return on, nil })
	if CurrentNotifyPolicy().Mode != NotifyModeOff {
		t.Fatal("yayınlanmadı")
	}
	LoadNotifyPolicyWith(func() (NotifyPolicy, error) { return NotifyPolicy{}, errTestRead })
	if CurrentNotifyPolicy().Mode != NotifyModeOff {
		t.Fatal("okuma hatası son iyi değeri ezdi")
	}
}

type testReadErr struct{}

func (testReadErr) Error() string { return "ch down" }

var errTestRead = testReadErr{}

// TestNotifyViewPriority — D2: bildirim önceliği = /inbox görünüm önceliği.
func TestNotifyViewPriority(t *testing.T) {
	keep := DefaultInboxKeepSourcePriority()
	cases := []struct {
		p    Problem
		want string
	}{
		{Problem{RuleID: "anomaly:orders-api:error_rate", Priority: "P1"}, "P1"},
		{Problem{RuleID: "anomaly:orders-api:p99_ms", Priority: "P1"}, "P3"},
		{Problem{RuleID: "svc-slowdown:crm", Priority: "P1"}, "P1"},
		{Problem{RuleID: "db-health:oracle@h/d", Priority: "P2"}, "P2"},
		{Problem{RuleID: "user-rule", Priority: "P1"}, "P3"},
		{Problem{RuleID: "builtin-http-p99-5s", Priority: "P2"}, "P2"},
		{Problem{RuleID: "exception-group:new", Priority: "P2"}, "P2"},
		{Problem{RuleID: "incident:i1", Kind: NotifyKindIncident, Severity: "critical", Priority: "P1"}, "P1"},
		{Problem{RuleID: "incident:i2", Kind: NotifyKindIncident, Severity: "warning", Priority: "P2"}, "P3"},
		{Problem{RuleID: "anomaly:a:error_rate", Priority: "P3"}, "P3"},
		{Problem{RuleID: "anomaly:a:error_rate"}, "P3"},
	}
	for _, c := range cases {
		if got := NotifyViewPriority(c.p, keep); got != c.want {
			t.Errorf("%s/%s → %s, istenen %s", c.p.RuleID, c.p.Priority, got, c.want)
		}
	}
	if got := NotifyViewPriority(Problem{RuleID: "anomaly:a:error_rate", Priority: "P1"}, []string{}); got != "P3" {
		t.Errorf("boş liste: %s", got)
	}
}
