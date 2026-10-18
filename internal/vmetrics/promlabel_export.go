package vmetrics

// PromLabel — v0.10.1350: promLabel'ın dışa açık kapısı. internal/api
// kullanıcıya okunan notta VM etiket yazımını (http_response_status_code)
// gösterir ve frontend lib/httpMetrics.ts HTTP_LABELS ile eşliği pinler —
// ikisi aynı sanitizasyondan geçmezse ayrışırdı.
func PromLabel(k string) string { return promLabel(k) }
