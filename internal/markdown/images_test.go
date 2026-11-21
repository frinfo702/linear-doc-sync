package markdown

import (
	"strings"
	"testing"
)

func TestRewriteImages(t *testing.T) {
	input := strings.Join([]string{
		"# Title",
		"",
		"Normal: ![alt text](https://uploads.linear.app/org/file1/photo.png)",
		"With title: ![cap](https://uploads.linear.app/org/file2/pic.jpg \"My Title\")",
		"Angle URL: ![angle](<https://uploads.linear.app/org/file3/my image.png>)",
		"Nested alt: ![img [1]](https://uploads.linear.app/org/file4/a(1).png)",
		"Linked: [![badge](https://uploads.linear.app/org/file5/b.png)](https://example.com)",
		"HTML double: <img alt=\"demo\" src=\"https://uploads.linear.app/org/file6/h.webp\" width=\"400\" />",
		"HTML single: <IMG width='200' src='https://uploads.linear.app/org/file7/s.gif'>",
		"Local untouched: ![local](./images/existing.png)",
		"Data URI untouched: ![data](data:image/png;base64,AAAA)",
		"Inline code untouched: `![code](https://uploads.linear.app/org/inline.png)`",
		"```markdown",
		"![fenced](https://uploads.linear.app/org/fenced.png)",
		"<img src=\"https://uploads.linear.app/org/fenced-html.png\">",
		"```",
		"~~~",
		"![tilde](https://uploads.linear.app/org/tilde.png)",
		"~~~",
		"After fence: ![](https://uploads.linear.app/org/file8/after.png)",
	}, "\n")

	var seen []string
	out, err := RewriteImages(input, func(rawURL string) (string, bool, error) {
		seen = append(seen, rawURL)
		return "./images/doc/" + ImageFilename(rawURL, "", []byte(rawURL)), true, nil
	})
	if err != nil {
		t.Fatalf("RewriteImages failed: %v", err)
	}

	if len(seen) != 8 {
		t.Fatalf("expected 8 remote images rewritten, got %d: %v", len(seen), seen)
	}

	// Verify fenced/inline/local/data untouched.
	for _, preserved := range []string{
		"![local](./images/existing.png)",
		"![data](data:image/png;base64,AAAA)",
		"`![code](https://uploads.linear.app/org/inline.png)`",
		"![fenced](https://uploads.linear.app/org/fenced.png)",
		`<img src="https://uploads.linear.app/org/fenced-html.png">`,
		"![tilde](https://uploads.linear.app/org/tilde.png)",
		`"My Title"`,
		"width=\"400\"",
		"width='200'",
		"](https://example.com)",
	} {
		if !strings.Contains(out, preserved) {
			t.Errorf("expected output to preserve %q, got:\n%s", preserved, out)
		}
	}

	// Verify none of the 8 rewritten URLs remain in output.
	for _, u := range seen {
		if strings.Contains(out, u) {
			t.Errorf("expected URL %q to be replaced, still found in:\n%s", u, out)
		}
	}
}

func TestImageFilename(t *testing.T) {
	pngBytes := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	svgBytes := []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)

	// 1. Extension from URL path
	f1 := ImageFilename("https://uploads.linear.app/org/uuid/My%20Screenshot.png?signature=abc", "application/octet-stream", pngBytes)
	if !strings.HasPrefix(f1, "my-screenshot-") || !strings.HasSuffix(f1, ".png") {
		t.Errorf("unexpected filename for URL with ext: %q", f1)
	}

	// 2. Extension from Content-Type when URL has no extension
	f2 := ImageFilename("https://uploads.linear.app/org/uuid-no-ext", "image/webp; charset=binary", []byte("webp-data"))
	if !strings.HasPrefix(f2, "uuid-no-ext-") || !strings.HasSuffix(f2, ".webp") {
		t.Errorf("unexpected filename from content-type: %q", f2)
	}

	// 3. Extension sniffed from PNG bytes when URL and Content-Type lack ext
	f3 := ImageFilename("https://uploads.linear.app/org/raw", "", pngBytes)
	if !strings.HasSuffix(f3, ".png") {
		t.Errorf("expected .png from sniffing, got %q", f3)
	}

	// 4. SVG sniffing
	f4 := ImageFilename("https://uploads.linear.app/org/diagram", "text/plain", svgBytes)
	if !strings.HasSuffix(f4, ".svg") {
		t.Errorf("expected .svg from sniffing, got %q", f4)
	}

	// 5. Same URL name with different bytes yields distinct filenames
	a := ImageFilename("https://uploads.linear.app/org/1/image.png", "image/png", []byte("img-a"))
	b := ImageFilename("https://uploads.linear.app/org/2/image.png", "image/png", []byte("img-b"))
	if a == b {
		t.Errorf("expected distinct filenames for different image bytes, both got %q", a)
	}
}
