package anomaly

// calendar_export.go — v0.10.1494 (CoSRE 2d): takvimli gün sınıfının
// (v0.10.1483 dayClassCal) dışa açık ikizi. AI SRE baseline okuyucusu
// (internal/sre readers_baseline.go) "şimdi" ile taban penceresinin gün
// sınıfı farklıysa (ör. 7 gün önce tatil) satıra dürüst not düşer — kıyası
// DEĞİŞTİRMEZ, yalnız söyler. Sınıf kuralı tek yerde (calendar.go).

import (
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// CalendarDayClass — SAF: weekday | saturday | sunday | holiday | half_day |
// month_end (takvim boşsa dayClass ile aynı).
func CalendarDayClass(t time.Time, cal chstore.AnomalyCalendarConfig) string {
	return dayClassCal(t, cal)
}
