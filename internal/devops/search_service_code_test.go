package devops

// v0.10.1208 — search_code yanıt ayrıştırma: yalnız istenen depo, TFVC yok,
// satır/parça varsa alınır, parça tek satır ve kırpık, tavan.

import "testing"

func TestParseServiceCodeHits(t *testing.T) {
	raw := []byte(`{"results":[
	 {"path":"/src/Pin.java","repository":{"name":"bsa-atm","type":"git"},"versions":[{"branchName":"refs/heads/release"}],
	  "matches":{"content":[{"charOffset":10,"line":42,"codeSnippet":"throw new BSAException(\n\"MCA-00029\")"}]}},
	 {"path":"/x/Other.java","repository":{"name":"other-repo","type":"git"}},
	 {"path":"/$/tfvc/A.cs","repository":{"name":"bsa-atm","type":"tfvc"}},
	 {"path":"/src/NoLine.java","repository":{"name":"BSA-ATM","type":"git"},"matches":{"content":[{"charOffset":5}]}}
	]}`)
	hits, err := parseServiceCodeHits(raw, "bsa-atm", 5)
	if err != nil || len(hits) != 2 {
		t.Fatalf("%v %+v", err, hits)
	}
	if h := hits[0]; h.Path != "src/Pin.java" || h.Branch != "release" || h.Line != 42 || h.Snippet != `throw new BSAException( "MCA-00029")` {
		t.Errorf("%+v", h)
	}
	if h := hits[1]; h.Line != 0 || h.Snippet != "" {
		t.Errorf("satır yoksa boş: %+v", h)
	}
	if one, _ := parseServiceCodeHits(raw, "bsa-atm", 1); len(one) != 1 {
		t.Error("tavan")
	}
	if _, err := parseServiceCodeHits([]byte("x"), "r", 5); err == nil {
		t.Error("bozuk yanıt hata")
	}
}
