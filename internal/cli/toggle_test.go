package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frinfo702/linear-doc-sync/internal/github"
	"github.com/frinfo702/linear-doc-sync/internal/linear"
	"github.com/frinfo702/linear-doc-sync/internal/state"
)

func TestKeyReader(t *testing.T) {
	input := "\x1b[A\x1b[B\x1b[C\x1b[D\x1bOA\x1bOBjk ar\r\x1bq\x03"
	kr := newKeyReader(strings.NewReader(input))

	want := []keyEvent{
		keyUp,
		keyDown,
		keyToggle,
		keyToggle,
		keyUp,
		keyDown,
		keyDown,
		keyUp,
		keyToggle,
		keyToggleAll,
		keyReset,
		keyEnter,
		keyCancel, // standalone ESC (not followed by [ or O)
		keyCancel, // q
		keyCancel, // Ctrl+C
	}

	for i, w := range want {
		got, err := kr.nextKey()
		if err != nil {
			t.Fatalf("event %d: unexpected error: %v", i, err)
		}
		if got != w {
			t.Fatalf("event %d: got %v, want %v", i, got, w)
		}
	}

	_, err := kr.nextKey()
	if err != io.EOF {
		t.Fatalf("expected io.EOF after all keys, got %v", err)
	}
}

func TestKeyReaderIgnoresUnknownCSI(t *testing.T) {
	// PageUp (\x1b[5~) should be ignored, followed by space + enter.
	kr := newKeyReader(strings.NewReader("\x1b[5~ \r"))
	ev, err := kr.nextKey()
	if err != nil || ev != keyToggle {
		t.Fatalf("expected keyToggle after ignored CSI, got %v, err=%v", ev, err)
	}
	ev, err = kr.nextKey()
	if err != nil || ev != keyEnter {
		t.Fatalf("expected keyEnter, got %v, err=%v", ev, err)
	}
}

func TestCheckboxPickerInteractions(t *testing.T) {
	t.Run("toggle multiple items and confirm", func(t *testing.T) {
		items := []pickerItem{
			{Title: "Alpha", Slug: "alpha", Initial: true, Selected: true},
			{Title: "Beta", Slug: "beta", Initial: false, Selected: false},
			{Title: "Gamma", Slug: "gamma", Initial: false, Selected: false},
		}
		var out bytes.Buffer
		// Toggle Alpha off, move down twice, toggle Gamma on, press Enter.
		confirmed, err := runCheckboxPicker(strings.NewReader(" \x1b[B\x1b[B \r"), &out, items)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !confirmed {
			t.Fatalf("expected confirmed=true")
		}
		if items[0].Selected != false || items[1].Selected != false || items[2].Selected != true {
			t.Fatalf("unexpected selections: %+v", items)
		}
		if !strings.Contains(out.String(), "[x]") || !strings.Contains(out.String(), "[ ]") {
			t.Fatalf("expected checkbox indicators [x] and [ ] in output, got:\n%s", out.String())
		}
	})

	t.Run("toggle all and reset", func(t *testing.T) {
		items := []pickerItem{
			{Title: "Alpha", Slug: "alpha", Initial: true, Selected: true},
			{Title: "Beta", Slug: "beta", Initial: false, Selected: false},
		}
		var out bytes.Buffer
		// 'a' checks all, 'r' resets to initial, Enter confirms.
		confirmed, err := runCheckboxPicker(strings.NewReader("ar\r"), &out, items)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !confirmed {
			t.Fatalf("expected confirmed=true")
		}
		if items[0].Selected != true || items[1].Selected != false {
			t.Fatalf("expected reset to initial state, got: %+v", items)
		}
	})

	t.Run("cancel with q or EOF does not confirm", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			input string
		}{
			{"q", " q"},
			{"esc", " \x1b"},
			{"ctrl-c", " \x03"},
			{"eof", " "},
		} {
			t.Run(tc.name, func(t *testing.T) {
				items := []pickerItem{{Title: "Alpha", Slug: "alpha", Initial: false, Selected: false}}
				var out bytes.Buffer
				confirmed, err := runCheckboxPicker(strings.NewReader(tc.input), &out, items)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if confirmed {
					t.Fatalf("expected confirmed=false for %s", tc.name)
				}
			})
		}
	})

	t.Run("unicode label truncation respects display width and preserves valid UTF-8", func(t *testing.T) {
		got := truncateLabel("日本語のドキュメントタイトル非常に長い名前", 10)
		if displayWidth(got) > 10 || !strings.HasSuffix(got, "...") {
			t.Fatalf("unexpected truncated label: %q (displayWidth=%d)", got, displayWidth(got))
		}
	})

	t.Run("narrow terminal wrapping and CJK alignment", func(t *testing.T) {
		items := []pickerItem{
			{Title: "Sample", Slug: "sample", Initial: true, Selected: false},
			{Title: "TOEFLノート", Slug: "toeflノート", Initial: false, Selected: false},
		}
		const termWidth = 65
		lines := buildPickerLines(items, 0, 0, len(items), termWidth, true, false)

		// Every generated line should fit within termWidth so redraw row counts stay exact.
		for i, line := range lines {
			if w := displayWidth(line); w > termWidth {
				t.Fatalf("line %d exceeds termWidth %d (width=%d): %q", i, termWidth, w, line)
			}
		}
		if rows := countPhysicalRows(lines, termWidth); rows != len(lines) {
			t.Fatalf("expected physical rows (%d) to equal line count (%d) when lines fit", rows, len(lines))
		}

		// Verify physical row counting when a line does wrap.
		wrapped := []string{
			strings.Repeat("a", 70),           // exactly 1 row on 70-col terminal
			strings.Repeat("a", 71),           // wraps onto 2 rows on 70-col terminal
			"",                                // empty line = 1 row
			ansiBold + "TOEFLノート" + ansiReset, // 5 + 3*2 = 11 cols = 1 row
		}
		if got := countPhysicalRows(wrapped, 70); got != 5 {
			t.Fatalf("countPhysicalRows = %d, want 5", got)
		}
	})
}

