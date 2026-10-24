package chstore

// notify_policy_replay_test.go — v0.10.1523 (Problem standardı R1): replay
// okumalarının sınırları (SQL şekli) + SAF birleştirme ve recall kontrolü.

import (
	"strings"
	"testing"
	"time"
)

func TestNotifyReplaySQLBounds(t *testing.T) {
	from := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	to := from.Add(14 * 24 * time.Hour)
	type built struct {
		name string
		sql  string
		args []any
	}
	sends, sargs := notifyReplaySendsSQL(from, to)
	shadow, shargs := notifyReplayShadowSQL(from, to)
	probs, pargs := notifyReplayProblemsSQL("id", from, to)
	traf, targs := serviceBucketTrafficSQL([]ServiceBucket{{"a", 1_700_000_100}, {"b", 1_700_000_400}})
	for _, b := range []built{{"sends", sends, sargs}, {"shadow", shadow, shargs}, {"problems", probs, pargs}, {"traffic", traf, targs}} {
		for _, frag := range []string{"LIMIT ", "max_execution_time = 20"} {
			if !strings.Contains(b.sql, frag) {
				t.Errorf("%s: %q yok\n%s", b.name, frag, b.sql)
			}
		}
		if strings.Count(b.sql, "?") != len(b.args) {
			t.Errorf("%s: %d yer tutucu, %d argüman", b.name, strings.Count(b.sql, "?"), len(b.args))
		}
	}
	for _, b := range []built{{"sends", sends, sargs}, {"shadow", shadow, shargs}} {
		if !strings.Contains(b.sql, "sent_at >= ? AND sent_at < ?") || strings.Contains(b.sql, "FINAL") {
			t.Errorf("%s: notification_log zaman sınırı / FINAL'siz okuma\n%s", b.name, b.sql)
		}
		if b.args[0] != chDateTime64Arg(from) || b.args[1] != chDateTime64Arg(to) {
			t.Errorf("%s: zaman argümanları %v", b.name, b.args[:2])
		}
	}
	if !strings.Contains(sends, "channel_kind NOT IN ('none', 'ignore', 'policy')") {
		t.Errorf("gönderim sayımı sentetik/gölge satırları dışlamalı:\n%s", sends)
	}
	if !strings.Contains(shadow, "channel_kind = 'policy'") {
		t.Errorf("gölge sayımı yalnız policy:\n%s", shadow)
	}
	if !strings.Contains(probs, "FROM problems FINAL") || !strings.Contains(probs, "started_at >= ? AND started_at < ?") {
		t.Errorf("problems okuması:\n%s", probs)
	}
	if pargs[len(pargs)-1] != notifyReplayProblemsLimit || sargs[len(sargs)-1] != notifyReplaySendsLimit {
		t.Errorf("LIMIT argümanları: %v / %v", pargs, sargs)
	}
	if !strings.Contains(traf, "FROM service_summary_5m") || !strings.Contains(traf, "time_bucket >= toDateTime(?, 'UTC') AND time_bucket <= toDateTime(?, 'UTC')") ||
		!strings.Contains(traf, "(service_name, time_bucket) IN (tuple(?, toDateTime(?, 'UTC')), tuple(?, toDateTime(?, 'UTC')))") {
		t.Errorf("trafik okuması:\n%s", traf)
	}
	if targs[0] != "2023-11-14 22:15:00" || targs[1] != "2023-11-14 22:20:00" {
		t.Errorf("DateTime argümanları (chDateTimeArg, saniye): %v", targs[:2])
	}
	// Çift tavanı: sorgu tavanın üstüne çıkmaz.
	many := make([]ServiceBucket, notifyReplayTrafficPairs+10)
	for i := range many {
		many[i] = ServiceBucket{"s", int64(i) * 300}
	}
	q, a := serviceBucketTrafficSQL(many)
	if strings.Count(q, "tuple(") != notifyReplayTrafficPairs || a[len(a)-1] != notifyReplayTrafficPairs {
		t.Errorf("çift tavanı uygulanmadı: %d", strings.Count(q, "tuple("))
	}
	if q, _ := serviceBucketTrafficSQL(nil); q != "" {
		t.Error("boş çift listesi sorgu üretmemeli")
	}
}

