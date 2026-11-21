package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreUpsertFindRemove(t *testing.T) {
	t.Setenv("LSY_CONFIG_DIR", filepath.Join(t.TempDir(), "lsy"))

	st, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(st.Documents) != 0 {
		t.Fatalf("expected empty store")
	}

	rec := Record{
		LinearID:     "doc-uuid-1",
		SlugID:       "a1b2c3d4e5f6",
		Slug:         "document-name",
		Title:        "Document Name",
		LinearURL:    "https://linear.app/example-project/document/document-name-a1b2c3d4e5f6",
		LocalPath:    "/tmp/docs/document-name.md",
		GitHubRepo:   "example/blog",
		GitHubBranch: "main",
		RemotePath:   "docs/document-name.md",
		Status:       StatusPublished,
		UpdatedAt:    time.Now().UTC(),
	}
	st.Upsert(rec)
	if err := st.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	queries := []string{
		"https://linear.app/example-project/document/document-name-a1b2c3d4e5f6",
		"https://linear.app/example-project/document/document-name-a1b2c3d4e5f6?tab=details",
		"doc-uuid-1",
		"a1b2c3d4e5f6",
		"document-name",
		"document-name.md",
		"Document Name",
		"docs/document-name.md",
	}
	for _, q := range queries {
		found, idx := reloaded.Find(q)
		if found == nil || idx != 0 {
			t.Errorf("Find(%q) failed: found=%v idx=%d", q, found, idx)
		}
	}

	reloaded.RemoveAt(0)
	if err := reloaded.Save(); err != nil {
		t.Fatalf("Save after remove failed: %v", err)
	}

	after, err := Load()
	if err != nil {
		t.Fatalf("Load after remove failed: %v", err)
	}
	if len(after.Documents) != 0 {
		t.Fatalf("expected 0 documents after removal, got %d", len(after.Documents))
	}
}
