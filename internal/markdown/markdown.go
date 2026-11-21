package markdown

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Metadata holds document metadata for optional YAML frontmatter rendering.
type Metadata struct {
	Title     string
	LinearID  string
	SlugID    string
	Slug      string
	LinearURL string
	UpdatedAt time.Time
}

// Slugify converts a document title or identifier into a clean URL/filename-safe slug.
func Slugify(input string) string {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return "document"
	}

	var b strings.Builder
	prevDash := false
	for _, r := range input {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == ' ' || r == '/' || r == '.':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "document"
	}
	return slug
}

// Render formats a Linear document's Markdown content with optional YAML frontmatter.
func Render(meta Metadata, content string, withFrontmatter bool) string {
	body := strings.TrimLeft(content, "\n")
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if !withFrontmatter {
		return body
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("title: %s\n", yamlQuote(meta.Title)))
	if meta.LinearID != "" {
		b.WriteString(fmt.Sprintf("linear_id: %s\n", yamlQuote(meta.LinearID)))
	}
	if meta.SlugID != "" {
		b.WriteString(fmt.Sprintf("slug_id: %s\n", yamlQuote(meta.SlugID)))
	}
	if meta.Slug != "" {
		b.WriteString(fmt.Sprintf("slug: %s\n", yamlQuote(meta.Slug)))
	}
	if meta.LinearURL != "" {
		b.WriteString(fmt.Sprintf("linear_url: %s\n", yamlQuote(meta.LinearURL)))
	}
	if !meta.UpdatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("updated_at: %s\n", yamlQuote(meta.UpdatedAt.UTC().Format(time.RFC3339))))
	}
	b.WriteString("---\n\n")
	b.WriteString(body)
	return b.String()
}

func yamlQuote(s string) string {
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	return `"` + escaped + `"`
}
