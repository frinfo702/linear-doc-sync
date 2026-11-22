package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

type pickerItem struct {
	Title    string
	Slug     string
	Initial  bool // true = published, false = private
	Selected bool
}

func (it pickerItem) label() string {
	title := strings.TrimSpace(it.Title)
	slug := strings.TrimSpace(it.Slug)
	switch {
	case title == "":
		return slug
	case slug == "" || strings.EqualFold(title, slug):
		return title
	default:
		return fmt.Sprintf("%s (%s)", title, slug)
	}
}

type keyEvent int

const (
	keyNone keyEvent = iota
	keyUp
	keyDown
	keyToggle
	keyToggleAll
	keyReset
	keyEnter
	keyCancel
)

type keyReader struct {
	r   io.Reader
	buf []byte
}

func newKeyReader(r io.Reader) *keyReader {
	return &keyReader{r: r}
}

func (kr *keyReader) nextKey() (keyEvent, error) {
	for {
		if len(kr.buf) == 0 {
			var tmp [32]byte
			n, err := kr.r.Read(tmp[:])
			if n > 0 {
				kr.buf = append(kr.buf, tmp[:n]...)
			}
			if len(kr.buf) == 0 {
				if err != nil {
					return keyNone, err
				}
				continue
			}
		}

		b := kr.buf[0]
		switch b {
		case 0x1b: // ESC or escape sequence
			if len(kr.buf) >= 3 && kr.buf[1] == '[' {
				// Consume full CSI sequence ending in 0x40..0x7E.
				end := 2
				for end < len(kr.buf) && (kr.buf[end] < 0x40 || kr.buf[end] > 0x7e) {
					end++
				}
				if end < len(kr.buf) {
					finalByte := kr.buf[end]
					seqLen := end + 1
					kr.buf = kr.buf[seqLen:]
					if seqLen == 3 {
						switch finalByte {
						case 'A':
							return keyUp, nil
						case 'B':
							return keyDown, nil
						case 'C', 'D':
							return keyToggle, nil
						}
					}
					continue
				}
			}
			if len(kr.buf) >= 3 && kr.buf[1] == 'O' {
				code := kr.buf[2]
				kr.buf = kr.buf[3:]
				switch code {
				case 'A':
					return keyUp, nil
				case 'B':
					return keyDown, nil
				case 'C', 'D':
					return keyToggle, nil
				default:
					continue
				}
			}
			kr.buf = kr.buf[1:]
			return keyCancel, nil
		case '\r', '\n':
			kr.buf = kr.buf[1:]
			return keyEnter, nil
		case ' ', 'x', 'X':
			kr.buf = kr.buf[1:]
			return keyToggle, nil
		case 'k', 'K', 0x10: // k or Ctrl+P
			kr.buf = kr.buf[1:]
			return keyUp, nil
		case 'j', 'J', 0x0e: // j or Ctrl+N
			kr.buf = kr.buf[1:]
			return keyDown, nil
		case 'a', 'A':
			kr.buf = kr.buf[1:]
			return keyToggleAll, nil
		case 'r', 'R':
			kr.buf = kr.buf[1:]
			return keyReset, nil
		case 'q', 'Q', 0x03, 0x04: // q, Ctrl+C, Ctrl+D
			kr.buf = kr.buf[1:]
			return keyCancel, nil
		default:
			kr.buf = kr.buf[1:]
			continue
		}
	}
}

