package wiki

// localdocs_test.go — v0.10.1163: tek indeks. Yüklenen belge wiki motorundan
// aranır; izin listeleri ve senkron süpürgesi ona dokunmaz; canlı modda da
// bulunur ve okunur.

import (
	"context"
	"testing"
)

const localDocBody = "# Kasa kapanış\n\nGün sonu kasa kapanış mutabakatı adımları: önce vezne sayımı yapılır.\n"

func TestLocalDocPureHelpers(t *testing.T) {
	if !IsLocalProject("  yüklenen belgeler ") || IsLocalProject("Platform") {
		t.Fatal("IsLocalProject")
	}
	for in, want := range map[string]string{"kasa.pdf": "/kasa.pdf", "runbook.md": "/runbook", "a/b.txt": "/a_b.txt", " ": ""} {
		if got := LocalDocPath(in); got != want {
			t.Errorf("LocalDocPath(%q) = %q, beklenen %q", in, got, want)
		}
	}
	if localDocTitle("kasa.PDF") != "kasa" || localDocTitle("notlar") != "notlar" {
		t.Fatal("localDocTitle")
	}
	// İzin listesi dar olsa da yüklenen belgeler kapsamda.
	c := Config{Projects: []string{"Platform"}, Wikis: []string{"Platform/Runbook"}}
	if !c.ProjectAllowed(LocalProject) || !c.WikiAllowed(LocalProject, LocalWikiName) || c.WikiAllowed("Diger", "X") {
		t.Fatal("izin listesi ayrıcalığı")
	}
}

func TestLocalDocSearchSyncAndLive(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, runbookWiki())
	s.Configure(Config{Enabled: true, Mode: ModeSync})
	if err := s.IndexLocalDoc(ctx, "kasa.pdf", localDocBody, ""); err != nil {
		t.Fatalf("index: %v", err)
	}
	found := func(tag string, o SearchOptions) {
		t.Helper()
		res, err := s.SearchWith(ctx, "kasa kapanış mutabakatı", "", o)
		if err != nil {
			t.Fatalf("%s: arama: %v", tag, err)
		}
		for _, h := range res.Hits {
			if h.WikiID == LocalWikiID && h.Path == "/kasa.pdf" {
				return
			}
		}
		t.Fatalf("%s: yüklenen belge isabetlerde yok: %+v", tag, res.Hits)
	}
	found("sync modu", SearchOptions{Limit: 5, NoSemantic: true})

	// Senkron: DevOps listesinde olmayan kaynak süpürülür — yüklenen belgeler HARİÇ.
	if _, err := s.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}
	found("senkron sonrası", SearchOptions{Limit: 5, NoSemantic: true})

	// Canlı mod: yerel indeks okunmaz ama yüklenen belge bulunur ve okunur.
	s.Configure(Config{Enabled: true, Mode: ModeLive})
	found("canlı mod", SearchOptions{Limit: 5, NoSemantic: true})
	rec, err := s.ReadPage(ctx, LocalProject, LocalWikiName, "/kasa.pdf")
	if err != nil || rec == nil || rec.Content != localDocBody {
		t.Fatalf("canlı modda ReadPage: rec=%v err=%v", rec, err)
	}

	// Aynı içerik yeniden yazılmaz; silme indeksten kaldırır.
	if err := s.IndexLocalDoc(ctx, "kasa.pdf", localDocBody, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteLocalDoc(ctx, "kasa.pdf"); err != nil {
		t.Fatal(err)
	}
	if paths, _ := s.LocalDocPaths(ctx); paths["/kasa.pdf"] {
		t.Fatal("silinen belge indekste kaldı")
	}
}
