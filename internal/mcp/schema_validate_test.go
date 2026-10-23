package mcp

// schema_validate_test.go — v0.10.1456 (C1): yerli JSON-Schema alt kümesi
// doğrulayıcısının anahtar başına tablo testi. 74 aracın kendi şemalarıyla
// tablo testi mcptools/schema_validate_catalog_test.go'da (mcp → mcptools
// içe aktaramaz).

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func testSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"service": map[string]any{"type": "string", "maxLength": 10},
			"range_s": map[string]any{"type": "integer", "minimum": 60, "maximum": 86400},
			"ratio":   map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"status":  map[string]any{"type": "string", "enum": []string{"error", "ok"}},
			"deep":    map[string]any{"type": "boolean"},
			"code":    map[string]any{"type": "string", "pattern": "^[A-Z]{3}$", "minLength": 3},
			"names": map[string]any{"type": "array", "maxItems": 2, "minItems": 1,
				"items": map[string]any{"type": "string"}},
			"labels": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"strict": map[string]any{"type": "object", "additionalProperties": false,
				"properties": map[string]any{"a": map[string]any{"type": "integer"}}},
			"future": map[string]any{"type": "string", "format": "date-time", "oneOf": []any{}, "x-unknown": 1},
		},
		"required": []string{"service"},
	}
}

