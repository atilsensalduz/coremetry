package chstore

// rag_tombstone_test.go — v0.10.1172 (operatör-bildirimi: belge silerken
// hata): rag_chunks silme / kuyruk temizliği mutasyon değil mezar taşı;
// her okuma mezar taşını eler.

import (
	"os"
	"strings"
	"testing"
)

func TestRagChunksNoMutationAndReadsSkipTombstones(t *testing.T) {
	b, err := os.ReadFile("rag.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, `mutationTarget("rag_chunks")`) {
		t.Fatal("rag_chunks için ALTER … DELETE mutasyonu geri gelmiş — mezar taşı kullan")
	}
	reads := strings.Count(src, "FROM rag_chunks FINAL")
	filtered := strings.Count(src, "doc_name != ''")
	if reads == 0 || filtered < reads {
		t.Fatalf("%d okuma, %d mezar taşı süzgeci — her okuma doc_name != '' ile elemeli", reads, filtered)
	}
}
