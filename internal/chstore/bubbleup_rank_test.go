package chstore

import (
	"context"
	"testing"
)

// v0.10.1495 regresyon testi — operatör (prod, JVM GC problemi): "Where errors
// concentrate" `messaging.message.body.size` gösterdi (749, 725, 731…). Ölçü /
// kimlik / zaman damgası attribute'ları sıralamaya girmez; dağınık sayısal
// değerli attribute elenir, kategorik sayılar (durum kodu) kalır.

func TestUnrankableAttributeKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		// v0.10.1495 — ölçü
		{"messaging.message.body.size", true},
		{"http.request.body.size", true},
		{"http.response.header.content_length", true},
		{"messaging.batch.message_count", true},
		{"db.response.returned_rows", true},
		{"messaging.kafka.message.offset", true},
		{"kafka.offset.committed", true},
		// zaman damgası
		{"app.event.timestamp", true},
		{"job.start_time", true},
		// kimlik (eski kurallar + yeniler)
		{"trace_id", true},
		{"user.id", true},
		{"order_id", true},
		{"id", true},
		{"session.uuid", true},
		{"x.request_id.header", true},
		{"correlation", true},
		{"messaging.message.id", true},
		{"", true},
		// kategorik — SIRALAMAYA GİRER
		{"http.route", false},
		{"http.response.status_code", false},
		{"k8s.pod.name", false},
		{"service.version", false},
		{"db.system", false},
		{"messaging.destination.name", false},
		{"messaging.kafka.partition", false},
		{"error.type", false},
		{"rpc.method", false},
	}
	for _, c := range cases {
		if got := isUnrankableAttributeKey(c.key); got != c.want {
			t.Errorf("isUnrankableAttributeKey(%q) = %v, want %v", c.key, got, c.want)
		}
		if got := isHighCardinalityKey(c.key); got != c.want {
			t.Errorf("isHighCardinalityKey(%q) = %v, want %v (aynı süzgeç)", c.key, got, c.want)
		}
	}
}

func TestScatteredNumericAttribute(t *testing.T) {
	vals := func(pairs ...any) []BubbleUpValue {
		var out []BubbleUpValue
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, BubbleUpValue{Value: pairs[i].(string), SelectionPct: pairs[i+1].(float64)})
		}
		return out
	}
	cases := []struct {
		name string
		vals []BubbleUpValue
		want bool
	}{
		{"prod: body.size dağınık sayılar", vals("749", 0.04, "725", 0.03, "731", 0.03, "752", 0.02, "740", 0.02, "733", 0.02), true},
		{"dağınık ondalık sayılar", vals("12.5", 0.1, "13.25", 0.1, "-4", 0.1), true},
		{"kategorik sayı: durum kodu 500 yoğun", vals("500", 0.9, "503", 0.05), false},
		{"kategorik sayı: bölüm, kapsam tam", vals("0", 0.34, "1", 0.33, "2", 0.33), false},
		{"sınır: kapsam tam %50 kalır", vals("1", 0.25, "2", 0.25), false},
		{"metin değerler — elenmez", vals("/checkout", 0.02, "/cart", 0.01), false},
		{"karışık sayı + metin — elenmez", vals("749", 0.01, "n/a", 0.01), false},
		{"boş liste — elenmez (çağıran atlar)", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isScatteredNumericAttribute(BubbleUpAttribute{Key: "k", Values: c.vals}); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// TestRunBubbleUpDropsScatteredNumeric — orkestrasyon düzeyi: dağınık sayısal
// attribute sonuçta YOK, kategorik olan kalır; hepsi elenirse dizi boş (null değil).
func TestRunBubbleUpDropsScatteredNumeric(t *testing.T) {
	ops := bubbleUpOps{
		totals: func(ctx context.Context) (uint64, uint64, error) { return 100, 1000, nil },
		keys: func(ctx context.Context) ([]string, error) {
			return []string{"messaging.message.body.size", "app.payload", "http.route"}, nil
		},
		perKey: func(ctx context.Context, key string, _, _ uint64) (BubbleUpAttribute, error) {
			switch key {
			case "app.payload": // adı süzgeçten geçer, değerleri dağınık sayı
				return BubbleUpAttribute{Key: key, Values: []BubbleUpValue{{Value: "749", SelectionPct: 0.05, Score: 0.04}}}, nil
			case "http.route":
				return BubbleUpAttribute{Key: key, Values: []BubbleUpValue{{Value: "/pay", SelectionPct: 0.8, Score: 0.3}}}, nil
			}
			t.Errorf("ad süzgecinin elemesi gereken anahtar sorgulandı: %s", key)
			return BubbleUpAttribute{Key: key}, nil
		},
	}
	res, err := runBubbleUp(context.Background(), ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Attributes) != 1 || res.Attributes[0].Key != "http.route" {
		t.Fatalf("attributes = %+v, want yalnız http.route", res.Attributes)
	}
}
