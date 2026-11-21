package markdown

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// ContentHash returns the lowercase hex SHA-256 digest of data.
func ContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// RewriteImages scans Markdown content for remote image URLs (both Markdown
// `![alt](url ...)` syntax and HTML `<img ... src="...">` tags) outside fenced
// code blocks and inline code spans. For each `http://` or `https://` image URL,
// rewrite is called; when it returns replace=true, the URL is replaced with newURL.
func RewriteImages(content string, rewrite func(rawURL string) (newURL string, replace bool, err error)) (string, error) {
	if content == "" || rewrite == nil {
		return content, nil
	}

	var out strings.Builder
	out.Grow(len(content))

	lines := strings.SplitAfter(content, "\n")
	inFence := false
	var fenceChar byte
	fenceLen := 0

	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		leadingSpaces := len(line) - len(trimmed)

		if !inFence {
			if leadingSpaces <= 3 && len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
				ch := trimmed[0]
				n := 0
				for n < len(trimmed) && trimmed[n] == ch {
					n++
				}
				if n >= 3 {
					// Backtick info strings cannot contain backticks in CommonMark.
					if ch != '`' || !strings.ContainsRune(trimmed[n:], '`') {
						inFence = true
						fenceChar = ch
						fenceLen = n
						out.WriteString(line)
						continue
					}
				}
			}
		} else {
			if leadingSpaces <= 3 && len(trimmed) >= fenceLen && trimmed[0] == fenceChar {
				n := 0
				for n < len(trimmed) && trimmed[n] == fenceChar {
					n++
				}
				if n >= fenceLen && strings.TrimSpace(trimmed[n:]) == "" {
					inFence = false
					fenceChar = 0
					fenceLen = 0
				}
			}
			out.WriteString(line)
			continue
		}

		rewritten, err := rewriteLineImages(line, rewrite)
		if err != nil {
			return "", err
		}
		out.WriteString(rewritten)
	}

	return out.String(), nil
}

func rewriteLineImages(line string, rewrite func(string) (string, bool, error)) (string, error) {
	var b strings.Builder
	b.Grow(len(line))

	i := 0
	for i < len(line) {
		// Skip inline code spans (`...`).
		if line[i] == '`' {
			tickCount := 0
			for i+tickCount < len(line) && line[i+tickCount] == '`' {
				tickCount++
			}
			delim := line[i : i+tickCount]
			rest := line[i+tickCount:]
			if end := strings.Index(rest, delim); end != -1 {
				endPos := i + tickCount + end + tickCount
				b.WriteString(line[i:endPos])
				i = endPos
				continue
			}
			b.WriteString(delim)
			i += tickCount
			continue
		}

		// Markdown image: ![alt](url "optional title")
		if line[i] == '!' && i+1 < len(line) && line[i+1] == '[' {
			if rewritten, nextIdx, matched, err := parseAndRewriteMDImage(line, i, rewrite); err != nil {
				return "", err
			} else if matched {
				b.WriteString(rewritten)
				i = nextIdx
				continue
			}
		}

		// HTML <img ... src="..." ...>
		if line[i] == '<' && hasFoldPrefix(line[i:], "<img") {
			afterTag := i + 4
			if afterTag < len(line) && isHTMLWhitespace(line[afterTag]) {
				if rewritten, nextIdx, matched, err := parseAndRewriteHTMLImg(line, i, rewrite); err != nil {
					return "", err
				} else if matched {
					b.WriteString(rewritten)
					i = nextIdx
					continue
				}
			}
		}

		b.WriteByte(line[i])
		i++
	}

	return b.String(), nil
}

