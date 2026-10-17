package api

// cosre_rules_prompt.go — v0.10.1289 (CoSRE kurallar / hafıza): operatör
// kurallarının model çağrısına girdiği TEK yer.
//
// Bağlama uyan kurallar (servis > takım > genel, en yeni önce, ≤5 kural /
// ≤600 B; internal/ai/cosrerules Relevant) çitli "OPERATÖR KURALLARI (veri)"
// bloğu olarak YALNIZ iki sohbet yüzeyine girer:
//
//   - serbest araç döngüsü: sistem mesajında sohbet bağlamı önsözünün
//     (chatContextPreambleTR) arkası, blok + copilot.CosreRulesAddendum;
//   - guided anlatım: blok kullanıcı mesajının VERİ bölümüne, ek sistem
//     mesajının önüne.
//
// Router, niyet sınıflandırıcısı, araç argümanları ve tek-atış ✨ explain
// yüzeyleri kuralları GÖRMEZ (cosre_rules_test.go TestRulesPromptSitesPinned).
// Kural yoksa iki prompt da bayt bayt eski. Arka plan AI SRE anlatımı aynı
// saf çekirdeği internal/sre/narrate.go'da kendi kullanır.

import (
	"context"
	"strconv"
	"strings"

	"github.com/cilcenk/coremetry/internal/ai/cosrerules"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

// cosreRulesSource — test dikişi (üretimde süreç-içi son yüklenen belge).
var cosreRulesSource = func() []cosrerules.Rule { return chstore.CurrentCosreRules().Rules }

// serviceOwnerTeam — katalog ownerTeam'i (ListServiceMetadata süreç-içi 30 s
// önbellekli); okunamazsa "".
func (s *Server) serviceOwnerTeam(ctx context.Context, service string) string {
	if s.store == nil || strings.TrimSpace(service) == "" {
		return ""
	}
	mds, err := s.store.ListServiceMetadata(ctx)
	if err != nil {
		return ""
	}
	if md, ok := mds[service]; ok {
		return md.OwnerTeam
	}
	for name, md := range mds {
		if strings.EqualFold(name, service) {
			return md.OwnerTeam
		}
	}
	return ""
}

// cosreRuleContext — kuralların bağlamı. Takım okumaları (katalog + kullanıcı
// satırı) YALNIZ takım kapsamlı kural varken yapılır: kuralsız ya da yalnız
// servis/genel kurallı kurulumda sohbet turuna ek okuma binmez.
func (s *Server) cosreRuleContext(ctx context.Context, service string, rules []cosrerules.Rule) cosrerules.Context {
	c := cosrerules.Context{Service: strings.TrimSpace(service)}
	hasTeam := false
	for _, r := range rules {
		if r.Scope.Kind == cosrerules.ScopeTeam {
			hasTeam = true
			break
		}
	}
	if !hasTeam {
		return c
	}
	c.Team = s.serviceOwnerTeam(ctx, c.Service)
	if u := s.userByID(ctx, copilot.MetaFromContext(ctx).UserID); u != nil {
		c.UserTeam = strings.TrimSpace(u.Team)
	}
	return c
}

// cosreRulesBlock — bağlama uyan kuralların çitli bloğu ve kural sayısı;
// kural yoksa ("", 0).
func (s *Server) cosreRulesBlock(ctx context.Context, service string) (string, int) {
	rules := cosreRulesSource()
	if len(rules) == 0 {
		return "", 0
	}
	picked, omitted := cosrerules.Relevant(rules, s.cosreRuleContext(ctx, service, rules), cosrerules.PromptMaxRules, cosrerules.PromptMaxBytes)
	return cosrerules.BlockTR(picked, omitted, tzdefault.Location()), len(picked)
}

// cosreRulesLoopPromptTR — SAF: serbest döngü sistem mesajı parçası (blok +
// ek, önsöz biçiminde "\n\n" ile biter); blok yoksa "".
func cosreRulesLoopPromptTR(block string) string {
	if block == "" {
		return ""
	}
	return block + "\n" + copilot.CosreRulesAddendum() + "\n\n"
}

// cosreRulesSystemPrefix — SAF: guided anlatımın sistem mesajı öneki; blok yoksa "".
func cosreRulesSystemPrefix(block string) string {
	if block == "" {
		return ""
	}
	return copilot.CosreRulesAddendum() + "\n\n"
}

// cosreRulesDataSuffix — SAF: guided VERİ bölümünün eki; blok yoksa "".
func cosreRulesDataSuffix(block string) string {
	if block == "" {
		return ""
	}
	return "\n\n" + block
}

// cosreRulesStepLabel — SAF: kural kullanıldığında görünen çip (operatör
// cevabın hangi notlarla yorumlandığını görür).
func cosreRulesStepLabel(n int) string {
	if n <= 0 {
		return ""
	}
	return "operatör kuralları: " + strconv.Itoa(n)
}
