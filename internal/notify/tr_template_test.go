package notify

// tr_template_test.go — v0.10.1532 (Problem standardı R4, D9). Türkçe metin
// yardımcıları (FE lib/problemStandardView.ts ile aynı kurallar), gövdenin
// standart alanları, HTML kaçışı ve legacy anahtarı.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestTRDuration(t *testing.T) {
	cases := map[int64]string{0: "0 sn", 45: "45 sn", 60: "1 dk", 2100: "35 dk", 3600: "1 sa",
		5*3600 + 40*60: "5 sa 40 dk", 86400: "1 g", 2*86400 + 3*3600: "2 g 3 sa", -5: "0 sn"}
	for in, want := range cases {
		if got := trDuration(in); got != want {
			t.Errorf("%d → %q, beklenen %q", in, got, want)
		}
	}
}

func TestTRSinceLine(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, loc)
	at := func(d, h, m int) int64 { return time.Date(2026, 10, d, h, m, 0, 0, loc).UnixNano() }
	cases := []struct {
		ns   int64
		want string
	}{
		{at(25, 6, 5), "06:05'ten beri"},
		{at(25, 6, 30), "06:30'dan beri"},
		{at(25, 6, 40), "06:40'tan beri"},
		{at(25, 6, 0), "06:00'dan beri"},
		{at(25, 9, 48), "09:48'den beri"},
		{at(25, 13, 0), "13:00'ten beri"},
		{at(24, 6, 5), "24.10 06:05'ten beri"},
		{0, "—"},
	}
	for _, c := range cases {
		if got := trSinceLine(c.ns, now, loc); got != c.want {
			t.Errorf("%d → %q, beklenen %q", c.ns, got, c.want)
		}
	}
}

func TestTRLines(t *testing.T) {
	if got := trDurationLine(chstore.StdSince{DurationSec: 5*3600 + 40*60, Ongoing: true, Episode: 3}); got != "5 sa 40 dk · sürüyor · 3. kez" {
		t.Errorf("süre satırı: %q", got)
	}
	im := chstore.StdImpact{Primary: chstore.StdMeasure{Text: "hata oranı %0,7"}, FailedPerMin: 419.6, Callers: []string{"a", "b"}}
	if got := trImpactLine(im); got != "hata oranı %0,7 · ~420 hatalı istek/dk · 2 çağıran (doğrulanmış)" {
		t.Errorf("etki: %q", got)
	}
	if trImpactLine(chstore.StdImpact{}) != "etki bilinmiyor" {
		t.Error("boş etki dürüstçe 'bilinmiyor'")
	}
	gates := []chstore.SignalGate{{ID: "G1", Name: "trafik", Status: chstore.GatePass}, {ID: "G3", Name: "VM karar", Status: chstore.GateNA},
		{ID: "G5", Name: "deploy", Status: chstore.GateContext}, {ID: "G6", Name: "takvim", Status: chstore.GateFlag}}
	if got := trConfidenceLine(chstore.StdConfidence{Level: chstore.ConfidenceHigh, Gates: gates}); got != "YÜKSEK (trafik ✓ takvim !)" {
		t.Errorf("güven: %q", got)
	}
	if trSourceLine(chstore.StdEvidence{Source: chstore.StdSourceVM}) != "VictoriaMetrics · örneklenmemiş" {
		t.Error("kaynak satırı")
	}
}

func trTestView() (*alertView, chstore.Problem) {
	p := chstore.Problem{ID: "p1", RuleID: "anomaly:svc:error_rate", RuleName: "Error <rate>", Service: "svc", Status: "open",
		Severity: "critical", Priority: "P1", PriorityReason: "critical + 14×", Metric: "error_rate", Value: 0.7, Threshold: 0.05,
		Description: "ham <b>dedektör</b> cümlesi"}
	std := chstore.ProblemStandard{
		Headline: "svc: teknik hata <script>arttı</script>", Subject: "svc",
		Impact:     chstore.StdImpact{Primary: chstore.StdMeasure{Text: "hata oranı %0,7 (normal %0,05)"}, Known: true},
		Since:      chstore.StdSince{StartedAt: time.Now().Add(-time.Hour).UnixNano(), DurationSec: 3600, Ongoing: true},
		Evidence:   chstore.StdEvidence{Sentence: "30 dk boyunca eşiğin üstünde", Source: chstore.StdSourceVM},
		Confidence: chstore.StdConfidence{Level: chstore.ConfidenceHigh},
		NextStep:   chstore.StdNextStep{Text: "Servisin hata kırılımına bak", Href: "/service?name=svc"},
		Context:    []string{"yakın deploy: v2.3.1"},
	}
	th := chstore.NotifyThread{Signature: "p:anomaly:svc:error_rate|svc", Topic: std.Headline, OpenedAt: time.Now().UnixNano()}
	return &alertView{Life: LifeOpened, Thread: th, Std: std, Now: time.Now()}, p
}

