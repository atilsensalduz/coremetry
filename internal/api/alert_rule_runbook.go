package api

// alert_rule_runbook.go — v0.10.1341: alert kuralının runbookUrl alanı
// yazılmadan önce SAF kapıdan geçer (internal/safelink). Kural runbook'u
// problem çekmecesinde / inbox rozetinde / CoSRE önerisinde href olur;
// v0.10.1315 çizimde süzüyordu, artık yazarken de reddediyoruz ki editör
// "javascript:" saklayıp sessizce kaybolan bir link bırakmasın.
//
// api.go BÜYÜMEZ (TestApiGoDoesNotGrow): create/update'teki mevcut
// `acceptRuleTarget || acceptRuleNotify` ifadesi aynı satırda uzatıldı.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/safelink"
)

var errRuleRunbook = errors.New(`runbookUrl must be an absolute http(s) URL or an app path such as "/runbook?id=…"`)

// validateRuleRunbook — SAF: kırpılmış runbook URL'si; boş geçerli.
func validateRuleRunbook(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	l, ok := safelink.SafeLink(raw)
	if !ok {
		return "", errRuleRunbook
	}
	return l, nil
}

// acceptRuleRunbook — 400 + JSON {error}; geçerliyse kırpılmış değeri
// kurala geri yazar (acceptRuleNotify ile aynı normalize-et-ve-yaz biçimi).
func (s *Server) acceptRuleRunbook(w http.ResponseWriter, rule *chstore.AlertRule) bool {
	u, err := validateRuleRunbook(rule.RunbookURL)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return false
	}
	rule.RunbookURL = u
	return true
}