func runCheckboxPicker(in io.Reader, out io.Writer, items []pickerItem) (bool, error) {
	if len(items) == 0 {
		return false, nil
	}

	inTTY := false
	outTTY := false
	var outFile *os.File
	termWidth, termHeight := 80, 24

	if f, ok := in.(*os.File); ok && isTerminal(f.Fd()) {
		inTTY = true
		restore, err := makeRaw(f.Fd())
		if err != nil {
			return false, fmt.Errorf("enable interactive terminal mode: %w", err)
		}
		defer restore()
	}

	if f, ok := out.(*os.File); ok && isTerminal(f.Fd()) {
		outTTY = true
		outFile = f
		termWidth, termHeight = terminalSize(f.Fd())
		fmt.Fprint(out, "\x1b[?25l")
		defer fmt.Fprint(out, "\x1b[?25h")
	}

	cursor := 0
	offset := 0
	prevRows := 0

	computeMaxVisible := func(height int) int {
		mv := len(items)
		if outTTY {
			// Reserve lines for header, controls (possibly wrapped), spacing, scroll hints, and footer.
			avail := height - 9
			if avail < 3 {
				avail = 3
			}
			if avail < mv {
				mv = avail
			}
		}
		return mv
	}

	maxVisible := computeMaxVisible(termHeight)

	render := func(final bool) {
		if outTTY && outFile != nil {
			termWidth, termHeight = terminalSize(outFile.Fd())
			maxVisible = computeMaxVisible(termHeight)
			if cursor < offset {
				offset = cursor
			} else if cursor >= offset+maxVisible {
				offset = cursor - maxVisible + 1
			}
		}
		lines := buildPickerLines(items, cursor, offset, maxVisible, termWidth, outTTY, final)
		var frame strings.Builder
		if outTTY && prevRows > 0 {
			fmt.Fprintf(&frame, "\x1b[%dA\r\x1b[J", prevRows)
		}
		for _, line := range lines {
			if outTTY {
				frame.WriteString("\r\x1b[2K")
				frame.WriteString(line)
				frame.WriteByte('\n')
			} else {
				frame.WriteString(line)
				frame.WriteByte('\n')
			}
		}
		if outTTY {
			frame.WriteString("\r\x1b[J")
		}
		_, _ = io.WriteString(out, frame.String())
		prevRows = countPhysicalRows(lines, termWidth)
	}

	render(false)

	kr := newKeyReader(in)
	for {
		ev, err := kr.nextKey()
		if err != nil {
			if err == io.EOF {
				if outTTY {
					render(true)
				}
				return false, nil
			}
			return false, err
		}

		switch ev {
		case keyUp:
			if cursor > 0 {
				cursor--
				if cursor < offset {
					offset = cursor
				}
			}
		case keyDown:
			if cursor < len(items)-1 {
				cursor++
				if cursor >= offset+maxVisible {
					offset = cursor - maxVisible + 1
				}
			}
		case keyToggle:
			items[cursor].Selected = !items[cursor].Selected
		case keyToggleAll:
			allSelected := true
			for _, it := range items {
				if !it.Selected {
					allSelected = false
					break
				}
			}
			target := !allSelected
			for i := range items {
				items[i].Selected = target
			}
		case keyReset:
			for i := range items {
				items[i].Selected = items[i].Initial
			}
		case keyEnter:
			if outTTY {
				render(true)
			}
			return true, nil
		case keyCancel:
			if outTTY {
				render(true)
			}
			return false, nil
		default:
			continue
		}

		if inTTY || outTTY {
			render(false)
		}
	}
}

func buildPickerLines(items []pickerItem, cursor, offset, maxVisible, termWidth int, useANSI, final bool) []string {
	lines := make([]string, 0, maxVisible+7)

	if useANSI {
		lines = append(lines, fmt.Sprintf("%sToggle document visibility%s ([x] = published, [ ] = private):", ansiBold, ansiReset))
	} else {
		lines = append(lines, "Toggle document visibility ([x] = published, [ ] = private):")
	}

	controlTokens := []string{
		"↑/↓ (j/k) move",
		"space toggle",
		"a all",
		"r reset",
		"enter confirm",
		"q/esc cancel",
	}
	for _, hintLine := range wrapTokens(controlTokens, "  ", " • ", termWidth) {
		if useANSI {
			lines = append(lines, ansiDim+hintLine+ansiReset)
		} else {
			lines = append(lines, hintLine)
		}
	}
	lines = append(lines, "")

	end := offset + maxVisible
	if end > len(items) {
		end = len(items)
	}

	// Calculate aligned label width across visible items using terminal cell width.
	maxLabel := 0
	for i := offset; i < end; i++ {
		if n := displayWidth(items[i].label()); n > maxLabel {
			maxLabel = n
		}
	}
	// Reserve columns for "> [x] " (6), gap "  " (2), and longest status "published -> private" (20).
	maxAllowedLabel := termWidth - 28
	if maxAllowedLabel < 10 {
		maxAllowedLabel = 10
	}
	if maxLabel > maxAllowedLabel {
		maxLabel = maxAllowedLabel
	}

	if offset > 0 {
		hint := fmt.Sprintf("  ... (%d more above)", offset)
		if useANSI {
			hint = ansiDim + hint + ansiReset
		}
		lines = append(lines, hint)
	}

	for i := offset; i < end; i++ {
		it := items[i]
		active := !final && i == cursor

		prefix := "  "
		if active {
			if useANSI {
				prefix = ansiCyan + ansiBold + "> " + ansiReset
			} else {
				prefix = "> "
			}
		}

		box := "[ ]"
		if it.Selected {
			box = "[x]"
		}
		if useANSI {
			if it.Selected {
				box = ansiGreen + box + ansiReset
			} else {
				box = ansiDim + box + ansiReset
			}
		}

		lbl := truncateLabel(it.label(), maxLabel)
		padCount := maxLabel - displayWidth(lbl)
		if padCount < 0 {
			padCount = 0
		}
		pad := strings.Repeat(" ", padCount)
		if useANSI && active {
			lbl = ansiBold + lbl + ansiReset
		}

		status := formatItemStatus(it, useANSI)
		lines = append(lines, fmt.Sprintf("%s%s %s%s  %s", prefix, box, lbl, pad, status))
	}

	if end < len(items) {
		hint := fmt.Sprintf("  ... (%d more below)", len(items)-end)
		if useANSI {
			hint = ansiDim + hint + ansiReset
		}
		lines = append(lines, hint)
	}

	lines = append(lines, "")

	toPublish, toPrivate := countPendingChanges(items)
	var footer string
	if toPublish == 0 && toPrivate == 0 {
		footer = "No pending changes."
		if useANSI {
			footer = ansiDim + footer + ansiReset
		}
	} else {
		var parts []string
		if toPublish > 0 {
			p := fmt.Sprintf("%d to publish", toPublish)
			if useANSI {
				p = ansiGreen + p + ansiReset
			}
			parts = append(parts, p)
		}
		if toPrivate > 0 {
			p := fmt.Sprintf("%d to make private", toPrivate)
			if useANSI {
				p = ansiYellow + p + ansiReset
			}
			parts = append(parts, p)
		}
		footer = "Pending: " + strings.Join(parts, ", ")
	}
	lines = append(lines, footer)
	return lines
}

