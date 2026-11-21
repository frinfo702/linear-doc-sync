package markdown

import (
	"strings"
	"testing"
	"time"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"API Design Document":      "api-design-document",
		"  Hello, World! (v2.0)  ": "hello-world-v2-0",
		"doc_with_underscores":     "doc-with-underscores",
		"---":                      "document",
		"":                         "document",
		"日本語ドキュメント":                "日本語ドキュメント",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRender(t *testing.T) {
	ts := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	meta := Metadata{
		Title:     `System "Architecture"`,
		LinearID:  "uuid-123",
		SlugID:    "abc123def456",
		Slug:      "system-architecture",
		LinearURL: "https://linear.app/org/document/system-architecture-abc123def456",
		UpdatedAt: ts,
	}

	withFM := Render(meta, "# Heading\n\nBody text", true)
	if !strings.HasPrefix(withFM, "---\n") {
		t.Fatalf("expected frontmatter header, got:\n%s", withFM)
	}
	if !strings.Contains(withFM, `title: "System \"Architecture\""`) {
		t.Fatalf("expected escaped title in frontmatter, got:\n%s", withFM)
	}
	if !strings.Contains(withFM, "# Heading\n\nBody text\n") {
		t.Fatalf("expected body content in output, got:\n%s", withFM)
	}

	withoutFM := Render(meta, "# Heading\n\nBody text", false)
	if strings.HasPrefix(withoutFM, "---\n") {
		t.Fatalf("did not expect frontmatter, got:\n%s", withoutFM)
	}
	if withoutFM != "# Heading\n\nBody text\n" {
		t.Fatalf("unexpected plain output: %q", withoutFM)
	}
}