func TestInteractiveToggleWorkflow(t *testing.T) {
	cfgDir := filepath.Join(t.TempDir(), ".config", "lsy")
	t.Setenv("LSY_CONFIG_DIR", cfgDir)
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("LSY_GITHUB_REPO", "")
	t.Setenv("LSY_GITHUB_BRANCH", "")
	t.Setenv("LSY_REPO_PATH", "")
	_ = os.Unsetenv("LSY_REPO_PATH")
	t.Setenv("LSY_DOCS_DIR", "")
	t.Setenv("LSY_WITH_IMAGE", "")

	imgBytes := []byte("\x89PNG\r\n\x1a\ntoggle-img")

	docs := map[string]map[string]any{
		"doc-1": {
			"id":        "doc-1",
			"title":     "Alpha Public",
			"slugId":    "aaa111",
			"content":   "# Alpha\n\n![pic](https://uploads.linear.app/org/u1/pic.png)\n",
			"url":       "https://linear.app/example/document/alpha-public-aaa111",
			"updatedAt": "2026-11-21T09:00:00Z",
		},
		"doc-2": {
			"id":        "doc-2",
			"title":     "Beta Private",
			"slugId":    "bbb222",
			"content":   "# Beta\n\nSecret draft v1.",
			"url":       "https://linear.app/example/document/beta-private-bbb222",
			"updatedAt": "2026-11-21T09:00:00Z",
		},
		"doc-3": {
			"id":        "doc-3",
			"title":     "Gamma Untouched",
			"slugId":    "ccc333",
			"content":   "# Gamma\n\nStays published.",
			"url":       "https://linear.app/example/document/gamma-untouched-ccc333",
			"updatedAt": "2026-11-21T09:00:00Z",
		},
	}

	linearCalls := 0
	linearHTTP := newTestHTTPClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host == "uploads.linear.app" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imgBytes)
			return
		}
		linearCalls++
		var req struct {
			Variables map[string]string `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		id := req.Variables["id"]
		w.Header().Set("Content-Type", "application/json")
		for _, d := range docs {
			if d["id"] == id || d["slugId"] == id {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"document": d}})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"message": "Entity not found"}}})
	})

	type ghFile struct {
		content []byte
		sha     string
	}
	remoteFiles := map[string]ghFile{}
	ghMutationCalls := 0

	ghHTTP := newTestHTTPClient(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch r.Method {
		case http.MethodGet:
			f, ok := remoteFiles[p]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"path": strings.TrimPrefix(p, "/repos/example/web/contents/"),
				"sha":  f.sha,
			})
		case http.MethodPut:
			ghMutationCalls++
			var reqBody struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			decoded, _ := base64.StdEncoding.DecodeString(reqBody.Content)
			blobSHA := github.GitBlobSHA(decoded)
			_, existed := remoteFiles[p]
			remoteFiles[p] = ghFile{content: decoded, sha: blobSHA}
			if existed {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusCreated)
			}
			rel := strings.TrimPrefix(p, "/repos/example/web/contents/")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": map[string]string{
					"path":     rel,
					"sha":      blobSHA,
					"html_url": "https://github.com/example/web/blob/main/" + rel,
				},
				"commit": map[string]string{"sha": "commit-sha"},
			})
		case http.MethodDelete:
			ghMutationCalls++
			delete(remoteFiles, p)
			w.WriteHeader(http.StatusOK)
		}
	})

	runWithInput := func(stdin string, args ...string) (string, error) {
		var out, errBuf bytes.Buffer
		app := &App{
			Build:  BuildInfo{Version: "0.3.0"},
			Stdin:  strings.NewReader(stdin),
			Stdout: &out,
			Stderr: &errBuf,
			NewLinearClient: func(apiKey string) *linear.Client {
				c := linear.NewClient(apiKey)
				c.Endpoint = "https://linear.test/graphql"
				c.HTTPClient = linearHTTP
				return c
			},
			NewGitHubClient: func(token string) *github.Client {
				c := github.NewClient(token)
				c.APIBase = "https://github.test"
				c.HTTPClient = ghHTTP
				return c
			},
		}
		err := app.Run(context.Background(), args)
		return out.String(), err
	}

	// Setup config
	_, _ = runWithInput("", "config", "set", "linear_api_key", "lin_api_secret")
	_, _ = runWithInput("", "config", "set", "github_token", "ghp_secret")
	_, _ = runWithInput("", "config", "set", "github_repo", "example/web")
	_, _ = runWithInput("", "config", "set", "repo-path", "docs")

	// Empty state gives friendly message
	out, err := runWithInput("", "toggle")
	if err != nil {
		t.Fatalf("toggle on empty state failed: %v", err)
	}
	if !strings.Contains(out, "No tracked documents found") {
		t.Fatalf("expected empty state message, got:\n%s", out)
	}

	// Seed 3 documents:
	// - alpha-public (published, with image)
	// - beta-private (fetched/unpublished)
	// - gamma-untouched (published)
	if _, err := runWithInput("", "publish", "doc-1", "--with-image"); err != nil {
		t.Fatalf("seed publish doc-1: %v", err)
	}
	if _, err := runWithInput("", "fetch", "doc-2"); err != nil {
		t.Fatalf("seed fetch doc-2: %v", err)
	}
	if _, err := runWithInput("", "publish", "doc-3"); err != nil {
		t.Fatalf("seed publish doc-3: %v", err)
	}

	// 1. Cancel with 'q' -> zero GitHub mutations
	ghMutationCalls = 0
	out, err = runWithInput(" q", "toggle")
	if err != nil {
		t.Fatalf("toggle cancel failed: %v", err)
	}
	if !strings.Contains(out, "Cancelled. No changes made.") {
		t.Fatalf("expected cancel message, got:\n%s", out)
	}
	if ghMutationCalls != 0 {
		t.Fatalf("expected 0 github mutations on cancel, got %d", ghMutationCalls)
	}

	// 2. Confirm with no changes -> zero GitHub mutations
	out, err = runWithInput("\r", "toggle")
	if err != nil {
		t.Fatalf("toggle no-op failed: %v", err)
	}
	if !strings.Contains(out, "No changes made.") {
		t.Fatalf("expected no changes message, got:\n%s", out)
	}
	if ghMutationCalls != 0 {
		t.Fatalf("expected 0 github mutations on no-op, got %d", ghMutationCalls)
	}

	// 3. Dry run: toggle alpha-public OFF and beta-private ON -> no remote changes
	out, err = runWithInput(" j \r", "toggle", "--dry-run")
	if err != nil {
		t.Fatalf("toggle dry-run failed: %v", err)
	}
	if !strings.Contains(out, "[publish] Beta Private") || !strings.Contains(out, "[private] Alpha Public") {
		t.Fatalf("unexpected dry-run output:\n%s", out)
	}
	if ghMutationCalls != 0 {
		t.Fatalf("expected 0 github mutations on dry-run, got %d", ghMutationCalls)
	}

	// 4. Real toggle via `lsy switch` alias:
	// Items sorted by slug:
	//   0: alpha-public   [x] -> toggle OFF (unpublish md + remote image, keep local copy)
	//   1: beta-private   [ ] -> toggle ON  (publish local md to GitHub, no Linear API call)
	//   2: gamma-untouched [x] -> untouched
	linearCalls = 0
	ghMutationCalls = 0
	out, err = runWithInput(" \x1b[B \r", "switch")
	if err != nil {
		t.Fatalf("switch failed: %v", err)
	}
	if !strings.Contains(out, "Summary: 1 published, 1 made private") {
		t.Fatalf("unexpected switch summary:\n%s", out)
	}
	if linearCalls != 0 {
		t.Fatalf("expected 0 linear calls without --fetch, got %d", linearCalls)
	}

	// Verify GitHub remote state: alpha md & alpha image deleted, beta md added, gamma untouched.
	if _, ok := remoteFiles["/repos/example/web/contents/docs/alpha-public.md"]; ok {
		t.Fatalf("expected alpha-public.md removed from GitHub")
	}
	for k := range remoteFiles {
		if strings.Contains(k, "images/alpha-public/") {
			t.Fatalf("expected alpha-public remote image removed from GitHub, still found %s", k)
		}
	}
	if _, ok := remoteFiles["/repos/example/web/contents/docs/beta-private.md"]; !ok {
		t.Fatalf("expected beta-private.md uploaded to GitHub, remoteFiles=%v", remoteFiles)
	}
	if _, ok := remoteFiles["/repos/example/web/contents/docs/gamma-untouched.md"]; !ok {
		t.Fatalf("expected gamma-untouched.md still present on GitHub")
	}

	// Verify local copies & state.json
	st, err := state.Load()
	if err != nil {
		t.Fatalf("state load: %v", err)
	}
	alphaRec, _ := st.Find("alpha-public")
	betaRec, _ := st.Find("beta-private")
	gammaRec, _ := st.Find("gamma-untouched")
	if alphaRec.Status != state.StatusUnpublished {
		t.Fatalf("expected alpha-public unpublished, got %s", alphaRec.Status)
	}
	if _, err := os.Stat(alphaRec.LocalPath); err != nil {
		t.Fatalf("expected alpha-public local file preserved: %v", err)
	}
	if len(alphaRec.Images) != 1 || alphaRec.Images[0].LocalPath == "" || alphaRec.Images[0].RemotePath != "" {
		t.Fatalf("expected alpha-public local image preserved with cleared remote metadata: %+v", alphaRec.Images)
	}
	if betaRec.Status != state.StatusPublished || betaRec.RemotePath != "docs/beta-private.md" {
		t.Fatalf("expected beta-private published at docs/beta-private.md, got %+v", betaRec)
	}
	if gammaRec.Status != state.StatusPublished {
		t.Fatalf("expected gamma-untouched still published, got %s", gammaRec.Status)
	}

	// 5. Re-publish alpha-public via `lsy list -i --unpublished` (only alpha-public shown!):
	// It should re-upload BOTH alpha-public.md AND its preserved local image!
	out, err = runWithInput(" \r", "list", "-i", "--unpublished")
	if err != nil {
		t.Fatalf("list -i --unpublished failed: %v", err)
	}
	if !strings.Contains(out, "Summary: 1 published, 0 made private") {
		t.Fatalf("unexpected list -i output:\n%s", out)
	}
	if _, ok := remoteFiles["/repos/example/web/contents/docs/alpha-public.md"]; !ok {
		t.Fatalf("expected alpha-public.md re-uploaded to GitHub")
	}
	hasAlphaImg := false
	for k := range remoteFiles {
		if strings.Contains(k, "images/alpha-public/") {
			hasAlphaImg = true
		}
	}
	if !hasAlphaImg {
		t.Fatalf("expected alpha-public local image re-uploaded to GitHub, remoteFiles=%v", remoteFiles)
	}

	// 6. Test --fetch flag: unpublish beta-private, update it in Linear, then toggle it ON with --fetch.
	if _, err := runWithInput("", "unpublish", "beta-private"); err != nil {
		t.Fatalf("unpublish beta-private: %v", err)
	}
	docs["doc-2"]["content"] = "# Beta\n\nFreshly fetched v2!"
	docs["doc-2"]["updatedAt"] = "2026-11-22T00:00:00Z"

	linearCalls = 0
	out, err = runWithInput(" \r", "toggle", "--unpublished", "--fetch")
	if err != nil {
		t.Fatalf("toggle --fetch failed: %v", err)
	}
	if linearCalls != 1 {
		t.Fatalf("expected 1 linear API call with --fetch, got %d", linearCalls)
	}
	uploadedBeta := string(remoteFiles["/repos/example/web/contents/docs/beta-private.md"].content)
	if !strings.Contains(uploadedBeta, "Freshly fetched v2!") {
		t.Fatalf("expected --fetch to publish refreshed Linear content, got:\n%s", uploadedBeta)
	}
}
