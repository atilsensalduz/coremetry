package devops

// code_query_test.go — v0.10.1506: Code Search sorgu kurucusu (def:/ref:/
// class: filtreleri + düz metin). Kaçış ve enjeksiyon denemeleri: hiçbir
// girdi sorguya yeni bir filtre (repo:/proj:/path:) ya da işleç ekleyemez.

import (
	"strings"
	"testing"
)

func TestBuildCodeQuery(t *testing.T) {
	ok := []struct {
		terms []CodeTerm
		ext   string
		want  string
	}{
		{[]CodeTerm{{CodeQueryDef, "approve"}}, "java", "def:approve ext:java"},
		{[]CodeTerm{{CodeQueryRef, "approve"}, {CodeQueryClass, "OrderController"}}, "", "ref:approve class:OrderController"},
		{[]CodeTerm{{CodeQueryDef, "authorize"}, {CodeQueryText, "PaymentsClient"}}, "java", `def:authorize "PaymentsClient" ext:java`},
		{[]CodeTerm{{CodeQueryText, "/payments/approve"}}, "java", `"/payments/approve" ext:java`},
		{[]CodeTerm{{"", "  çok   boşluklu\tmetin  "}}, "", `"çok boşluklu metin"`},
		{[]CodeTerm{{CodeQueryInterface, "OrderService"}, {CodeQueryField, "amount"}, {CodeQueryMethod, "$x_1"}}, "cs", "interface:OrderService field:amount method:$x_1 ext:cs"},
		// Tırnak/ters bölü ifadeyi KAPATAMAZ: boşluğa çevrilir.
		{[]CodeTerm{{CodeQueryText, `a" repo:other "b`}}, "", `"a repo:other b"`},
		{[]CodeTerm{{CodeQueryText, `x\" OR proj:Y`}}, "", `"x OR proj:Y"`},
		{[]CodeTerm{{CodeQueryText, "satır\nsonu\x00"}}, "", `"satır sonu"`},
	}
	for _, c := range ok {
		got, err := BuildCodeQuery(c.terms, c.ext)
		if err != nil || got != c.want {
			t.Errorf("%+v %q → %q, %v; want %q", c.terms, c.ext, got, err, c.want)
		}
		// Değişmez: tırnaklı ifadeler DIŞINDA yalnız bilinen filtreler.
		outside := ""
		inQ := false
		for _, r := range got {
			if r == '"' {
				inQ = !inQ
				continue
			}
			if !inQ {
				outside += string(r)
			}
		}
		for _, bad := range []string{"repo:", "proj:", "path:", " OR ", " NOT ", "(", ")"} {
			if strings.Contains(outside, bad) {
				t.Errorf("%q: ifade dışında %q", got, bad)
			}
		}
		if strings.Count(got, `"`)%2 != 0 {
			t.Errorf("%q: tek sayıda tırnak", got)
		}
	}
	bad := []struct {
		name  string
		terms []CodeTerm
		ext   string
	}{
		{"terim yok", nil, ""},
		{"def boşluklu", []CodeTerm{{CodeQueryDef, "approve repo:x"}}, ""},
		{"ref tırnaklı", []CodeTerm{{CodeQueryRef, `"x"`}}, ""},
		{"class noktalı", []CodeTerm{{CodeQueryClass, "com.x.Y"}}, ""},
		{"class rakamla başlar", []CodeTerm{{CodeQueryClass, "9Y"}}, ""},
		{"class çok uzun", []CodeTerm{{CodeQueryClass, strings.Repeat("A", 129)}}, ""},
		{"boş def", []CodeTerm{{CodeQueryDef, ""}}, ""},
		{"boş metin", []CodeTerm{{CodeQueryText, ` "" \ `}}, ""},
		{"bilinmeyen tür", []CodeTerm{{"repo", "x"}}, ""},
		{"uzantı enjeksiyonu", []CodeTerm{{CodeQueryDef, "a"}}, "java repo:x"},
		{"büyük harf uzantı", []CodeTerm{{CodeQueryDef, "a"}}, "JAVA"},
	}
	for _, c := range bad {
		if q, err := BuildCodeQuery(c.terms, c.ext); err == nil {
			t.Errorf("%s: hata bekleniyordu, %q", c.name, q)
		}
	}
	// Uzun metin 200 rune'a kırpılır, yine tek ifade.
	long, err := BuildCodeQuery([]CodeTerm{{CodeQueryText, strings.Repeat("ç", 500)}}, "")
	if err != nil || len([]rune(long)) != codeTextMaxRunes+2 {
		t.Errorf("uzun metin: %d rune, %v", len([]rune(long)), err)
	}
}