func parseAndRewriteMDImage(s string, start int, rewrite func(string) (string, bool, error)) (string, int, bool, error) {
	// Find matching ']' for alt text starting at start+1 ('[').
	altEnd := -1
	depth := 0
	for j := start + 1; j < len(s); j++ {
		if s[j] == '\\' && j+1 < len(s) {
			j++
			continue
		}
		if s[j] == '[' {
			depth++
		} else if s[j] == ']' {
			depth--
			if depth == 0 {
				altEnd = j
				break
			}
		}
	}
	if altEnd == -1 || altEnd+1 >= len(s) || s[altEnd+1] != '(' {
		return "", start, false, nil
	}

	// Parse destination inside (...) starting at altEnd+2.
	p := altEnd + 2
	for p < len(s) && (s[p] == ' ' || s[p] == '\t') {
		p++
	}
	if p >= len(s) {
		return "", start, false, nil
	}

	urlStart := p
	urlEnd := -1
	angleWrapped := false
	closeParen := -1

	if s[p] == '<' {
		angleWrapped = true
		urlStart = p + 1
		q := urlStart
		for q < len(s) && s[q] != '>' && s[q] != '\n' {
			if s[q] == '\\' && q+1 < len(s) {
				q += 2
				continue
			}
			q++
		}
		if q >= len(s) || s[q] != '>' {
			return "", start, false, nil
		}
		urlEnd = q
		closeParen = findClosingParen(s, q+1)
	} else {
		parenDepth := 0
		q := p
		for q < len(s) {
			ch := s[q]
			if ch == '\\' && q+1 < len(s) {
				q += 2
				continue
			}
			if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
				break
			}
			if ch == '(' {
				parenDepth++
			} else if ch == ')' {
				if parenDepth == 0 {
					break
				}
				parenDepth--
			}
			q++
		}
		urlEnd = q
		closeParen = findClosingParen(s, q)
	}

	if closeParen == -1 || urlEnd <= urlStart {
		return "", start, false, nil
	}

	rawURL := s[urlStart:urlEnd]
	if !isRemoteHTTPURL(rawURL) {
		return s[start : closeParen+1], closeParen + 1, true, nil
	}

	newURL, replace, err := rewrite(rawURL)
	if err != nil {
		return "", start, false, err
	}
	if !replace {
		return s[start : closeParen+1], closeParen + 1, true, nil
	}

	var out strings.Builder
	if angleWrapped {
		out.WriteString(s[start : urlStart-1]) // up to before '<'
		out.WriteString(newURL)
		out.WriteString(s[urlEnd+1 : closeParen+1]) // after '>'
	} else {
		out.WriteString(s[start:urlStart])
		out.WriteString(newURL)
		out.WriteString(s[urlEnd : closeParen+1])
	}
	return out.String(), closeParen + 1, true, nil
}

// findClosingParen finds the ')' closing a Markdown image destination, skipping
// optional whitespace and an optional quoted or parenthesized title.
func findClosingParen(s string, pos int) int {
	i := pos
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i < len(s) && s[i] == ')' {
		return i
	}
	if i >= len(s) {
		return -1
	}
	// Optional title delimited by ", ', or (...).
	var endDelim byte
	switch s[i] {
	case '"':
		endDelim = '"'
	case '\'':
		endDelim = '\''
	case '(':
		endDelim = ')'
	default:
		return -1
	}
	i++
	for i < len(s) && s[i] != endDelim {
		if s[i] == '\\' && i+1 < len(s) {
			i += 2
			continue
		}
		i++
	}
	if i >= len(s) {
		return -1
	}
	i++ // skip endDelim
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i < len(s) && s[i] == ')' {
		return i
	}
	return -1
}

