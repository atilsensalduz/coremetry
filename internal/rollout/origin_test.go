package rollout

// origin_test.go — v0.10.1445 — geri almanın kaynağı sınıflandırıcısı
// (origin.go): plan §2.2 doğruluk tablosu kural 0–7, "Argo kanıtı yoksa her
// zaman unknown" değişmezi ve imaj tag sıralaması.

import (
	"strings"
	"testing"
)

func TestClassifyRolloutOriginTable(t *testing.T) {
	base := OriginInput{ArgoNewRevision: "4b7d0aa3", ArgoPrevRevision: "9f3c2e1b", HistoryDepth: 7, WindowMin: 5}
	with := func(f func(*OriginInput)) OriginInput { in := base; f(&in); return in }
	cases := []struct {
		name       string
		in         OriginInput
		want       Origin
		conf       uint8
		reasonPart string
	}{
		{"0 ambiguous", with(func(i *OriginInput) {
			i.Ambiguous, i.AmbiguousApps, i.AmbiguousSyncs, i.KSMChangeType = true, 2, 1, V2ChangeRollback
			i.SeenBefore = true
		}), OriginUnknown, 0, "ambiguous: 2 Argo apps"},
		{"1 no argo (KSM rollback)", with(func(i *OriginInput) { i.ArgoNewRevision, i.KSMChangeType = "", V2ChangeRollback }), OriginUnknown, 0, "no Argo sync matched this rollout (±5m)"},
		{"2 same revision", with(func(i *OriginInput) { i.ArgoPrevRevision, i.KSMChangeType = "4b7d0aa3", V2ChangeConfig }), OriginUnknown, 0, "same git revision"},
		{"3 seen + KSM rollback", with(func(i *OriginInput) { i.SeenBefore, i.KSMChangeType = true, V2ChangeRollback }), OriginRollback, 95, "re-activated the known ReplicaSet"},
		{"4 seen + rollout", with(func(i *OriginInput) { i.SeenBefore, i.KSMChangeType = true, V2ChangeRollout }), OriginRollback, 75, "garbage-collected"},
		{"4 seen + config", with(func(i *OriginInput) { i.SeenBefore, i.KSMChangeType = true, V2ChangeConfig }), OriginRollback, 75, "garbage-collected"},
		{"4 seen + initial", with(func(i *OriginInput) { i.SeenBefore, i.KSMChangeType = true, V2ChangeInitial }), OriginRollback, 75, "garbage-collected"},
		{"5 new rev + KSM rollback", with(func(i *OriginInput) { i.KSMChangeType = V2ChangeRollback }), OriginRedeployOlder, 85, "history depth 7"},
		{"5 shallow history", with(func(i *OriginInput) { i.KSMChangeType, i.HistoryDepth = V2ChangeRollback, 2 }), OriginRedeployOlder, 70, "history depth 2"},
		{"6 rollout + older tag", with(func(i *OriginInput) {
			i.KSMChangeType, i.PrevTag, i.NewTag = V2ChangeRollout, "2026.10.18.3", "2026.10.17.1"
		}), OriginRedeployOlder, 60, "tag ordering only"},
		{"7 rollout + newer tag", with(func(i *OriginInput) { i.KSMChangeType, i.PrevTag, i.NewTag = V2ChangeRollout, "1.4.2", "1.5.0" }), OriginForward, 80, "new build"},
		{"7 rollout + sha tags", with(func(i *OriginInput) { i.KSMChangeType, i.PrevTag, i.NewTag = V2ChangeRollout, "9f3c2e1", "1a2b3c4" }), OriginForward, 80, "new build"},
		{"7 config", with(func(i *OriginInput) { i.KSMChangeType = V2ChangeConfig }), OriginForward, 80, "config-only"},
		{"7 initial", with(func(i *OriginInput) { i.KSMChangeType = V2ChangeInitial }), OriginForward, 80, "first rollout"},
		{"unknown change type", with(func(i *OriginInput) { i.KSMChangeType = "" }), OriginUnknown, 0, "unknown"},
	}
	for _, c := range cases {
		got := ClassifyRolloutOrigin(c.in)
		if got.Origin != c.want || got.Confidence != c.conf || !strings.Contains(got.Reason, c.reasonPart) {
			t.Errorf("%s: %+v; beklenen %s/%d ⊃ %q", c.name, got, c.want, c.conf, c.reasonPart)
		}
	}
	if got := ClassifyRolloutOrigin(with(func(i *OriginInput) {
		i.KSMChangeType, i.PrevTag, i.NewTag = V2ChangeRollout, "2026.10.18.3", "2026.10.17.1"
	})); got.TagOrder != TagOlder {
		t.Errorf("TagOrder: %s", got.TagOrder)
	}
}

