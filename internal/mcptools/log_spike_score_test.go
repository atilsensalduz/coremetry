package mcptools

// v0.10.1284 — AI SRE Faz 1a: explain_log_spike (log artışını ne açıklıyor).
// Saf çekirdek: tepe bulma, taban penceresi, katkı sıralaması, Türkçe
// gerekçe, önbellek anahtarı.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

func flat(n int, v int64) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestDetectSpike(t *testing.T) {
	t.Run("düz seri → yok", func(t *testing.T) {
		x := []int64{100, 104, 98, 101, 99, 103, 97, 100, 102, 99}
		if d := detectSpike(x, 60); d.Found {
			t.Fatalf("düz seride tepe bulundu: %+v", d)
		}
	})
	t.Run("tek kova tepe", func(t *testing.T) {
		x := flat(20, 100)
		x[12] = 400
		d := detectSpike(x, 60)
		if !d.Found || d.Start != 12 || d.End != 12 || d.LevelShift {
			t.Fatalf("tek kova: %+v", d)
		}
		if d.PeakRatio != 4 {
			t.Fatalf("tepe oranı 4 bekleniyordu: %v", d.PeakRatio)
		}
	})
	t.Run("kenar z≥1.5 sürdükçe genişler", func(t *testing.T) {
		x := flat(20, 100)
		x[9], x[10], x[11] = 130, 400, 125 // 130/125: z≈3 (σ=√100=10) → genişler
		d := detectSpike(x, 60)
		if !d.Found || d.Start != 9 || d.End != 11 {
			t.Fatalf("genişleme: %+v", d)
		}
	})
	t.Run("seviye değişimi (koşu > yarı)", func(t *testing.T) {
		// 9 kova: medyan 10; sıcak 4,6,7,8 — 5'teki tek kovalık boşluk birleşir
		// → 4..8 = 5/9 > %50.
		x := []int64{10, 10, 10, 10, 200, 10, 200, 200, 200}
		d := detectSpike(x, 60)
		if !d.Found || !d.LevelShift || d.Start != 4 || d.End != 8 {
			t.Fatalf("seviye değişimi: %+v", d)
		}
	})
	t.Run("Poisson tabanı: m=0, x=3 → yok", func(t *testing.T) {
		x := flat(30, 0)
		x[20] = 3
		if d := detectSpike(x, 60); d.Found {
			t.Fatalf("seyrek seride 3 satır tepe sayıldı: %+v", d)
		}
	})
	t.Run("iki koşu: büyük olan kazanır", func(t *testing.T) {
		x := flat(30, 100)
		x[5] = 300              // puan 200
		x[20], x[21] = 500, 500 // puan 800
		d := detectSpike(x, 60)
		if !d.Found || d.Start != 20 || d.End != 21 {
			t.Fatalf("büyük koşu: %+v", d)
		}
	})
	t.Run("eşit puanda en yeni", func(t *testing.T) {
		x := flat(30, 100)
		x[5], x[20] = 400, 400
		d := detectSpike(x, 60)
		if !d.Found || d.Start != 20 {
			t.Fatalf("eşitlikte en yeni: %+v", d)
		}
	})
	t.Run("mutlak taban kova genişliğiyle büyür", func(t *testing.T) {
		x := flat(20, 4)
		x[10] = 30 // x−m = 26; 24 dk kova → taban max(10, 5·1440/60=120) = 120
		if d := detectSpike(x, 1440); d.Found {
			t.Fatalf("geniş kovada küçük sapma tepe sayıldı: %+v", d)
		}
		if d := detectSpike(x, 60); !d.Found {
			t.Fatalf("dar kovada aynı sapma tepe olmalı: %+v", d)
		}
	})
}

