package chstore

// v0.10.1483 — takvim bölümü: normalize + gün türü + settings round-trip.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAnomalyCalendar(t *testing.T) {
	in := AnomalyCalendarConfig{MonthEndBusinessDays: 9, Days: []AnomalySpecialDay{
		{From: "2031-03-12", To: "2031-03-14", Label: "  Bayram  ", Kind: CalendarHoliday},
		{From: "2031-01-01", Label: "Yılbaşı", Kind: CalendarHoliday},
		{From: "2031-13-01", Kind: CalendarHoliday},                    // geçersiz tarih → düşer
		{From: "2031-02-01", Kind: "festival"},                         // geçersiz tür → düşer
		{From: "2031-04-10", To: "2031-04-01", Kind: CalendarHalfDay},  // ters aralık → tek gün
		{From: "2031-06-01", To: "2031-09-01", Kind: CalendarMonthEnd}, // 31 gün tavanı
		{From: "2031-07-01", Label: strings.Repeat("ş", 80), Kind: CalendarHoliday},
	}}
	out := NormalizeAnomalyCalendar(in)
	if out.MonthEndBusinessDays != CalendarMonthEndDefaultN || out.MonthEndAuto {
		t.Errorf("N/auto: %+v", out)
	}
	got := []string{}
	for _, d := range out.Days {
		got = append(got, d.From+"~"+d.To+"~"+d.Kind)
	}
	want := "2031-01-01~~holiday,2031-03-12~2031-03-14~holiday,2031-04-10~~half-day,2031-06-01~2031-07-01~month-end-like,2031-07-01~~holiday"
	if strings.Join(got, ",") != want {
		t.Errorf("satırlar:\n got %s\nwant %s", strings.Join(got, ","), want)
	}
	if out.Days[1].Label != "Bayram" || len([]rune(out.Days[4].Label)) != calendarLabelMaxRunes {
		t.Errorf("etiket: %q / %d", out.Days[1].Label, len([]rune(out.Days[4].Label)))
	}
	if !reflect.DeepEqual(NormalizeAnomalyCalendar(out), out) {
		t.Error("normalize idempotent olmalı")
	}
	if e := NormalizeAnomalyCalendar(AnomalyCalendarConfig{}); e.Days == nil || len(e.Days) != 0 {
		t.Error("boş takvim [] yazılmalı (null değil)")
	}
}

func TestCalendarSpecialKindOn(t *testing.T) {
	c := NormalizeAnomalyCalendar(AnomalyCalendarConfig{Days: []AnomalySpecialDay{
		{From: "2031-03-12", To: "2031-03-14", Kind: CalendarHoliday},
		{From: "2031-03-14", Kind: CalendarHalfDay},
		{From: "2031-03-20", Kind: CalendarMonthEnd},
	}})
	at := func(s string) time.Time { t, _ := time.Parse(time.RFC3339, s); return t }
	for s, want := range map[string]string{
		"2031-03-11T23:59:00Z": "", "2031-03-12T00:00:00Z": CalendarHoliday, "2031-03-14T12:00:00Z": CalendarHoliday,
		"2031-03-15T00:00:00Z": "", "2031-03-20T08:00:00Z": CalendarMonthEnd,
		"2031-03-12T01:00:00+03:00": "", // UTC günü 2031-03-11
	} {
		if got := c.SpecialKindOn(at(s)); got != want {
			t.Errorf("%s: %q, beklenen %q", s, got, want)
		}
	}
}

