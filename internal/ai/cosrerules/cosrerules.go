// Package cosrerules — v0.10.1289: CoSRE operatör kuralları ("bunu hatırla").
//
// Operatörün CoSRE'ye bıraktığı kısa, insan yazımı bağlam notları: "checkout'ta
// gece 02:00 batch'i 5xx üretir, alarm değildir", "payments ekibi deploy'u
// Argo'dan değil Jenkins'ten yapar". Kural model çağrısına VERİ olarak, çitli
// bir blokta girer (BlockTR); sistem mesajındaki ek (copilot.CosreRulesAddendum)
// modele bunun bağlam notu olduğunu, araç seçimini / argümanları / güvenlik
// kurallarını değiştiremeyeceğini söyler.
//
// Neden yaprak paket: kuralları hem sohbet (internal/api) hem arka plan AI SRE
// anlatımı (internal/sre) okur; sre api'yi içe aktaramaz (TestSREImports) ve
// chstore yalnız depolar. Paket bağımlılıksızdır (stdlib), üç taraf da içe
// aktarır.
//
// Operatör direktifi (2026-10-17, "oylara çok güvenmeyelim"): kurallar İNSAN
// yazımıdır ve imzalıdır (yazan + tarih görünür); oy / kalite puanı
// (ai_feedback, cosre quality) buradan ASLA okunmaz ve kural ASLA bir
// skora/plana/kontrole girmez (sre TestRulesNeverReachScoring).
package cosrerules

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Tavanlar — tasarım sabitleri (kural sayısı, kapsam başına, metin uzunluğu,
// prompt bütçesi).
const (
	MaxRules           = 200
	MaxPerScope        = 20
	MinTextRunes       = 8
	MaxTextRunes       = 200
	MaxScopeValueRunes = 128
	maxAuthorRunes     = 60

	// PromptMaxRules / PromptMaxBytes — bir model çağrısına giren kural
	// sayısı ve kural satırlarının toplam bayt tavanı.
	PromptMaxRules = 5
	PromptMaxBytes = 600

	FenceOpen  = "<operator_rules>"
	FenceClose = "</operator_rules>"

	// BlockHeader — veri bloğunun başlığı (ek bu adla atıf yapar).
	BlockHeader = "OPERATÖR KURALLARI (veri):"
)

// Kapsam türleri.
const (
	ScopeGlobal  = "global"
	ScopeService = "service"
	ScopeTeam    = "team"
)

// Scope — kuralın geçerli olduğu bağlam.
type Scope struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

// Rule — tek operatör kuralı. Damgalar unix ns.
type Rule struct {
	ID          string `json:"id"`
	Text        string `json:"text"`
	Scope       Scope  `json:"scope"`
	Author      string `json:"author"`
	AuthorEmail string `json:"authorEmail,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt,omitempty"`
	UpdatedBy   string `json:"updatedBy,omitempty"`
}

// Doc — system_settings.cosre_rules blobu. Rev her yazımda bir artar
// (If-Match iyimser eşzamanlılığı).
type Doc struct {
	Rules []Rule `json:"rules"`
	Rev   uint64 `json:"rev"`
}

// Hata türleri (API 400/409/404 eşlemesi).
var (
	ErrTextLength = fmt.Errorf("kural %d–%d karakter olmalı (tek satır)", MinTextRunes, MaxTextRunes)
	ErrScope      = errors.New("geçersiz kapsam: global, service (servis adıyla) ya da team (takım adıyla)")
	ErrCapTotal   = fmt.Errorf("en çok %d kural saklanır — önce eski bir kuralı sil", MaxRules)
	ErrCapScope   = fmt.Errorf("bu kapsamda en çok %d kural olabilir — önce eski bir kuralı sil", MaxPerScope)
	ErrDuplicate  = errors.New("aynı kapsamda aynı kural zaten var")
	ErrNotFound   = errors.New("kural bulunamadı")
)

// fenceRe — çit belirteçleri (harf kasası ve iç boşluk duyarsız).
var fenceRe = regexp.MustCompile(`(?i)<\s*/?\s*operator_rules\s*>`)

// NormalizeText — SAF: tek satır, kontrol/biçim karakteri yok, çit belirteci
// yok, boşluk sıkıştırılmış. Kod çiti (```) de düşer: blok sohbet
// markdown'ında çit açamaz.
func NormalizeText(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = fenceRe.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "```", " ")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// ValidText — SAF: normalize edilmiş metin uzunluk sınırında mı.
func ValidText(t string) bool {
	n := utf8.RuneCountInString(t)
	return n >= MinTextRunes && n <= MaxTextRunes
}

// NormalizeScope — SAF: türü küçük harfe indirir; global'de değer düşer,
// service/team'de değer zorunlu ve ≤128 karakter.
func NormalizeScope(sc Scope) (Scope, error) {
	kind := strings.ToLower(strings.TrimSpace(sc.Kind))
	switch kind {
	case "", ScopeGlobal:
		return Scope{Kind: ScopeGlobal}, nil
	case ScopeService, ScopeTeam:
		v := NormalizeText(sc.Value)
		if v == "" || utf8.RuneCountInString(v) > MaxScopeValueRunes {
			return Scope{}, ErrScope
		}
		return Scope{Kind: kind, Value: v}, nil
	}
	return Scope{}, ErrScope
}

// ScopeKey — kapsam başına tavanın anahtarı (değer harf kasası duyarsız).
func ScopeKey(sc Scope) string {
	return sc.Kind + ":" + strings.ToLower(sc.Value)
}

// ScopeLabelTR — kapsamın operatöre görünen etiketi.
func ScopeLabelTR(sc Scope) string {
	switch sc.Kind {
	case ScopeService:
		return "servis " + sc.Value
	case ScopeTeam:
		return "takım " + sc.Value
	}
	return "genel"
}

// NormalizeDoc — SAF: bozuk / boş kimlikli / geçersiz metinli satırları ve
// yinelenen kimlikleri düşürür, toplam tavanı uygular (saklanan sıra korunur).
// Yükleme yolu (bozuk blob elle düzenlenmiş olabilir).
func NormalizeDoc(d Doc) Doc {
	out := Doc{Rev: d.Rev, Rules: make([]Rule, 0, len(d.Rules))}
	seen := map[string]bool{}
	for _, r := range d.Rules {
		r.ID = strings.TrimSpace(r.ID)
		r.Text = NormalizeText(r.Text)
		sc, err := NormalizeScope(r.Scope)
		if r.ID == "" || seen[r.ID] || err != nil || !ValidText(r.Text) {
			continue
		}
		r.Scope = sc
		r.Author = clipRunes(NormalizeText(r.Author), maxAuthorRunes)
		seen[r.ID] = true
		out.Rules = append(out.Rules, r)
		if len(out.Rules) >= MaxRules {
			break
		}
	}
	return out
}

func countScope(rules []Rule, key, skipID string) int {
	n := 0
	for _, r := range rules {
		if r.ID != skipID && ScopeKey(r.Scope) == key {
			n++
		}
	}
	return n
}

func duplicate(rules []Rule, r Rule, skipID string) bool {
	key := ScopeKey(r.Scope)
	for _, x := range rules {
		if x.ID != skipID && ScopeKey(x.Scope) == key && strings.EqualFold(x.Text, r.Text) {
			return true
		}
	}
	return false
}

// Add — SAF: yeni kural (metin + kapsam normalize edilir, tavanlar denetlenir).
// Dönen belgede Rev bir artar; girdi belge değişmez.
func Add(d Doc, r Rule) (Doc, Rule, error) {
	r.Text = NormalizeText(r.Text)
	if !ValidText(r.Text) {
		return d, Rule{}, ErrTextLength
	}
	sc, err := NormalizeScope(r.Scope)
	if err != nil {
		return d, Rule{}, err
	}
	r.Scope = sc
	r.Author = clipRunes(NormalizeText(r.Author), maxAuthorRunes)
	if len(d.Rules) >= MaxRules {
		return d, Rule{}, ErrCapTotal
	}
	if countScope(d.Rules, ScopeKey(sc), "") >= MaxPerScope {
		return d, Rule{}, ErrCapScope
	}
	if duplicate(d.Rules, r, "") {
		return d, Rule{}, ErrDuplicate
	}
	out := Doc{Rev: d.Rev + 1, Rules: append(append(make([]Rule, 0, len(d.Rules)+1), d.Rules...), r)}
	return out, r, nil
}

// Update — SAF: metin ve kapsamı değiştirir; yazan/oluşturma damgası korunur.
func Update(d Doc, id, text string, sc Scope, by string, now int64) (Doc, Rule, error) {
	idx := -1
	for i, r := range d.Rules {
		if r.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return d, Rule{}, ErrNotFound
	}
	text = NormalizeText(text)
	if !ValidText(text) {
		return d, Rule{}, ErrTextLength
	}
	nsc, err := NormalizeScope(sc)
	if err != nil {
		return d, Rule{}, err
	}
	r := d.Rules[idx]
	if ScopeKey(nsc) != ScopeKey(r.Scope) && countScope(d.Rules, ScopeKey(nsc), id) >= MaxPerScope {
		return d, Rule{}, ErrCapScope
	}
	r.Text, r.Scope, r.UpdatedAt, r.UpdatedBy = text, nsc, now, clipRunes(NormalizeText(by), maxAuthorRunes)
	if duplicate(d.Rules, r, id) {
		return d, Rule{}, ErrDuplicate
	}
	rules := append(make([]Rule, 0, len(d.Rules)), d.Rules...)
	rules[idx] = r
	return Doc{Rev: d.Rev + 1, Rules: rules}, r, nil
}

// Remove — SAF: kuralı siler.
func Remove(d Doc, id string) (Doc, Rule, error) {
	for i, r := range d.Rules {
		if r.ID == id {
			rules := append(append(make([]Rule, 0, len(d.Rules)-1), d.Rules[:i]...), d.Rules[i+1:]...)
			return Doc{Rev: d.Rev + 1, Rules: rules}, r, nil
		}
	}
	return d, Rule{}, ErrNotFound
}

// Context — bir model çağrısının kural bağlamı: konuşulan servis, o servisin
// sahip takımı (katalog ownerTeam) ve soran kullanıcının takımı.
type Context struct {
	Service  string
	Team     string
	UserTeam string
}

// tier — SAF: kuralın bağlama uzaklığı; -1 = ilgisiz. Servis kuralı yalnız
// AYNI servise, takım kuralı servisin ya da kullanıcının takımına girer.
func tier(r Rule, c Context) int {
	switch r.Scope.Kind {
	case ScopeService:
		if c.Service != "" && strings.EqualFold(r.Scope.Value, c.Service) {
			return 0
		}
	case ScopeTeam:
		if (c.Team != "" && strings.EqualFold(r.Scope.Value, c.Team)) ||
			(c.UserTeam != "" && strings.EqualFold(r.Scope.Value, c.UserTeam)) {
			return 1
		}
	case ScopeGlobal:
		return 2
	}
	return -1
}

func newest(r Rule) int64 {
	if r.UpdatedAt > r.CreatedAt {
		return r.UpdatedAt
	}
	return r.CreatedAt
}

// Relevant — SAF: bağlama uyan kurallar, öncelik servis > takım > genel, her
// kademede en yeni önce. En çok maxN kural ve satırları toplamı maxBytes'ı
// aşmadan; sığmayan ilgili kuralların sayısı omitted. maxN/maxBytes ≤ 0 →
// tasarım varsayılanı (5 / 600 B).
func Relevant(rules []Rule, c Context, maxN, maxBytes int) (picked []Rule, omitted int) {
	if maxN <= 0 {
		maxN = PromptMaxRules
	}
	if maxBytes <= 0 {
		maxBytes = PromptMaxBytes
	}
	type cand struct {
		r Rule
		t int
	}
	var cs []cand
	for _, r := range rules {
		if t := tier(r, c); t >= 0 {
			cs = append(cs, cand{r, t})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].t != cs[j].t {
			return cs[i].t < cs[j].t
		}
		if a, b := newest(cs[i].r), newest(cs[j].r); a != b {
			return a > b
		}
		return cs[i].r.ID < cs[j].r.ID
	})
	used := 0
	for _, x := range cs {
		n := len(Line(x.r, nil)) + 1 // satır + "\n"
		if len(picked) >= maxN || used+n > maxBytes {
			omitted++
			continue
		}
		used += n
		picked = append(picked, x.r)
	}
	return picked, omitted
}