// Yarım son kova atılır; ızgara boş kovayı sıfırlar.
func TestSpikeGridDropsPartialLastBucket(t *testing.T) {
	from := time.Unix(1_700_000_000, 0).UTC()
	from = floorTime(from, time.Minute)
	to := from.Add(5*time.Minute + 20*time.Second) // son kova yarım
	ns := func(i int) int64 { return from.Add(time.Duration(i) * time.Minute).UnixNano() }
	series := []logstore.LogSeries{
		{Name: "ERROR", Points: []logstore.LogPoint{{T: ns(1), V: 5}, {T: ns(5), V: 99}}},
		{Name: "INFO", Points: []logstore.LogPoint{{T: ns(0), V: 10}, {T: ns(1), V: 10}, {T: ns(5), V: 1}}},
		{Name: "weird", Points: []logstore.LogPoint{{T: ns(2), V: 3}}},
	}
	starts, total, errs, bands := spikeGrid(series, from, to, 60, false)
	if len(starts) != 5 {
		t.Fatalf("yarım son kova atılmalı: %d kova", len(starts))
	}
	if total[0] != 10 || total[1] != 15 || total[2] != 3 || total[3] != 0 || errs[1] != 5 {
		t.Fatalf("toplam=%v err=%v", total, errs)
	}
	if bands["OTHER"][2] != 3 {
		t.Fatalf("sözlük dışı ad OTHER'a: %v", bands)
	}
	starts, total, _, _ = spikeGrid(series, from, to, 60, true)
	if len(starts) != 6 || total[5] != 100 {
		t.Fatalf("verilen aralıkta yarım kova tutulur: %d %v", len(starts), total)
	}
}

