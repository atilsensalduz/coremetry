package selfobs

// sampler.go — v0.10.1463 (CoSRE program D1, karar 7): ada duyarlı kök
// örnekleyici.
//
// Neden: self-telemetri örnekleyicisi ParentBased(TraceIDRatioBased(0.1)).
// Arka plan araştırmasının kök span'ı (sre.investigation) bu oranla
// açılsaydı araştırmaların %90'ı iz bırakmazdı — operatörün "bu araştırma
// neden 40 sn sürdü / hangi okuyucu boş döndü" sorusu tek tük cevaplanırdı.
// Hacim zaten sınırlı (saatte ≤ sre_agent.maxLLMCallsPerHour anlatım +
// kuyruk tavanı kadar deterministik koşu), o yüzden bu kökler HER ZAMAN
// örneklenir; geri kalan her kök (HTTP istekleri, sohbet, CH sorguları) eski
// oranla devam eder. Çocuklar ParentBased ile kökün kararını izler.
//
// Ingest yolunda örnekleme YOK kuralı (/otel-conventions §6) etkilenmez: bu
// yalnız Coremetry'nin KENDİ SDK'sı; gelen OTLP verisine dokunmaz.
//
// Ayar: COREMETRY_SELF_OBS_ALWAYS_SAMPLE (virgüllü span adları; varsayılan
// "sre.investigation"; "-" = hiçbiri, eski davranış).

import (
	"os"
	"strings"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const envAlwaysSample = "COREMETRY_SELF_OBS_ALWAYS_SAMPLE"

// DefaultAlwaysSample — her zaman örneklenen kök span adları.
const DefaultAlwaysSample = "sre.investigation"

// rootSampler — kök span adı always kümesindeyse RecordAndSample, aksi
// halde oran örnekleyicisi. ParentBased'in KÖK dalına takılır.
type rootSampler struct {
	always map[string]bool
	ratio  sdktrace.Sampler
	desc   string
}

func newRootSampler(always []string, ratio float64) rootSampler {
	m := make(map[string]bool, len(always))
	for _, n := range always {
		if n = strings.TrimSpace(n); n != "" {
			m[n] = true
		}
	}
	r := sdktrace.TraceIDRatioBased(ratio)
	return rootSampler{always: m, ratio: r, desc: "CoremetryRootSampler{always=" + strings.Join(always, ",") + "," + r.Description() + "}"}
}

func (s rootSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	if s.always[p.Name] {
		res := s.ratio.ShouldSample(p) // tracestate'i aynı yoldan taşı
		res.Decision = sdktrace.RecordAndSample
		return res
	}
	return s.ratio.ShouldSample(p)
}

func (s rootSampler) Description() string { return s.desc }

// parseAlwaysSample — SAF: env değeri → ad listesi. Boş = varsayılan,
// "-" = hiçbiri.
func parseAlwaysSample(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultAlwaysSample
	}
	if raw == "-" {
		return nil
	}
	var out []string
	for _, n := range strings.Split(raw, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// newSelfObsSampler — Init'in örnekleyicisi.
func newSelfObsSampler(ratio float64) sdktrace.Sampler {
	return sdktrace.ParentBased(newRootSampler(parseAlwaysSample(os.Getenv(envAlwaysSample)), ratio))
}
