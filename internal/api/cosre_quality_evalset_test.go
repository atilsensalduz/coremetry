package api

// CoSRE öğrenme Faz 4 — en kötü cevap → evalset vakası; keyFacts sınaması.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestQualityEvalsetCase(t *testing.T) {
	w := chstore.CosreWorstAnswer{CosreAnswerQuality: chstore.CosreAnswerQuality{ExchangeID: "e1", Score: 0.05, Reasons: []string{"no_evidence", "rephrase"}, Tier: "loop"}}
	inter := chstore.CosreInteraction{Question: "MCA-00029 neden?", Answer: "kanıt yok",
		Tools: []chstore.CosreTool{{Name: "find_exceptions", OK: true}, {Name: "find_exceptions", OK: true}, {Name: "query_logs", OK: false}}}
	g := &chstore.CosreGrounding{Details: `[{"claim":"a","label":"grounded","fact":"MCA-00029"},{"claim":"b","label":"ungrounded","fact":"v2.3"},{"claim":"c","label":"no_evidence","fact":"x"}]`}
	c, ok := qualityEvalsetCase(w, inter, g)
	if !ok || c.ID != "q-e1" || c.Prompt != "MCA-00029 neden?" {
		t.Fatalf("%+v", c)
	}
	tools := c.Expect["expectedTools"].([]string)
	facts := c.Expect["keyFacts"].([]string)
	forbidden := c.Expect["mustNotContain"].([]string)
	if len(tools) != 1 || tools[0] != "find_exceptions" || len(facts) != 1 || facts[0] != "MCA-00029" || forbidden[0] != "v2.3" {
		t.Errorf("beklentiler: tools=%v facts=%v forbidden=%v", tools, facts, forbidden)
	}
	if _, ok := qualityEvalsetCase(chstore.CosreWorstAnswer{}, chstore.CosreInteraction{}, nil); ok {
		t.Error("sorusuz vaka atlanmalı")
	}
}

func TestScoreEvalCaseKeyFacts(t *testing.T) {
	c := evalCase{Expect: evalExpect{KeyFacts: []string{"MCA-00029", "p99"}}}
	fails, _ := scoreEvalCase(c, "", "Hata mca-00029 kaynaklı", nil)
	if len(fails) != 1 || fails[0] != "missing fact: p99" {
		t.Errorf("%v", fails)
	}
}
