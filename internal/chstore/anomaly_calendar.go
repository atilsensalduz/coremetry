package chstore

// anomaly_calendar.go — v0.10.1483: mevsimsel baseline'ların TAKVİM ETKİLERİ.
//
// Mevsimsel baseline gün sınıfıyla eşleşir (hafta içi / cumartesi / pazar);
// resmi tatil hafta içine düşünce o günün trafiği bir pazarınkine benzer ama
// baseline sıradan bir hafta içi günüdür → yapay "trafik düşüşü" anomalisi.
// Operatör özel günleri (tarih ya da tarih aralığı, etiket, tür) anomaly
// ayar blob'unun (system_settings "anomaly_sensitivity") Calendar bölümüne
// yazar; dedektör (internal/anomaly/calendar.go) o gün sınıfı değiştirir.
//
// Tarihler UTC takvim günüdür (YYYY-MM-DD): mevsimsel motorun tamamı UTC'ye
// pinli (v0.8.323) ve SQL tarafı toDate(time_bucket, 'UTC') ile aynı günü görür.

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Özel gün türleri (JSON değerleri).
const (
	CalendarHoliday  = "holiday"
	CalendarHalfDay  = "half-day"
	CalendarMonthEnd = "month-end-like"
)

const (
	calendarMaxDays       = 200 // satır tavanı (elle düzenlenmiş blob sınırsız büyümesin)
	calendarMaxSpanDays   = 31  // tek satırın aralık tavanı
	calendarLabelMaxRunes = 64
	// CalendarMonthEndDefaultN — otomatik ay sonu sınıfı: ayın son N iş günü.
	CalendarMonthEndDefaultN = 2
	calendarMonthEndMaxN     = 5
)

// AnomalySpecialDay — tek özel gün satırı. To boş = tek gün.
type AnomalySpecialDay struct {
	From  string `json:"from"`
	To    string `json:"to,omitempty"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// AnomalyCalendarConfig — takvim bölümü. Sıfır değer = takvim yok (eski
// blob'lar bu bölümü taşımaz; davranış bayt bayt eski).
type AnomalyCalendarConfig struct {
	Days []AnomalySpecialDay `json:"days"`
	// MonthEndAuto — ayın son MonthEndBusinessDays iş günü otomatik
	// "month-end-like" sınıfı (varsayılan KAPALI).
	MonthEndAuto         bool `json:"monthEndAuto"`
	MonthEndBusinessDays int  `json:"monthEndBusinessDays"`
}

// calendarDate — SAF: "YYYY-MM-DD" → UTC gün başı.
func calendarDate(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// validCalendarKind — SAF.
func validCalendarKind(k string) bool {
	switch k {
	case CalendarHoliday, CalendarHalfDay, CalendarMonthEnd:
		return true
	}
	return false
}

// NormalizeAnomalyCalendar — SAF: geçersiz tarih / tür taşıyan satır DÜŞER
// (kelepçe değil: yanlış bir tatil günü sessizce başka güne kaymasın);
// To < From → tek gün; aralık en çok 31 gün; etiket kırpılır; From sırası;
// satır tavanı 200; MonthEndBusinessDays 1..5 (aralık dışı → 2).
func NormalizeAnomalyCalendar(c AnomalyCalendarConfig) AnomalyCalendarConfig {
	out := AnomalyCalendarConfig{Days: []AnomalySpecialDay{}, MonthEndAuto: c.MonthEndAuto, MonthEndBusinessDays: c.MonthEndBusinessDays}
	if out.MonthEndBusinessDays < 1 || out.MonthEndBusinessDays > calendarMonthEndMaxN {
		out.MonthEndBusinessDays = CalendarMonthEndDefaultN
	}
	for _, d := range c.Days {
		kind := strings.TrimSpace(d.Kind)
		from, ok := calendarDate(d.From)
		if !ok || !validCalendarKind(kind) {
			continue
		}
		to := from
		if t, ok := calendarDate(d.To); ok && !t.Before(from) {
			to = t
		}
		if span := int(to.Sub(from).Hours() / 24); span >= calendarMaxSpanDays {
			to = from.AddDate(0, 0, calendarMaxSpanDays-1)
		}
		label := strings.TrimSpace(d.Label)
		if utf8.RuneCountInString(label) > calendarLabelMaxRunes {
			label = string([]rune(label)[:calendarLabelMaxRunes])
		}
		row := AnomalySpecialDay{From: from.Format("2006-01-02"), Label: label, Kind: kind}
		if !to.Equal(from) {
			row.To = to.Format("2006-01-02")
		}
		out.Days = append(out.Days, row)
	}
	sort.SliceStable(out.Days, func(i, j int) bool { return out.Days[i].From < out.Days[j].From })
	if len(out.Days) > calendarMaxDays {
		out.Days = out.Days[len(out.Days)-calendarMaxDays:] // en yeniler kalır
	}
	return out
}

// SpecialKindOn — SAF: t'nin UTC gününün AÇIK satır türü ("" = yok). Birden
// çok satır aynı güne düşerse öncelik holiday > half-day > month-end-like.
func (c AnomalyCalendarConfig) SpecialKindOn(t time.Time) string {
	day := t.UTC().Format("2006-01-02")
	best := ""
	rank := map[string]int{CalendarHoliday: 3, CalendarHalfDay: 2, CalendarMonthEnd: 1}
	for _, d := range c.Days {
		to := d.To
		if to == "" {
			to = d.From
		}
		if day >= d.From && day <= to && rank[d.Kind] > rank[best] {
			best = d.Kind
		}
	}
	return best
}