// Değişmez: Argo kanıtı (yeni revizyon) yoksa geri kalan her girdi
// kombinasyonunda sonuç unknown — tag sırası tek başına geri alma kanıtı değil.
func TestClassifyRolloutOriginNoArgoIsAlwaysUnknown(t *testing.T) {
	tags := [][2]string{{"", ""}, {"2026.10.18.3", "2026.10.17.1"}, {"1.0.0", "2.0.0"}, {"latest", "latest"}, {"abc1234", "def5678"}}
	for _, ct := range []string{V2ChangeRollout, V2ChangeConfig, V2ChangeRollback, V2ChangeInitial, "", "weird"} {
		for _, tg := range tags {
			for _, seen := range []bool{false, true} {
				for _, prev := range []string{"", "9f3c2e1b"} {
					for _, depth := range []int{0, 1, 10} {
						in := OriginInput{KSMChangeType: ct, PrevTag: tg[0], NewTag: tg[1], ArgoPrevRevision: prev, SeenBefore: seen, HistoryDepth: depth}
						for _, nr := range []string{"", "   "} {
							in.ArgoNewRevision = nr
							if got := ClassifyRolloutOrigin(in); got.Origin != OriginUnknown || got.Confidence != 0 {
								t.Fatalf("Argo kanıtı yok ama %+v → %+v", in, got)
							}
						}
					}
				}
			}
		}
	}
}

func TestCompareImageTags(t *testing.T) {
	cases := []struct {
		prev, new string
		want      TagOrder
	}{
		{"1.4.2", "1.5.0", TagNewer},
		{"v1.10.0", "v1.9.9", TagOlder},
		{"1.4.2", "1.4.2", TagEqual},
		{"2026.10.18.3", "2026.10.17.1", TagOlder},
		{"2026.10.17.1", "2026.10.18.3", TagNewer},
		{"release-2026.08.21.3", "release-2026.08.22.1", TagNewer},
		{"release-2026.08.21.3", "release-2026.08.21.2", TagOlder},
		{"9f3c2e1", "1a2b3c4", TagUnordered}, // 7-hex sha
		{"9f3c2e1b4d5e6f708192a3b4c5d6e7f809102030", "0a1b2c3d4e5f60718293a4b5c6d7e8f901122334", TagUnordered}, // 40-hex sha
		{"1.2.3", "9f3c2e1", TagUnordered},
		{"latest", "1.2.3", TagUnordered},
		{"1.2.3", "latest", TagUnordered},
		{"0.0.1-SNAPSHOT", "1.2.3", TagUnordered},
		{"1.2.3", "2026.10.18.3", TagUnordered}, // karışık iskelet
		{"1.2.3-rc1", "1.2.3", TagUnordered},
		{"stable", "canary", TagUnordered},
		{"", "1.2.3", TagUnordered},
		{"20261018", "20261017", TagOlder}, // yalnız rakam (hex harfi yok) → sayı
		{"build-99", "build-100", TagNewer},
	}
	for _, c := range cases {
		if got := CompareImageTags(c.prev, c.new); got != c.want {
			t.Errorf("CompareImageTags(%q, %q) = %s, beklenen %s", c.prev, c.new, got, c.want)
		}
	}
}