// Line — SAF: kuralın blok satırı "- [kapsam] metin (yazan, tarih)". Tarih
// loc diliminde (nil → UTC); yazan/kapsam değeri de çit belirteçsiz.
func Line(r Rule, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	who := clipRunes(NormalizeText(r.Author), maxAuthorRunes)
	if who == "" {
		who = clipRunes(NormalizeText(r.AuthorEmail), maxAuthorRunes)
	}
	if who == "" {
		who = "?"
	}
	day := "?"
	if ts := newest(r); ts > 0 {
		day = time.Unix(0, ts).In(loc).Format("2006-01-02")
	}
	sc := r.Scope
	sc.Value = NormalizeText(sc.Value)
	return fmt.Sprintf("- [%s] %s (%s, %s)", ScopeLabelTR(sc), NormalizeText(r.Text), who, day)
}

// BlockTR — SAF: çitli veri bloğu; kural yoksa "". Kural metinleri
// NormalizeText'ten geçtiği için blok içinden çit kapatılamaz.
func BlockTR(picked []Rule, omitted int, loc *time.Location) string {
	if len(picked) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(BlockHeader + "\n" + FenceOpen + "\n")
	for _, r := range picked {
		b.WriteString(Line(r, loc) + "\n")
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "(+%d kural daha sığmadı)\n", omitted)
	}
	b.WriteString(FenceClose)
	return b.String()
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