func TestNotifyReplayHelpers(t *testing.T) {
	if subjectPriority("[CRITICAL][P1] orders-api — x") != "P1" || subjectPriority("[WARNING] a — b") != "" {
		t.Error("subjectPriority")
	}
	if subjectSeverity("[CRITICAL][P1] x") != "critical" {
		t.Error("subjectSeverity")
	}
	for id, want := range map[string]string{
		"exception-group:abc":                    "abc",
		"exception-group:abc:regressed:p1:17000": "abc",
		"exception-group:ora:deadbeef:regressed": "ora:deadbeef",
		"abc":                                    "abc",
		"exception-group:ora-source:prod:p1":     "",
	} {
		if got := ExceptionFingerprintOfNotifyID(id); got != want {
			t.Errorf("%s → %q, istenen %q", id, got, want)
		}
	}
	at := time.Unix(1_700_000_123, 0)
	c, b := TrafficPairsFor("a", at)
	if c.Bucket != 1_699_999_800 || b.Bucket != c.Bucket-3600 {
		t.Errorf("kova çiftleri: %+v %+v", c, b)
	}
	if StdTrafficFrom(map[ServiceBucket]BucketTraffic{}, c, b) != nil {
		t.Error("eksik kova = bilinmiyor (nil)")
	}
}