func TestSpikeBaseWindow(t *testing.T) {
	for _, c := range []struct{ in, want time.Duration }{
		{time.Minute, 15 * time.Minute},
		{5 * time.Minute, 15 * time.Minute},
		{10 * time.Minute, 30 * time.Minute},
		{time.Hour, 3 * time.Hour},
		{6 * time.Hour, 3 * time.Hour},
	} {
		if got := spikeBaseWindow(c.in); got != c.want {
			t.Errorf("spikeBaseWindow(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func fsr(total int64, vals ...any) *logstore.FieldStatsResult {
	r := &logstore.FieldStatsResult{Total: total, Values: []logstore.FieldValueCount{}}
	for i := 0; i+1 < len(vals); i += 2 {
		r.Values = append(r.Values, logstore.FieldValueCount{Value: vals[i].(string), Count: int64(vals[i+1].(int))})
	}
	return r
}

func TestRankSpikeContributors(t *testing.T) {
	// Artış 10 dk'da 1000 satır, taban 30 dk'da 900 (→ 300 ölçekli), E = 700.
	tot := spikeTotals{S: 1000, B: 900, Ls: 10 * time.Minute, Lb: 30 * time.Minute}
	t.Run("pay matematiği", func(t *testing.T) {
		pod := spikeDimInput{Dim: spikeDimPod, Spike: fsr(1000, "pod-a", 600, "pod-b", 300, "pod-c", 100),
			Base: fsr(900, "pod-a", 300, "pod-b", 300, "pod-c", 300), BaseLimit: 20}
		out, more := rankSpikeContributors([]spikeDimInput{pod}, tot, 5)
		// pod-c (100 = taban hızı) elenir; pod-b 3× büyüdü (fazla 200 ≥ E·%10)
		// ama payı %33,3 → %30 DÜŞTÜ: orantılı büyüyen değer artışı açıklamaz.
		if more || len(out) != 1 {
			t.Fatalf("yalnız pod-a bekleniyordu: %+v", out)
		}
		c := out[0]
		// b̂ = 300·(10/30) = 100 → fazla 500 → pay 500/700 = %71.4
		if c.Value != "pod-a" || c.SharePct != 71.4 || c.SpikePct != 60 || c.BasePct != 33.3 || c.Rank != 1 || c.New {
			t.Fatalf("pay: %+v", c)
		}
	})
	t.Run("destek tabanı", func(t *testing.T) {
		small := spikeTotals{S: 30, B: 0, Ls: 10 * time.Minute, Lb: 30 * time.Minute}
		d := spikeDimInput{Dim: spikeDimPod, Spike: fsr(30, "a", 19, "b", 11), Base: fsr(0), BaseLimit: 20}
		out, _ := rankSpikeContributors([]spikeDimInput{d}, small, 5)
		if len(out) != 0 {
			t.Fatalf("<20 satırlık değer katkı olamaz: %+v", out)
		}
	})
	t.Run("yeni (taban top-N tam)", func(t *testing.T) {
		d := spikeDimInput{Dim: spikeDimErrorType, Spike: fsr(1000, "SocketTimeout", 400), Base: fsr(900, "IOException", 50), BaseLimit: 20}
		out, _ := rankSpikeContributors([]spikeDimInput{d}, tot, 5)
		if len(out) != 1 || !out[0].New || out[0].BaseCount != 0 || out[0].BaseUnknown {
			t.Fatalf("yeni: %+v", out)
		}
	})
	t.Run("taban bilinmiyor → üst sınır", func(t *testing.T) {
		vals := []any{}
		for i := 0; i < 20; i++ {
			vals = append(vals, "l"+string(rune('a'+i)), 45-i)
		}
		base := fsr(900, vals...)
		d := spikeDimInput{Dim: spikeDimLogger, Spike: fsr(1000, "com.acme.Retry", 500), Base: base, BaseLimit: 20}
		out, _ := rankSpikeContributors([]spikeDimInput{d}, tot, 5)
		if len(out) != 1 || !out[0].BaseUnknown || out[0].New {
			t.Fatalf("baseUnknown: %+v", out)
		}
		// 20. değer 26 → normalde ≤ %2.9; fazla = 500 − 26/3
		if out[0].BaseCount != 26 || out[0].BasePct != 2.9 || !strings.Contains(out[0].Reason, "normalde ≤ %2,9") {
			t.Fatalf("üst sınır: %+v", out[0])
		}
	})
	t.Run("boyut başına ≤2, toplam ≤5, fazlası hasMore", func(t *testing.T) {
		big := spikeTotals{S: 10000, B: 0, Ls: 10 * time.Minute, Lb: 30 * time.Minute}
		mk := func(dim string) spikeDimInput {
			return spikeDimInput{Dim: dim, Spike: fsr(10000, dim+"1", 3000, dim+"2", 2500, dim+"3", 2000), Base: fsr(0), BaseLimit: 20}
		}
		out, more := rankSpikeContributors([]spikeDimInput{mk("pod"), mk("logger"), mk("cluster")}, big, 5)
		if len(out) != 5 || !more {
			t.Fatalf("5 + hasMore bekleniyordu: %d %v", len(out), more)
		}
		per := map[string]int{}
		for _, c := range out {
			per[c.Dim]++
		}
		for d, n := range per {
			if n > 2 {
				t.Fatalf("%s boyutundan %d katkı", d, n)
			}
		}
	})
	t.Run("E ≤ 0 → hiç", func(t *testing.T) {
		down := spikeTotals{S: 200, B: 900, Ls: 10 * time.Minute, Lb: 30 * time.Minute}
		d := spikeDimInput{Dim: spikeDimPod, Spike: fsr(200, "a", 200), Base: fsr(900), BaseLimit: 20}
		if out, more := rankSpikeContributors([]spikeDimInput{d}, down, 5); len(out) != 0 || more {
			t.Fatalf("düşüşte katkı yok: %+v", out)
		}
	})
	t.Run("desen ham örneklem tabanı", func(t *testing.T) {
		d := spikeDimInput{Dim: spikeDimPattern, Spike: fsr(1000, "conn reset <*>", 500), Base: fsr(900), Raw: map[string]int64{"conn reset <*>": 3}, RawMin: 5}
		if out, _ := rankSpikeContributors([]spikeDimInput{d}, tot, 5); len(out) != 0 {
			t.Fatalf("3 örnek satırlık desen katkı olamaz: %+v", out)
		}
	})
}

func TestSpikeReasonTR(t *testing.T) {
	for _, c := range []struct {
		in   SpikeContributor
		want string
	}{
		{SpikeContributor{Dim: spikeDimPod, Value: "checkout-7f9-abc", SharePct: 62, SpikePct: 71, BasePct: 12},
			"artışın %62'si pod checkout-7f9-abc kaynaklı (artışta payı %71, normalde %12)"},
		{SpikeContributor{Dim: spikeDimPattern, Value: "Connection reset <*>", SharePct: 35, New: true},
			"yeni desen «Connection reset <*>» — artışın %35'i (öncesinde görülmedi)"},
		{SpikeContributor{Dim: spikeDimLogger, Value: "com.acme.Retry", SharePct: 40, SpikePct: 55.5, BasePct: 2.9, BaseUnknown: true},
			"artışın %40'ı logger com.acme.Retry kaynaklı (artışta payı %55,5, normalde ≤ %2,9)"},
		{SpikeContributor{Dim: spikeDimSeverity, Value: "ERROR", SharePct: 10, SpikePct: 30, BasePct: 3},
			"artışın %10'u seviye ERROR kaynaklı (artışta payı %30, normalde %3)"},
		{SpikeContributor{Dim: spikeDimErrorType, Value: "SocketTimeoutException", SharePct: 100, New: true},
			"yeni hata türü SocketTimeoutException — artışın %100'ü (öncesinde görülmedi)"},
	} {
		if got := spikeReasonTR(c.in); got != c.want {
			t.Errorf("got  %q\nwant %q", got, c.want)
		}
	}
	for n, want := range map[int]string{0: "ı", 1: "i", 2: "si", 3: "ü", 6: "sı", 9: "u", 20: "si", 30: "u", 40: "ı", 50: "si", 60: "ı", 70: "i", 90: "ı", 100: "ü", 62: "si", 47: "si"} {
		if got := numPossessive(n); got != want {
			t.Errorf("numPossessive(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestLogSpikeKeyHashesAllInputs(t *testing.T) {
	from := floorTime(time.Unix(1_700_000_000, 0).UTC(), 30*time.Second)
	to := from.Add(time.Hour)
	base := LogSpikeRequest{Service: "checkout", Env: "prod", Query: "timeout", Level: "all"}
	k0 := logSpikeKey(base, from, to, "elasticsearch", []string{"k8s.pod.name", "log.logger"})
	if !strings.HasPrefix(k0, "logspike:v1:") {
		t.Fatalf("anahtar öneki: %s", k0)
	}
	// Alan sırası anahtarı değiştirmez (sıralı).
	if k := logSpikeKey(base, from, to, "elasticsearch", []string{"log.logger", "k8s.pod.name"}); k != k0 {
		t.Fatal("alan sırası anahtarı değiştirdi")
	}
	// Aynı 30 sn kovası → aynı anahtar.
	if k := logSpikeKey(base, from.Add(10*time.Second), to.Add(5*time.Second), "elasticsearch", []string{"k8s.pod.name", "log.logger"}); k != k0 {
		t.Fatal("aynı 30 sn kovası farklı anahtar üretti")
	}
	given := base
	given.SpikeFrom, given.SpikeTo = from, to
	variants := map[string]string{
		"service": logSpikeKey(LogSpikeRequest{Service: "pay", Env: "prod", Query: "timeout", Level: "all"}, from, to, "elasticsearch", []string{"k8s.pod.name", "log.logger"}),
		"env":     logSpikeKey(LogSpikeRequest{Service: "checkout", Env: "uat", Query: "timeout", Level: "all"}, from, to, "elasticsearch", []string{"k8s.pod.name", "log.logger"}),
		"query":   logSpikeKey(LogSpikeRequest{Service: "checkout", Env: "prod", Query: "timeouts", Level: "all"}, from, to, "elasticsearch", []string{"k8s.pod.name", "log.logger"}),
		"level":   logSpikeKey(LogSpikeRequest{Service: "checkout", Env: "prod", Query: "timeout", Level: "error"}, from, to, "elasticsearch", []string{"k8s.pod.name", "log.logger"}),
		"from":    logSpikeKey(base, from.Add(-time.Minute), to, "elasticsearch", []string{"k8s.pod.name", "log.logger"}),
		"to":      logSpikeKey(base, from, to.Add(time.Minute), "elasticsearch", []string{"k8s.pod.name", "log.logger"}),
		"source":  logSpikeKey(given, from, to, "elasticsearch", []string{"k8s.pod.name", "log.logger"}),
		"backend": logSpikeKey(base, from, to, "clickhouse", []string{"k8s.pod.name", "log.logger"}),
		"fields":  logSpikeKey(base, from, to, "elasticsearch", []string{"k8s.pod.name"}),
		// Sınır kaybolmaz: "a"+"b" ile "ab"+"" ayrışır.
		"boundary": logSpikeKey(LogSpikeRequest{Service: "checkoutprod", Env: "", Query: "timeout", Level: "all"}, from, to, "elasticsearch", []string{"k8s.pod.name", "log.logger"}),
	}
	seen := map[string]string{k0: "base"}
	for name, k := range variants {
		if prev, dup := seen[k]; dup {
			t.Errorf("%s girdisi anahtarı değiştirmedi (%s ile aynı)", name, prev)
		}
		seen[k] = name
	}
}
