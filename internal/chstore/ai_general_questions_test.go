package chstore

// ai_general_questions_test.go — v0.10.1458: system_settings
// `ai.generalQuestions` blobunun saf kuralları — blob yokken AÇIK (operatör
// kararı 2026-10-23), "allow" alanı olmayan blob da açık, yalnız açık false
// kapatır, bozuk blob son değeri korur.

import (
	"testing"
	"time"
)

func TestAIGeneralQuestionsDefaultOnWhenBlobMissing(t *testing.T) {
	aiGeneralQuestionsCur.Store(nil)
	if !CurrentAIGeneralQuestions().Allow {
		t.Fatal("yüklenmemişken varsayılan AÇIK olmalı")
	}
	if !DefaultAIGeneralQuestions().Allow {
		t.Fatal("DefaultAIGeneralQuestions().Allow = false")
	}
	cases := []struct {
		raw  string
		want bool
	}{
		{"", true},
		{`{}`, true},
		{`{"updatedBy":"a"}`, true},
		{`{"allow":true}`, true},
		{`{"allow":false}`, false},
	}
	for _, c := range cases {
		a, ok := parseAIGeneralQuestions([]byte(c.raw))
		if !ok || a.Allow != c.want {
			t.Errorf("parse(%q) = %+v ok=%v, want allow=%v", c.raw, a, ok, c.want)
		}
	}
	if _, ok := parseAIGeneralQuestions([]byte(`{bozuk`)); ok {
		t.Error("bozuk blob ok=false dönmeli (son değer korunur)")
	}
}

func TestAIGeneralQuestionsSetRoundTrip(t *testing.T) {
	t.Cleanup(func() { aiGeneralQuestionsCur.Store(nil) })
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	SetAIGeneralQuestions(AIGeneralQuestions{Allow: false, UpdatedBy: " admin@example.test ", UpdatedAt: at})
	got := CurrentAIGeneralQuestions()
	if got.Allow || got.UpdatedBy != "admin@example.test" || !got.UpdatedAt.Equal(at) {
		t.Fatalf("Current = %+v", got)
	}
}