func TestValidateArgsTable(t *testing.T) {
	type want struct{ path, rule string }
	cases := []struct {
		name string
		raw  string
		want []want
	}{
		{"valid minimal", `{"service":"orders"}`, nil},
		{"valid full", `{"service":"orders","range_s":3600,"ratio":0.5,"status":"error","deep":true,"code":"ABC","names":["a"],"labels":{"env":"prod"},"strict":{"a":1},"future":"x"}`, nil},
		{"required missing", `{}`, []want{{"service", "required"}}},
		{"empty body = {}", ``, []want{{"service", "required"}}},
		{"null body = {}", `null`, []want{{"service", "required"}}},
		{"required null", `{"service":null}`, []want{{"service", "required"}}},
		{"optional null ignored", `{"service":"a","range_s":null}`, nil},
		{"integer as string", `{"service":"a","range_s":"3600"}`, []want{{"range_s", "type"}}},
		{"integer as float integral ok", `{"service":"a","range_s":3600.0}`, nil},
		{"integer fractional", `{"service":"a","range_s":3600.5}`, []want{{"range_s", "type"}}},
		{"number accepts int", `{"service":"a","ratio":1}`, nil},
		{"minimum", `{"service":"a","range_s":10}`, []want{{"range_s", "minimum"}}},
		{"maximum", `{"service":"a","range_s":999999}`, []want{{"range_s", "maximum"}}},
		{"number maximum", `{"service":"a","ratio":1.5}`, []want{{"ratio", "maximum"}}},
		{"enum case-insensitive", `{"service":"a","status":"ERROR"}`, nil},
		{"enum miss", `{"service":"a","status":"failed"}`, []want{{"status", "enum"}}},
		{"boolean as string", `{"service":"a","deep":"true"}`, []want{{"deep", "type"}}},
		{"string as number", `{"service":7}`, []want{{"service", "type"}}},
		{"maxLength runes", `{"service":"çççççççççççç"}`, []want{{"service", "maxLength"}}},
		{"maxLength ok runes", `{"service":"ççççççççç"}`, nil},
		{"pattern", `{"service":"a","code":"abc"}`, []want{{"code", "pattern"}}},
		{"minLength", `{"service":"a","code":"AB"}`, []want{{"code", "minLength"}, {"code", "pattern"}}},
		{"maxItems", `{"service":"a","names":["a","b","c"]}`, []want{{"names", "maxItems"}}},
		{"minItems", `{"service":"a","names":[]}`, []want{{"names", "minItems"}}},
		{"items type", `{"service":"a","names":["a",2]}`, []want{{"names[1]", "type"}}},
		{"array as string", `{"service":"a","names":"a"}`, []want{{"names", "type"}}},
		{"additionalProperties schema", `{"service":"a","labels":{"env":1}}`, []want{{"labels.env", "type"}}},
		{"additionalProperties false", `{"service":"a","strict":{"a":1,"b":2}}`, []want{{"strict.b", "additionalProperties"}}},
		{"nested type", `{"service":"a","strict":{"a":"1"}}`, []want{{"strict.a", "type"}}},
		{"unknown top-level key allowed", `{"service":"a","extra":1}`, nil},
		{"unknown schema keys ignored", `{"service":"a","future":"anything"}`, nil},
		{"broken json", `{"service":`, []want{{"", "json"}}},
		{"trailing garbage", `{"service":"a"} {}`, []want{{"", "json"}}},
		{"root not object", `[1]`, []want{{"", "type"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ValidateArgs(testSchema(), json.RawMessage(c.raw))
			var gw []want
			for _, is := range got {
				gw = append(gw, want{is.Path, is.Rule})
			}
			if !reflect.DeepEqual(gw, c.want) {
				t.Fatalf("issues = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestValidateArgsEmptySchemaNoop(t *testing.T) {
	if got := ValidateArgs(nil, json.RawMessage(`{"x":1}`)); got != nil {
		t.Fatalf("nil schema → %v", got)
	}
	if got := ValidateArgs(map[string]any{"type": "object"}, json.RawMessage(`{}`)); got != nil {
		t.Fatalf("bare object, {} → %v", got)
	}
}

func TestValidateArgsAnyStyleSchema(t *testing.T) {
	// JSON'dan çözülmüş şema ([]any / float64) Go literal şemayla aynı karar.
	var sch map[string]any
	if err := json.Unmarshal([]byte(`{"type":"object","required":["n"],"properties":{"n":{"type":["integer","string"],"enum":[1,"x"],"maximum":5}}}`), &sch); err != nil {
		t.Fatal(err)
	}
	if got := ValidateArgs(sch, json.RawMessage(`{"n":1}`)); got != nil {
		t.Fatalf("n=1 → %v", got)
	}
	if got := ValidateArgs(sch, json.RawMessage(`{"n":"X"}`)); got != nil {
		t.Fatalf(`n="X" → %v`, got)
	}
	if got := ValidateArgs(sch, json.RawMessage(`{"n":true}`)); len(got) != 1 || got[0].Rule != "type" {
		t.Fatalf("n=true → %v", got)
	}
}

func TestValidateArgsCapsIssues(t *testing.T) {
	props := map[string]any{}
	var req []string
	for i := 0; i < 20; i++ {
		n := fmt.Sprintf("f%02d", i)
		props[n] = map[string]any{"type": "string"}
		req = append(req, n)
	}
	got := ValidateArgs(map[string]any{"type": "object", "properties": props, "required": req}, json.RawMessage(`{}`))
	if len(got) != argIssuesMax {
		t.Fatalf("issues = %d, want cap %d", len(got), argIssuesMax)
	}
}

func TestArgIssuesErrorContract(t *testing.T) {
	if ArgIssuesError("x", nil) != nil {
		t.Fatal("no issues must be nil error")
	}
	issues := ValidateArgs(testSchema(), json.RawMessage(`{"range_s":"3600"}`))
	err := ArgIssuesError("query_traces", issues)
	var ae *ArgIssuesErr
	if !errors.As(err, &ae) {
		t.Fatalf("not ArgIssuesErr: %T", err)
	}
	// Sarılı hata da aynı sözleşmeyle sınıflanır.
	te := ClassifyToolError(fmt.Errorf("scope: %w", err))
	if te.Error != ToolErrBadArgs || !te.Retryable {
		t.Fatalf("class = %+v", te)
	}
	for _, needle := range []string{"service: zorunlu alan eksik", "range_s: tam sayı bekleniyor", "tırnaksız", "BİR kez daha"} {
		if !strings.Contains(te.Hint, needle) {
			t.Errorf("hint %q missing %q", te.Hint, needle)
		}
	}
	if !strings.Contains(te.Detail, "query_traces") {
		t.Errorf("detail = %q", te.Detail)
	}
	// JSON biçimi ToolErrorJSON ile aynı alan sırası.
	js := ToolErrorJSON(err)
	if !strings.HasPrefix(js, `{"error":"bad_args","retryable":true,"hint":`) {
		t.Fatalf("json = %s", js)
	}
	ae.Final = true
	fin := ClassifyToolError(ae)
	if fin.Error != ToolErrBadArgs || fin.Retryable || !strings.Contains(fin.Hint, "BIRAK") {
		t.Fatalf("final = %+v", fin)
	}
}