func wrapTokens(tokens []string, indent, sep string, maxWidth int) []string {
	if len(tokens) == 0 {
		return nil
	}
	if maxWidth <= 0 {
		return []string{indent + strings.Join(tokens, sep)}
	}
	var out []string
	cur := indent + tokens[0]
	for _, tok := range tokens[1:] {
		candidate := cur + sep + tok
		if displayWidth(candidate) <= maxWidth {
			cur = candidate
		} else {
			out = append(out, cur)
			cur = indent + tok
		}
	}
	out = append(out, cur)
	return out
}

func countPhysicalRows(lines []string, termWidth int) int {
	if termWidth <= 0 {
		return len(lines)
	}
	rows := 0
	for _, line := range lines {
		w := displayWidth(line)
		if w <= 0 {
			rows++
		} else {
			rows += (w + termWidth - 1) / termWidth
		}
	}
	return rows
}

func displayWidth(s string) int {
	w := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		if r == 0x1b {
			inEsc = true
			continue
		}
		w += runeDisplayWidth(r)
	}
	return w
}

func runeDisplayWidth(r rune) int {
	if r == 0 || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	if isWideRune(r) {
		return 2
	}
	return 1
}

// isWideRune reports whether r occupies two terminal columns (East Asian Wide/Fullwidth and emoji).
func isWideRune(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) || // Hangul Jamo
		r == 0x2329 || r == 0x232A ||
		(r >= 0x2E80 && r <= 0x303E) || // CJK Radicals, Kangxi, Symbols & Punctuation
		(r >= 0x3040 && r <= 0x33FF) || // Hiragana, Katakana, Bopomofo, CJK Compatibility
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Unified Ideographs Extension A
		(r >= 0x4E00 && r <= 0xA4CF) || // CJK Unified Ideographs, Yi
		(r >= 0xA960 && r <= 0xA97C) || // Hangul Jamo Extended-A
		(r >= 0xAC00 && r <= 0xD7A3) || // Hangul Syllables
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
		(r >= 0xFE10 && r <= 0xFE19) || // Vertical forms
		(r >= 0xFE30 && r <= 0xFE6F) || // CJK Compatibility Forms
		(r >= 0xFF00 && r <= 0xFF60) || // Fullwidth ASCII & Punctuation
		(r >= 0xFFE0 && r <= 0xFFE6) || // Fullwidth Signs
		(r >= 0x1F300 && r <= 0x1FAFF) || // Misc Symbols, Pictographs & Emoji
		(r >= 0x20000 && r <= 0x3FFFD) // CJK Extensions B-G
}

func formatItemStatus(it pickerItem, useANSI bool) string {
	switch {
	case !it.Initial && it.Selected:
		if useANSI {
			return ansiGreen + "private -> publish" + ansiReset
		}
		return "private -> publish"
	case it.Initial && !it.Selected:
		if useANSI {
			return ansiYellow + "published -> private" + ansiReset
		}
		return "published -> private"
	case it.Selected:
		if useANSI {
			return ansiDim + "published" + ansiReset
		}
		return "published"
	default:
		if useANSI {
			return ansiDim + "private" + ansiReset
		}
		return "private"
	}
}

func countPendingChanges(items []pickerItem) (toPublish, toPrivate int) {
	for _, it := range items {
		if !it.Initial && it.Selected {
			toPublish++
		} else if it.Initial && !it.Selected {
			toPrivate++
		}
	}
	return toPublish, toPrivate
}

func truncateLabel(s string, maxWidth int) string {
	if maxWidth <= 0 || displayWidth(s) <= maxWidth {
		return s
	}
	if maxWidth <= 3 {
		var b strings.Builder
		w := 0
		for _, r := range s {
			rw := runeDisplayWidth(r)
			if w+rw > maxWidth {
				break
			}
			b.WriteRune(r)
			w += rw
		}
		return b.String()
	}
	limit := maxWidth - 3
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := runeDisplayWidth(r)
		if w+rw > limit {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "..."
}