func parseAndRewriteHTMLImg(s string, start int, rewrite func(string) (string, bool, error)) (string, int, bool, error) {
	// Find closing '>' of the <img ...> tag, respecting quoted attribute values.
	end := -1
	var quote byte
	for j := start + 4; j < len(s); j++ {
		ch := s[j]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			continue
		}
		if ch == '>' {
			end = j
			break
		}
	}
	if end == -1 {
		return "", start, false, nil
	}

	tag := s[start : end+1]
	// Locate src attribute inside tag.
	idx := 4
	for idx < len(tag)-1 {
		for idx < len(tag)-1 && (isHTMLWhitespace(tag[idx]) || tag[idx] == '/') {
			idx++
		}
		if idx >= len(tag)-1 {
			break
		}
		nameStart := idx
		for idx < len(tag)-1 && !isHTMLWhitespace(tag[idx]) && tag[idx] != '=' && tag[idx] != '>' && tag[idx] != '/' {
			idx++
		}
		attrName := strings.ToLower(tag[nameStart:idx])
		for idx < len(tag)-1 && isHTMLWhitespace(tag[idx]) {
			idx++
		}
		if idx >= len(tag)-1 || tag[idx] != '=' {
			continue
		}
		idx++ // skip '='
		for idx < len(tag)-1 && isHTMLWhitespace(tag[idx]) {
			idx++
		}
		if idx >= len(tag)-1 {
			break
		}

		var valStart, valEnd int
		if tag[idx] == '"' || tag[idx] == '\'' {
			q := tag[idx]
			idx++
			valStart = idx
			for idx < len(tag)-1 && tag[idx] != q {
				idx++
			}
			valEnd = idx
			if idx < len(tag)-1 {
				idx++
			}
		} else {
			valStart = idx
			for idx < len(tag)-1 && !isHTMLWhitespace(tag[idx]) && tag[idx] != '>' {
				idx++
			}
			valEnd = idx
		}

		if attrName == "src" {
			rawURL := strings.TrimSpace(tag[valStart:valEnd])
			if !isRemoteHTTPURL(rawURL) {
				return tag, end + 1, true, nil
			}
			newURL, replace, err := rewrite(rawURL)
			if err != nil {
				return "", start, false, err
			}
			if !replace {
				return tag, end + 1, true, nil
			}
			rebuilt := tag[:valStart] + newURL + tag[valEnd:]
			return rebuilt, end + 1, true, nil
		}
	}

	return tag, end + 1, true, nil
}

func isRemoteHTTPURL(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func hasFoldPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func isHTMLWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

var allowedImageExts = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
	".svg":  true,
	".avif": true,
	".bmp":  true,
	".ico":  true,
	".tif":  true,
	".tiff": true,
}

var contentTypeToExt = map[string]string{
	"image/png":     ".png",
	"image/jpeg":    ".jpg",
	"image/jpg":     ".jpg",
	"image/gif":     ".gif",
	"image/webp":    ".webp",
	"image/svg+xml": ".svg",
	"image/avif":    ".avif",
	"image/bmp":     ".bmp",
	"image/x-icon":  ".ico",
	"image/tiff":    ".tiff",
}

// ImageFilename derives a deterministic, URL-safe local filename for a downloaded
// image using its original URL basename (when meaningful), detected extension,
// and an 8-character SHA-256 content hash suffix to prevent collisions.
func ImageFilename(rawURL, contentType string, data []byte) string {
	var baseName string
	if u, err := url.Parse(rawURL); err == nil {
		if decoded, err := url.PathUnescape(path.Base(u.Path)); err == nil && decoded != "." && decoded != "/" {
			baseName = decoded
		}
	}

	ext := strings.ToLower(path.Ext(baseName))
	if !allowedImageExts[ext] {
		ext = extFromContentType(contentType)
	}
	if ext == "" {
		ext = detectImageExt(data)
	}
	if ext == "" {
		ext = ".png"
	}

	stemRaw := strings.TrimSuffix(baseName, path.Ext(baseName))
	stem := sanitizeASCIIStem(stemRaw)
	if stem == "" {
		stem = "image"
	}

	shortHash := ContentHash(data)[:8]
	return stem + "-" + shortHash + ext
}

func extFromContentType(ct string) string {
	if strings.TrimSpace(ct) == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		mediaType = strings.TrimSpace(strings.Split(ct, ";")[0])
	}
	return contentTypeToExt[strings.ToLower(mediaType)]
}

func detectImageExt(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("<svg")) || bytes.HasPrefix(trimmed, []byte("<?xml")) && bytes.Contains(trimmed[:min(len(trimmed), 512)], []byte("<svg")) {
		return ".svg"
	}
	detected := http.DetectContentType(data)
	return extFromContentType(detected)
}

func sanitizeASCIIStem(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			b.WriteByte(ch)
			prevDash = false
		} else if ch == '-' || ch == '_' || ch == ' ' || ch == '.' {
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}