// TestAnomalySensitivityCalendarRoundTrip — PUT yolu (Normalize) ve blob
// JSON'u takvimi taşır; eski blob (bölüm yok) takvimsiz okunur.
func TestAnomalySensitivityCalendarRoundTrip(t *testing.T) {
	cfg := DefaultAnomalySensitivity()
	cfg.Calendar = AnomalyCalendarConfig{MonthEndAuto: true, MonthEndBusinessDays: 3, Days: []AnomalySpecialDay{{From: "2031-01-01", Label: "Yılbaşı", Kind: CalendarHoliday}}}
	n := NormalizeAnomalySensitivity(cfg)
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var back AnomalySensitivityConfig
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	back = NormalizeAnomalySensitivity(back)
	if !reflect.DeepEqual(back.Calendar, n.Calendar) || !back.Calendar.MonthEndAuto || back.Calendar.MonthEndBusinessDays != 3 || len(back.Calendar.Days) != 1 {
		t.Errorf("round-trip: %+v", back.Calendar)
	}
	var legacy AnomalySensitivityConfig
	if err := json.Unmarshal([]byte(`{"dwellBuckets":3,"criticalZ":6}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if l := NormalizeAnomalySensitivity(legacy).Calendar; l.MonthEndAuto || len(l.Days) != 0 || l.MonthEndBusinessDays != CalendarMonthEndDefaultN {
		t.Errorf("eski blob: %+v", l)
	}
}

// TestCalendarTimezone — v0.10.1490: özel gün üyeliği takvim diliminin yerel
// günü; boş = UTC (eski davranış); bilinmeyen ad doğrulamada reddedilir,
// Normalize'da UTC'ye (boş) düşer.
func TestCalendarTimezone(t *testing.T) {
	base := AnomalyCalendarConfig{Days: []AnomalySpecialDay{{From: "2031-01-01", Label: "Yılbaşı", Kind: CalendarHoliday}}}
	ist := base
	ist.Timezone = "Europe/Istanbul"
	ist = NormalizeAnomalyCalendar(ist)
	utc := NormalizeAnomalyCalendar(base)
	at := func(s string) time.Time { t, _ := time.Parse(time.RFC3339, s); return t }
	cases := []struct {
		name string
		cal  AnomalyCalendarConfig
		at   string
		want string
	}{
		{"İstanbul: yerel 00:00 (UTC 21:00 önceki gün) tatil", ist, "2030-12-31T21:00:00Z", CalendarHoliday},
		{"İstanbul: yerel 02:59 tatil", ist, "2030-12-31T23:59:00Z", CalendarHoliday},
		{"İstanbul: yerel 23:59 önceki gün değil", ist, "2030-12-31T20:59:00Z", ""},
		{"İstanbul: yerel 23:59 tatil son dakika", ist, "2031-01-01T20:59:00Z", CalendarHoliday},
		{"İstanbul: yerel ertesi gün 00:00 değil", ist, "2031-01-01T21:00:00Z", ""},
		{"UTC (boş): UTC 21:00 önceki gün tatil değil", utc, "2030-12-31T21:00:00Z", ""},
		{"UTC (boş): UTC 23:59 tatil", utc, "2031-01-01T23:59:00Z", CalendarHoliday},
	}
	for _, c := range cases {
		if got := c.cal.SpecialKindOn(at(c.at)); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
	for name, ok := range map[string]bool{"": true, "UTC": true, "Europe/Istanbul": true, "Europe/Berlin": true, " Europe/Istanbul ": true,
		"Mars/Olympus": false, "Local": false, "local": false, "Europe/Istanbul/x": false, "+03:00": false} {
		if err := ValidateCalendarTimezone(name); (err == nil) != ok {
			t.Errorf("ValidateCalendarTimezone(%q) = %v", name, err)
		}
	}
	bad := NormalizeAnomalyCalendar(AnomalyCalendarConfig{Timezone: "Mars/Olympus"})
	if bad.Timezone != "" || bad.Location() != time.UTC {
		t.Errorf("geçersiz dilim UTC'ye düşmeli: %+v", bad)
	}
	if NormalizeAnomalyCalendar(AnomalyCalendarConfig{Timezone: " Europe/Istanbul "}).Timezone != "Europe/Istanbul" {
		t.Error("dilim kırpılmalı")
	}
}

// TestCalendarDayWindowUTC — yerel günün UTC anları: İstanbul sabit +3, Berlin
// DST günleri 23 / 25 saat, UTC eski davranış.
func TestCalendarDayWindowUTC(t *testing.T) {
	cases := []struct {
		tz, date, start string
		hours           float64
	}{
		{"", "2031-01-01", "2031-01-01T00:00:00Z", 24},
		{"Europe/Istanbul", "2031-01-01", "2030-12-31T21:00:00Z", 24},
		{"Europe/Berlin", "2031-01-01", "2030-12-31T23:00:00Z", 24},
		{"Europe/Berlin", "2031-03-30", "2031-03-29T23:00:00Z", 23}, // yaz saatine geçiş
		{"Europe/Berlin", "2031-10-26", "2031-10-25T22:00:00Z", 25}, // kış saatine dönüş
		{"Europe/Berlin", "2031-07-01", "2031-06-30T22:00:00Z", 24},
	}
	for _, c := range cases {
		s, e, ok := AnomalyCalendarConfig{Timezone: c.tz}.DayWindowUTC(c.date)
		if !ok || s.Format(time.RFC3339) != c.start || e.Sub(s).Hours() != c.hours || s.Location() != time.UTC {
			t.Errorf("%s %s: [%v, %v) ok=%v", c.tz, c.date, s, e, ok)
		}
	}
	if _, _, ok := (AnomalyCalendarConfig{}).DayWindowUTC("2031-13-01"); ok {
		t.Error("geçersiz tarih")
	}
}

// TestAnomalySensitivityCalendarTimezoneRoundTrip — dilim blob'da taşınır;
// eski blob (alan yok) boş = UTC okunur; boş dilim JSON'a yazılmaz.
func TestAnomalySensitivityCalendarTimezoneRoundTrip(t *testing.T) {
	cfg := DefaultAnomalySensitivity()
	if cfg.Calendar.Timezone != "" {
		t.Fatalf("varsayılan dilim boş (UTC) olmalı: %q", cfg.Calendar.Timezone)
	}
	cfg.Calendar.Timezone = "Europe/Istanbul"
	b, _ := json.Marshal(NormalizeAnomalySensitivity(cfg))
	var back AnomalySensitivityConfig
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if NormalizeAnomalySensitivity(back).Calendar.Timezone != "Europe/Istanbul" {
		t.Errorf("round-trip dilim: %s", b)
	}
	b, _ = json.Marshal(NormalizeAnomalyCalendar(AnomalyCalendarConfig{}))
	if strings.Contains(string(b), "timezone") {
		t.Errorf("boş dilim yazılmamalı: %s", b)
	}
}
