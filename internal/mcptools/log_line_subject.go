package mcptools

// log_line_subject.go — v0.10.1291 (CoSRE follow-up D): tek log SATIRININ ✨
// öznesi. Sohbetteki log tablosu (query_logs, explain_log_spike örnek
// satırları) her satır için bu kimliği `rowSubjects` olarak taşır; Logs
// sayfasının açılan satırı AYNI kodeği istemcide üretir
// (frontend/src/lib/logLineSubject.ts). Uç POST /api/copilot/explain-log
// satırı logstore.Store üzerinden YENİDEN okur (ES ya da CH — doğrudan ES
// yok) ve gövde özetiyle eşler.
//
// Biçim (lib/aiSubject.ts `log` türü):
//
//	log:<tsNs>:<enc(service)>:<enc(pod)>:<traceId>:<fnv32a(gövde önü) 8 hex>
//
// Gövde özeti gövdenin İLK 120 rune'u üzerinden: sohbet satırı 300 rune'da
// kırpılır ve FenceSafe ``` → ˋˋˋ yapar; ön ek ve ˋ→` geri dönüşü iki
// taraftaki özetin aynı çıkmasını sağlar (tam gövde / kırpık gövde).

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
)

// LogBodyHashRunes — özetin baktığı gövde öneki (rune).
const LogBodyHashRunes = 120

// LogBodyHash — SAF: gövde önekinin FNV-1a 32 özeti (8 küçük hex).
func LogBodyHash(body string) string {
	r := []rune(body)
	if len(r) > LogBodyHashRunes {
		r = r[:LogBodyHashRunes]
	}
	for i, c := range r {
		if c == 'ˋ' {
			r[i] = '`'
		}
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(string(r)))
	return fmt.Sprintf("%08x", h.Sum32())
}

// LogLineSubject — SAF: satırın ✨ öznesi; servis ya da zaman yoksa "" (uç
// satırı bulamaz — buton hiç çizilmez).
func LogLineSubject(tsNs int64, service, pod, traceID, body string) string {
	service = strings.TrimSpace(service)
	if tsNs <= 0 || service == "" {
		return ""
	}
	if !isHex32(traceID) {
		traceID = ""
	}
	return "log:" + strconv.FormatInt(tsNs, 10) + ":" + EncodeURIComponent(service) + ":" +
		EncodeURIComponent(strings.TrimSpace(pod)) + ":" + strings.ToLower(traceID) + ":" + LogBodyHash(body)
}

func isHex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// EncodeURIComponent — SAF: JS encodeURIComponent ile bayt-aynı kaçış
// (url.QueryEscape boşluğu '+' yapar; istemcinin decodeURIComponent'i onu
// geri çevirmez).
func EncodeURIComponent(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}
