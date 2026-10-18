package api

// service_metadata_links.go — v0.10.1341: servis kataloğu link alanları
// (runbookUrl, oncallUrl, customLinks[i].url, repository) yazılmadan önce
// SAF kapıdan geçer. v0.10.1315 bu alanları çizimde SafeHref'le süzüyordu;
// yazarken ret, editörün "javascript:" ya da "//evil" saklayıp sessizce
// çizilmeyen bir link bırakmasını önler ve hatayı alan adıyla söyler.
//
// Katalog OKUMALARI bilinçli olarak süzülmez: AdminCatalog eski kötü
// değeri "blocked" diye gösterir ki admin düzeltebilsin (v0.10.1315).
//
// api.go BÜYÜMEZ (TestApiGoDoesNotGrow): putServiceMetadata'daki
// Decode + http.Error satırları decodeServiceMetadata + writeJSONError ile
// 1:1 değiştirildi.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/safelink"
)

const linkFieldRule = `must be an absolute http(s) URL or an app path such as "/runbook?id=…"`

// decodeServiceMetadata — PUT gövdesini çözer ve link alanlarını doğrular.
// Hata metni doğrudan 400 {error} olarak yazılır.
func decodeServiceMetadata(r *http.Request, m *chstore.ServiceMetadata) error {
	if err := json.NewDecoder(r.Body).Decode(m); err != nil {
		return fmt.Errorf("invalid body: %w", err)
	}
	return validateServiceMetadataLinks(m)
}

// validateServiceMetadataLinks — SAF: link alanlarını kırpar (yerinde) ve
// doğrular; boş alan geçerli. Hata alanı adlandırır ("runbookUrl: …",
// "customLinks[2].url: …").
func validateServiceMetadataLinks(m *chstore.ServiceMetadata) error {
	check := func(field string, v *string) error {
		*v = strings.TrimSpace(*v)
		if *v == "" {
			return nil
		}
		if _, ok := safelink.SafeLink(*v); !ok {
			return fmt.Errorf("%s: %s", field, linkFieldRule)
		}
		return nil
	}
	if err := check("runbookUrl", &m.RunbookURL); err != nil {
		return err
	}
	if err := check("oncallUrl", &m.OncallURL); err != nil {
		return err
	}
	for i := range m.CustomLinks {
		if err := check(fmt.Sprintf("customLinks[%d].url", i), &m.CustomLinks[i].URL); err != nil {
			return err
		}
	}
	m.Repository = strings.TrimSpace(m.Repository)
	if m.Repository != "" && !isSafeRepository(m.Repository) {
		return fmt.Errorf(`repository: must be an http(s) URL, an ssh:// URL or a git remote such as "git@host:org/repo.git"`)
	}
	return nil
}

// scpRemote — git'in scp benzeri uzak biçimi: user@host:path. Yol "/" ile
// başlamaz (":/" ve "://" biçimlerini ssh:// dalına bırakır); boşluk,
// kontrol karakteri, ters bölü, ":" yok — "javascript:" eşleşemez (user
// kısmı ":" taşıyamaz).
var scpRemote = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?:[A-Za-z0-9._~-][A-Za-z0-9._~/-]*$`)

// isSafeRepository — SAF: safelink (http(s) / uygulama yolu) YA DA git uzak
// biçimleri (ssh://[user@]host/path, user@host:org/repo(.git)).
func isSafeRepository(u string) bool {
	if _, ok := safelink.SafeLink(u); ok {
		return true
	}
	if len(u) > safelink.MaxLen || !utf8.ValidString(u) || strings.ContainsAny(u, "\\") {
		return false
	}
	if strings.IndexFunc(u, func(r rune) bool {
		return r < 0x20 || (r >= 0x7f && r <= 0x9f) || unicode.IsSpace(r) || r == 0xFEFF
	}) >= 0 {
		return false
	}
	if strings.HasPrefix(strings.ToLower(u), "ssh://") {
		pu, err := url.Parse(u)
		if err != nil || pu.Host == "" || pu.Hostname() == "" {
			return false
		}
		if pu.User != nil {
			if _, hasPw := pu.User.Password(); hasPw {
				return false
			}
		}
		return true
	}
	return scpRemote.MatchString(u)
}
