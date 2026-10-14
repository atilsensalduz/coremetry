package api

// problem_notes_test.go — v0.10.1169: "probleme not" aksiyonunun saf parçaları.

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestProblemNoteTokenBindsProblemTextAndCaller(t *testing.T) {
	text := problemNoteText("get_problem", "  {\"error_rate\":  0.12}\n")
	if !strings.HasPrefix(text, "CoSRE bulgusu (get_problem): ") || strings.Contains(text, "\n") {
		t.Fatalf("metin: %q", text)
	}
	c := auth.ActionClaims{Kind: problemNoteTokenKind, ID: problemNoteDigest("p-1", text), Who: "u-1"}
	if !verifyProblemNote(c, "p-1", text, "u-1") {
		t.Fatal("doğru jeton kabul edilmeli")
	}
	for name, ok := range map[string]bool{
		"metin değişti":   verifyProblemNote(c, "p-1", text+" (düzenlendi)", "u-1"),
		"başka problem":   verifyProblemNote(c, "p-2", text, "u-1"),
		"başka kullanıcı": verifyProblemNote(c, "p-1", text, "u-2"),
		"başka tür":       verifyProblemNote(auth.ActionClaims{Kind: "problem", ID: c.ID, Who: "u-1"}, "p-1", text, "u-1"),
	} {
		if ok {
			t.Errorf("%s: reddedilmeli", name)
		}
	}
	if problemNoteText("x", "   ") != "" {
		t.Fatal("boş sonuç not üretmez")
	}
	first, again := problemNoteRowID("tok"), problemNoteRowID("tok")
	if first != again || problemNoteRowID("a") == problemNoteRowID("b") {
		t.Fatal("satır kimliği jetondan kararlı türemeli")
	}
}

func TestProblemNotesFor(t *testing.T) {
	rows := []chstore.SavedView{
		{ID: "a", Page: problemNotePage, Name: "p-1", QueryString: `{"problemId":"p-1","text":"eski","at":1}`},
		{ID: "b", Page: problemNotePage, Name: "p-1", QueryString: `{"problemId":"p-1","text":"yeni","at":2}`},
		{ID: "c", Page: problemNotePage, Name: "p-2", QueryString: `{"problemId":"p-2","text":"x","at":3}`},
		{ID: "d", Page: "traces", Name: "p-1", QueryString: `{"problemId":"p-1","text":"y","at":4}`},
		{ID: "e", Page: problemNotePage, Name: "p-1", QueryString: `{`},
	}
	got := problemNotesFor(rows, "p-1")
	if len(got) != 2 || got[0].Text != "yeni" || got[1].ID != "a" {
		t.Fatalf("got %+v", got)
	}
}

// Aksiyon yalnız tool döngüsünde, başarılı sonuçtan ve sayfa bağlamının
// problem kimliğiyle üretilir (model metni yolu yok).
func TestProblemNoteActionEmittedFromToolResult(t *testing.T) {
	src, _ := os.ReadFile("copilot_chat.go")
	s := string(src)
	if strings.Count(s, "s.problemNoteAction(ctx, pageCtx.ProblemID, tc.Name, tr.Content)") != 1 {
		t.Fatal("probleme not aksiyonu tool döngüsünde tek noktadan, tool sonucundan türemeli")
	}
	if !strings.Contains(s, `if !tr.IsError && !noteOffered && pageCtx != nil`) {
		t.Fatal("yalnız başarılı tool sonucu ve tur başına bir kez")
	}
}