func TestBuildEmailBodyTR(t *testing.T) {
	n := &Notifier{}
	v, p := trTestView()
	body := n.buildEmailBodyTR(v, p, nil, "")
	if !strings.HasPrefix(body, "AÇILDI · P1 · güven YÜKSEK\n") {
		t.Errorf("ilk satır durum · öncelik · güven olmalı (önizleme): %q", strings.SplitN(body, "\n", 2)[0])
	}
	for _, want := range []string{"Etki:", "Ne zamandan beri:", "Güven:", "Kaynak:", "Neden:", "Bağlam:", "(önceliği etkilemez)",
		"Sonraki adım:", "Öncelik:", "Teknik: kural: Error <rate>", "Dedektör notu:"} {
		if !strings.Contains(body, want) {
			t.Errorf("gövde %q taşımalı:\n%s", want, body)
		}
	}
	for _, en := range []string{"Service:", "Severity:", "Started at:", "threshold"} {
		if strings.Contains(body, en) {
			t.Errorf("D9: İngilizce etiket kalmamalı: %q", en)
		}
	}
}

func TestBuildEmailHTMLTREscapes(t *testing.T) {
	n := &Notifier{}
	v, p := trTestView()
	h := n.buildEmailHTMLTR(v, p, nil, "")
	if strings.Contains(h, "<script>") || strings.Contains(h, "<b>dedektör</b>") || strings.Contains(h, "Error <rate>") {
		t.Error("her dinamik alan kaçışlı olmalı")
	}
	if !strings.Contains(h, `lang="tr"`) || !strings.Contains(h, "Ne zamandan beri") || !strings.Contains(h, "AÇILDI · P1") {
		t.Error("Türkçe standart alanlar HTML'de")
	}
	v.Life = LifeResolved
	if !strings.Contains(n.buildEmailHTMLTR(v, p, nil, ""), "#16a34a") {
		t.Error("çözülme şeridi yeşil")
	}
}

func TestTRChatTitleAndLegacySwitch(t *testing.T) {
	v, p := trTestView()
	if got := trChatTitle(v, p); !strings.HasPrefix(got, "AÇILDI · [P1] svc: teknik hata") {
		t.Errorf("sohbet başlığı: %q", got)
	}
	if alertTitleCtx(context.Background(), p) != alertTitle(p) {
		t.Error("görünüm yokken eski başlık")
	}
	if alertTitleCtx(withAlertView(context.Background(), v), p) != trChatTitle(v, p) {
		t.Error("görünüm varken Türkçe başlık")
	}
	// legacy anahtarı: görünüm kurulmaz.
	pol := chstore.DefaultNotifyPolicy()
	if pol.Template != chstore.NotifyTemplateTR {
		t.Fatalf("varsayılan şablon tr: %q", pol.Template)
	}
	pol.Template = chstore.NotifyTemplateLegacy
	chstore.SetNotifyPolicy(pol)
	defer chstore.SetNotifyPolicy(chstore.DefaultNotifyPolicy())
	n := &Notifier{}
	if alertViewOf(n.prepareAlertView(context.Background(), p)) != nil {
		t.Error("template=legacy: görünüm kurulmamalı (eski biçim bayt bayt)")
	}
}

func TestSlackTeamsBodiesTR(t *testing.T) {
	n := &Notifier{}
	v, p := trTestView()
	sb := n.slackBodyTR(v, chstore.NotificationChannel{}, p)
	if !strings.HasPrefix(sb["text"].(string), "AÇILDI · [P1]") {
		t.Errorf("slack başlık: %v", sb["text"])
	}
	tb := n.teamsBodyTR(v, chstore.NotificationChannel{}, p)
	facts := tb["sections"].([]map[string]any)[0]["facts"].([]map[string]string)
	if facts[0]["name"] != "Etki" {
		t.Errorf("teams ilk alan Etki olmalı: %v", facts[0])
	}
	if !strings.Contains(n.zoomTextTR(v, chstore.NotificationChannel{}, p), "• Ne zamandan beri:") {
		t.Error("zoom metni Türkçe alanlar")
	}
	if !strings.Contains(n.buildWhatsAppTextTR(v, p), "Etki: ") {
		t.Error("whatsapp metni Türkçe alanlar")
	}
}