// TestFoldNotifyReplayRecall — "Gerçek problem" işaretli ya da onaylı vakası
// olan her öğe, bar tutuyorsa TEK TEK işaretlenir; sayaçlar aile başına.
func TestFoldNotifyReplayRecall(t *testing.T) {
	from := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	to := from.Add(14 * 24 * time.Hour)
	t0 := from.Add(24 * time.Hour)
	ns := func(d time.Duration) int64 { return t0.Add(d).UnixNano() }
	res := ns(10 * time.Minute)
	probs := []Problem{
		// 1) Gönderilmiş, yüksek etkili rampa (P1, istisna listesinde) → geçer.
		{ID: "p1", RuleID: "anomaly:bff-web:error_rate", Service: "bff-web", Metric: "error_rate", Value: 3.2, Threshold: 0.4,
			Severity: "critical", Status: "resolved", StartedAt: ns(0), ResolvedAt: &res,
			Description: "Teknik hata (5xx) oranı sürekli yükseldi on bff-web — current 3.20% vs baseline 0.40% (eşik 0.80%, 2.5 hata/sn, 6 kova ≈ 30 dk boyunca eşiğin üstünde). " + REDEvidenceVM + "."},
		// 2) Aynı imza 20 dk sonra yeniden açıldı (flap) — gönderilmiş, 12 sa tekrar penceresinde → dedupe.
		{ID: "p2", RuleID: "anomaly:bff-web:error_rate", Service: "bff-web", Metric: "error_rate", Value: 3.0, Threshold: 0.4,
			Severity: "critical", Status: "open", StartedAt: ns(30 * time.Minute)},
		// 3) p99 anomalisi istisna listesinde değil → görünüm P3 → özet; "gerçek" işaretli → recall'da İŞARETLENİR.
		{ID: "p3", RuleID: "anomaly:payments:p99_ms", Service: "payments", Metric: "p99_ms", Value: 1500, Threshold: 300,
			Severity: "critical", Status: "open", StartedAt: ns(time.Hour)},
		// 4) Onaylı vaka kaynağı, düşük trafik (G1 ölçülmüş başarısızlık) → İŞARETLENİR.
		{ID: "p4", RuleID: "anomaly:quiet-svc:error_rate", Service: "quiet-svc", Metric: "error_rate", Value: 50, Threshold: 1,
			Severity: "critical", Status: "open", StartedAt: ns(2 * time.Hour)},
		// 6) Pencere ÖNCESİ açılmış onaylı vaka kaynağı: recall'a girer, aile sayacına girmez.
		{ID: "p0", RuleID: "db-health:oracle@h/d", Service: "db:oracle@d", Kind: ProblemKindDB, Metric: DBHealthMetricErrorPct,
			Value: 20, Threshold: 5, Severity: "critical", Status: "resolved", StartedAt: from.Add(-time.Hour).UnixNano()},
		// 5) "problem değil" işaretli kural, işaretten 1 sa sonra açıldı → noise.
		{ID: "p5", RuleID: "user-noisy", Service: "noisy-svc", Metric: "custom", Value: 10, Threshold: 1,
			Severity: "critical", Status: "open", StartedAt: ns(3 * time.Hour)},
	}
	sends := map[string]ReplaySend{
		"p1":                   {RelatedKind: "problem", Rows: 2, OK: 2, FirstSubject: "[CRITICAL][P1] bff-web — Anomaly · Error rate", FirstAt: ns(time.Minute)},
		"p2":                   {RelatedKind: "problem", Rows: 1, OK: 1, FirstSubject: "[CRITICAL][P1] bff-web — Anomaly · Error rate", FirstAt: ns(31 * time.Minute)},
		"p3":                   {RelatedKind: "problem", Rows: 1, OK: 1, FirstSubject: "[CRITICAL][P1] payments — Anomaly · P99 latency", FirstAt: ns(61 * time.Minute)},
		"p5":                   {RelatedKind: "problem", Rows: 1, OK: 1, FirstSubject: "[CRITICAL][P1] noisy-svc — Noisy", FirstAt: ns(181 * time.Minute)},
		"exception-group:fp1":  {RelatedKind: "exception", Rows: 1, OK: 1, FirstSubject: "[CRITICAL][P1] orders-api — Exception · NPE", FirstAt: ns(4 * time.Hour)},
		"inc-1":                {RelatedKind: "incident", Rows: 1, OK: 1, FirstSubject: "[CRITICAL][P1]  — Incident · x", FirstAt: ns(5 * time.Hour)},
		"exception-group:fail": {RelatedKind: "exception", Rows: 1, OK: 0, FirstAt: ns(6 * time.Hour)},
	}
	cur4, _ := TrafficPairsFor("quiet-svc", time.Unix(0, ns(2*time.Hour)))
	traffic := map[ServiceBucket]BucketTraffic{cur4: {Calls: 4, Errors: 2}}
	keep := DefaultInboxKeepSourcePriority()
	keep = append(keep, "user-noisy")
	in := NotifyReplayInput{
		From: from, To: to, Problems: probs, Sends: sends, Traffic: traffic,
		Verdicts: []ProblemVerdict{
			{Signature: "p:anomaly:payments:p99_ms|payments", Verdict: ProblemVerdictReal},
			{Signature: "p:user-noisy|noisy-svc", Verdict: ProblemVerdictNoise, At: ns(2 * time.Hour)},
			{Signature: "a:evt-1", Verdict: ProblemVerdictReal},
			{Signature: "e:fp9", Verdict: ProblemVerdictReal},
		},
		Cases: []IncidentCase{
			{CaseID: "p:p4", Source: "problem", SourceID: "p4", Status: CaseConfirmed},
			{CaseID: "p:p0", Source: "problem", SourceID: "p0", Status: CaseConfirmed},
			{CaseID: "p:p1", Source: "problem", SourceID: "p1", Status: CaseConfirmed},
			{CaseID: "p:gone", Source: "problem", SourceID: "gone", Status: CaseConfirmed},
			{CaseID: "p:draft", Source: "problem", SourceID: "p3", Status: CaseDraft},
		},
		Groups: map[string]ExceptionGroup{
			"fp1": {Fingerprint: "fp1", Type: "NPE", Service: "orders-api", Occurrences: 900, Priority: "P1", FirstSeen: ns(4 * time.Hour), LastSeen: ns(4 * time.Hour)},
			"fp9": {Fingerprint: "fp9", Type: "IOE", Service: "orders-api", Occurrences: 3, Priority: "P2", FirstSeen: ns(time.Hour), LastSeen: ns(time.Hour)},
		},
		Keep: keep, Policy: DefaultNotifyPolicy(), Priority: DefaultProblemPriority(),
	}
	rep := FoldNotifyReplay(in)
	byFam := map[string]NotifyReplayFamily{}
	for _, f := range rep.Families {
		byFam[f.Family] = f
	}
	e := byFam[StdFamilyError]
	if e.Opened != 3 || e.SentBefore != 2 || e.WouldSendAfter != 1 || e.Holds[NotifyHoldDedupe] != 1 || e.FlapReopens != 1 || e.SendRowsBefore != 3 {
		t.Errorf("error ailesi: %+v", e)
	}
	l := byFam[StdFamilyLatency]
	if l.SentBefore != 1 || l.WouldSendAfter != 0 || l.Holds[NotifyHoldP3Digest] != 1 {
		t.Errorf("latency ailesi: %+v", l)
	}
	r := byFam[StdFamilyRule]
	if r.Holds[NotifyHoldNoise] != 1 || r.NoiseMarked != 1 {
		t.Errorf("rule ailesi: %+v", r)
	}
	x := byFam[StdFamilyException]
	if x.SentBefore != 1 || x.WouldSendAfter != 1 {
		t.Errorf("exception ailesi (ok=0 satır sayılmaz): %+v", x)
	}
	if i := byFam[StdFamilyIncident]; i.SentBefore != 1 || i.WouldSendAfter != 1 {
		t.Errorf("incident ailesi: %+v", i)
	}
	if byFam[StdFamilyDB].Opened != 0 {
		t.Errorf("pencere dışı açılış aile sayacına girdi: %+v", byFam[StdFamilyDB])
	}
	if rep.Totals.SentBefore != 6 {
		t.Errorf("toplam önce: %d", rep.Totals.SentBefore)
	}
	// Recall: p3 (gerçek işaret, P3 özet), p4 (onaylı vaka, G1) ve fp9 (gerçek
	// e: işareti, 3 oluşum → G1) tutuluyor; p1 (onaylı) geçiyor.
	rc := rep.Recall
	if rc.Pass || rc.Held != 3 || rc.Checked != 5 || rc.OutOfFunnel != 1 || rc.NotFound != 1 {
		t.Fatalf("recall: %+v", rc)
	}
	got := map[string]string{}
	for _, c := range rc.Cases {
		got[c.ID] = c.Reason + "/" + c.Hold
	}
	want := map[string]string{
		"p3":                  "real_mark/" + NotifyHoldP3Digest,
		"p4":                  "confirmed_case/" + NotifyHoldGatePrefix + "G1",
		"exception-group:fp9": "real_mark/" + NotifyHoldGatePrefix + "G1",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("recall %s = %q, istenen %q", id, got[id], w)
		}
	}
	if rep.Days != 14 || len(rep.Notes) == 0 {
		t.Errorf("rapor başlığı: days=%d notes=%d", rep.Days, len(rep.Notes))
	}
}

func TestNotifyReplayTrafficPairsDedup(t *testing.T) {
	at := time.Unix(1_700_000_000, 0).UnixNano()
	pairs := NotifyReplayTrafficPairs([]Problem{{Service: "a", StartedAt: at}, {Service: "a", StartedAt: at}, {Service: "", StartedAt: at}})
	if len(pairs) != 2 {
		t.Fatalf("çiftler: %+v", pairs)
	}
}
