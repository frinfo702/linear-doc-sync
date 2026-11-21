package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frinfo702/linear-doc-sync/internal/github"
	"github.com/frinfo702/linear-doc-sync/internal/linear"
	"github.com/frinfo702/linear-doc-sync/internal/state"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newTestHTTPClient(handler http.HandlerFunc) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			handler(rec, req)
			return rec.Result(), nil
		}),
	}
}

func TestEndToEndWorkflow(t *testing.T) {
	cfgDir := filepath.Join(t.TempDir(), ".config", "lsy")
	t.Setenv("LSY_CONFIG_DIR", cfgDir)
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("LSY_GITHUB_REPO", "")
	t.Setenv("LSY_GITHUB_BRANCH", "")
	t.Setenv("LSY_REPO_PATH", "")
	t.Setenv("LSY_DOCS_DIR", "")
	t.Setenv("LSY_WITH_IMAGE", "")

	// Mock Linear GraphQL API.
	linearHTTP := newTestHTTPClient(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"document": map[string]any{
					"id":      "doc-1",
					"title":   "Launch Plan",
					"slugId":  "abc123456789",
					"content": "# Launch Plan\n\nShip it!",
					"url":     "https://linear.app/example-project/document/launch-plan-abc123456789",
				},
			},
		})
	})

	// Mock GitHub Contents API.
	remoteFiles := map[string]string{}
	ghHTTP := newTestHTTPClient(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch r.Method {
		case http.MethodGet:
			sha, ok := remoteFiles[p]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"path": strings.TrimPrefix(p, "/repos/example/web/contents/"),
				"sha":  sha,
			})
		case http.MethodPut:
			_, existed := remoteFiles[p]
			remoteFiles[p] = "blob-sha-1"
			if existed {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusCreated)
			}
			rel := strings.TrimPrefix(p, "/repos/example/web/contents/")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": map[string]string{
					"path":     rel,
					"sha":      "blob-sha-1",
					"html_url": "https://github.com/example/web/blob/main/" + rel,
				},
				"commit": map[string]string{"sha": "commit-sha"},
			})
		case http.MethodDelete:
			delete(remoteFiles, p)
			w.WriteHeader(http.StatusOK)
		}
	})

	run := func(args ...string) (string, error) {
		var out, errBuf bytes.Buffer
		app := &App{
			Build:  BuildInfo{Version: "0.1.0", Commit: "abcdef1234", Date: "2026-11-21"},
			Stdin:  strings.NewReader(""),
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

	// 1. Configure lsy via `lsy config set`
	if _, err := run("config", "set", "linear_api_key", "lin_api_secret"); err != nil {
		t.Fatalf("config set linear_api_key: %v", err)
	}
	if _, err := run("config", "set", "github_token", "ghp_secret"); err != nil {
		t.Fatalf("config set github_token: %v", err)
	}
	if _, err := run("config", "set", "github_repo", "example/web"); err != nil {
		t.Fatalf("config set github_repo: %v", err)
	}
	if _, err := run("config", "set", "repo-path", "content/articles"); err != nil {
		t.Fatalf("config set repo-path: %v", err)
	}

	// Verify ~/.config/lsy exists
	if _, err := os.Stat(filepath.Join(cfgDir, "config.json")); err != nil {
		t.Fatalf("config.json missing: %v", err)
	}

	// 2. Publish with --repo-path override placed AFTER positional arg
	out, err := run("publish", "https://linear.app/example-project/document/launch-plan-abc123456789", "--repo-path", "blog/posts")
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if !strings.Contains(out, "blog/posts/launch-plan.md") {
		t.Fatalf("expected remote path blog/posts/launch-plan.md in output, got:\n%s", out)
	}
	if _, ok := remoteFiles["/repos/example/web/contents/blog/posts/launch-plan.md"]; !ok {
		t.Fatalf("expected file uploaded to blog/posts/launch-plan.md, got: %+v", remoteFiles)
	}

	localFile := filepath.Join(cfgDir, "docs", "launch-plan.md")
	data, err := os.ReadFile(localFile)
	if err != nil {
		t.Fatalf("local clone missing: %v", err)
	}
	if !strings.Contains(string(data), "# Launch Plan") || !strings.Contains(string(data), `title: "Launch Plan"`) {
		t.Fatalf("unexpected local markdown content:\n%s", string(data))
	}

	// 3. List tracked documents
	listOut, err := run("list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !strings.Contains(listOut, "launch-plan") || !strings.Contains(listOut, "published") {
		t.Fatalf("unexpected list output:\n%s", listOut)
	}

	// 4. Unpublish by document name
	unpubOut, err := run("unpublish", "launch-plan")
	if err != nil {
		t.Fatalf("unpublish failed: %v", err)
	}
	if !strings.Contains(unpubOut, "Unpublished") {
		t.Fatalf("unexpected unpublish output:\n%s", unpubOut)
	}
	if len(remoteFiles) != 0 {
		t.Fatalf("expected remote file deleted, got: %+v", remoteFiles)
	}
	// Local file should still exist after unpublish
	if _, err := os.Stat(localFile); err != nil {
		t.Fatalf("local file should be kept after unpublish: %v", err)
	}

	// 5. Remove local file with `lsy rm`
	rmOut, err := run("rm", "launch-plan")
	if err != nil {
		t.Fatalf("rm failed: %v", err)
	}
	if !strings.Contains(rmOut, "Removed local document") {
		t.Fatalf("unexpected rm output:\n%s", rmOut)
	}
	if _, err := os.Stat(localFile); !os.IsNotExist(err) {
		t.Fatalf("expected local file to be deleted")
	}

	// 6. List should now be empty
	emptyOut, err := run("list")
	if err != nil {
		t.Fatalf("list after rm failed: %v", err)
	}
	if !strings.Contains(emptyOut, "No tracked documents found.") {
		t.Fatalf("expected empty list message, got:\n%s", emptyOut)
	}
}

func TestPublishWithImagesAndCleanup(t *testing.T) {
	cfgDir := filepath.Join(t.TempDir(), ".config", "lsy")
	t.Setenv("LSY_CONFIG_DIR", cfgDir)
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("LSY_GITHUB_REPO", "")
	t.Setenv("LSY_GITHUB_BRANCH", "")
	t.Setenv("LSY_REPO_PATH", "")
	t.Setenv("LSY_DOCS_DIR", "")
	t.Setenv("LSY_WITH_IMAGE", "")

	img1Bytes := []byte("\x89PNG\r\n\x1a\nimg1-content")
	img2Bytes := []byte("\x89PNG\r\n\x1a\nimg2-content")
	docContent := "# Visual Guide\n\n![diagram](https://uploads.linear.app/org/u1/diagram.png)\n\n![chart](https://uploads.linear.app/org/u2/chart.png)\n"
	var sawSignedHeader string

	linearHTTP := newTestHTTPClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			sawSignedHeader = r.Header.Get("public-file-urls-expire-in")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"document": map[string]any{
						"id":        "doc-img-1",
						"title":     "Visual Guide",
						"slugId":    "vis123456789",
						"content":   docContent,
						"url":       "https://linear.app/example/document/visual-guide-vis123456789",
						"updatedAt": "2026-11-21T10:00:00Z",
					},
				},
			})
			return
		}
		if r.URL.Host == "uploads.linear.app" {
			if r.Header.Get("Authorization") != "lin_api_secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			switch r.URL.Path {
			case "/org/u1/diagram.png":
				_, _ = w.Write(img1Bytes)
			case "/org/u2/chart.png":
				_, _ = w.Write(img2Bytes)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	type ghFile struct {
		content []byte
		sha     string
	}
	remoteFiles := map[string]ghFile{}
	putCounts := map[string]int{}

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
			var reqBody struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			decoded, _ := base64.StdEncoding.DecodeString(reqBody.Content)
			blobSHA := github.GitBlobSHA(decoded)
			_, existed := remoteFiles[p]
			remoteFiles[p] = ghFile{content: decoded, sha: blobSHA}
			putCounts[p]++
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
			delete(remoteFiles, p)
			w.WriteHeader(http.StatusOK)
		}
	})

	run := func(args ...string) (string, error) {
		var out, errBuf bytes.Buffer
		app := &App{
			Build:  BuildInfo{Version: "0.2.0"},
			Stdin:  strings.NewReader(""),
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

	_, _ = run("config", "set", "linear_api_key", "lin_api_secret")
	_, _ = run("config", "set", "github_token", "ghp_secret")
	_, _ = run("config", "set", "github_repo", "example/web")
	_, _ = run("config", "set", "repo-path", "docs")

	// 1. Publish with --with-image
	out, err := run("publish", "https://linear.app/example/document/visual-guide-vis123456789", "--with-image")
	if err != nil {
		t.Fatalf("publish --with-image failed: %v", err)
	}
	if sawSignedHeader != "300" {
		t.Fatalf("expected public-file-urls-expire-in=300, got %q", sawSignedHeader)
	}
	if !strings.Contains(out, "Images: 2 total (2 uploaded)") {
		t.Fatalf("expected image upload summary, got:\n%s", out)
	}

	// Verify local markdown has relative ./images/visual-guide/... links and no uploads.linear.app links.
	localMD := filepath.Join(cfgDir, "docs", "visual-guide.md")
	mdBytes, err := os.ReadFile(localMD)
	if err != nil {
		t.Fatalf("read local md: %v", err)
	}
	if strings.Contains(string(mdBytes), "uploads.linear.app") {
		t.Fatalf("expected remote linear image URLs to be rewritten, got:\n%s", string(mdBytes))
	}
	if !strings.Contains(string(mdBytes), "./images/visual-guide/diagram-") || !strings.Contains(string(mdBytes), "./images/visual-guide/chart-") {
		t.Fatalf("expected local relative image paths, got:\n%s", string(mdBytes))
	}

	// Verify 3 files uploaded to GitHub (1 md + 2 images).
	if len(remoteFiles) != 3 {
		t.Fatalf("expected 3 remote files in github, got %d: %v", len(remoteFiles), remoteFiles)
	}

	// 2. Enable with_image in config and re-publish without --with-image flag; unchanged images should NOT be re-PUT.
	if _, err := run("config", "set", "with-image", "true"); err != nil {
		t.Fatalf("config set with-image failed: %v", err)
	}
	out2, err := run("publish", "visual-guide")
	if err != nil {
		t.Fatalf("re-publish failed: %v", err)
	}
	if !strings.Contains(out2, "Images: 2 total (0 uploaded)") {
		t.Fatalf("expected unchanged images to skip upload, got:\n%s", out2)
	}

	// 3. Remove second image from Linear doc and re-publish; stale local & remote image should be cleaned up.
	docContent = "# Visual Guide\n\n![diagram](https://uploads.linear.app/org/u1/diagram.png)\n"
	if _, err := run("publish", "visual-guide"); err != nil {
		t.Fatalf("re-publish after removing image failed: %v", err)
	}
	if len(remoteFiles) != 2 {
		t.Fatalf("expected 2 remote files after stale image cleanup, got %d", len(remoteFiles))
	}

	// 4. Unpublish deletes both Markdown and remaining remote image from GitHub, keeping local copies.
	if _, err := run("unpublish", "visual-guide"); err != nil {
		t.Fatalf("unpublish failed: %v", err)
	}
	if len(remoteFiles) != 0 {
		t.Fatalf("expected all remote files (md + images) deleted on unpublish, got %d", len(remoteFiles))
	}
	st, err := state.Load()
	if err != nil {
		t.Fatalf("state load: %v", err)
	}
	rec, _ := st.Find("visual-guide")
	if rec == nil || len(rec.Images) != 1 || rec.Images[0].LocalPath == "" {
		t.Fatalf("expected local image still tracked after unpublish: %+v", rec)
	}
	if _, err := os.Stat(rec.Images[0].LocalPath); err != nil {
		t.Fatalf("expected local image kept after unpublish: %v", err)
	}
	keptImgPath := rec.Images[0].LocalPath

	// 5. rm deletes local markdown AND local image.
	if _, err := run("rm", "visual-guide"); err != nil {
		t.Fatalf("rm failed: %v", err)
	}
	if _, err := os.Stat(keptImgPath); !os.IsNotExist(err) {
		t.Fatalf("expected local image to be deleted on rm")
	}
}

func TestFetchUpdateStateAndConflictResolution(t *testing.T) {
	cfgDir := filepath.Join(t.TempDir(), ".config", "lsy")
	t.Setenv("LSY_CONFIG_DIR", cfgDir)
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("LSY_GITHUB_REPO", "")
	t.Setenv("LSY_GITHUB_BRANCH", "")
	t.Setenv("LSY_REPO_PATH", "")
	t.Setenv("LSY_DOCS_DIR", "")
	t.Setenv("LSY_WITH_IMAGE", "")

	docs := map[string]map[string]any{
		"doc-1": {
			"id":        "doc-1",
			"title":     "Alpha Guide",
			"slugId":    "aaa111",
			"content":   "# Alpha v1\n\nInitial text.",
			"url":       "https://linear.app/example/document/alpha-guide-aaa111",
			"updatedAt": "2026-11-21T09:00:00Z",
		},
		"doc-2": {
			"id":        "doc-2",
			"title":     "Beta Guide",
			"slugId":    "bbb222",
			"content":   "# Beta v1\n\nInitial beta.",
			"url":       "https://linear.app/example/document/beta-guide-bbb222",
			"updatedAt": "2026-11-21T09:00:00Z",
		},
	}

	linearHTTP := newTestHTTPClient(func(w http.ResponseWriter, r *http.Request) {
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

	ghHTTP := newTestHTTPClient(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("fetch should never call GitHub API, got %s %s", r.Method, r.URL.Path)
	})

	runWithInput := func(stdin string, args ...string) (string, error) {
		var out, errBuf bytes.Buffer
		app := &App{
			Build:  BuildInfo{Version: "0.2.0"},
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

	_, _ = runWithInput("", "config", "set", "linear_api_key", "lin_api_secret")

	// 1. Fetch a new document (not published yet) -> saves locally & records in state.json
	out, err := runWithInput("", "fetch", "https://linear.app/example/document/alpha-guide-aaa111")
	if err != nil {
		t.Fatalf("fetch doc-1 failed: %v", err)
	}
	if !strings.Contains(out, `Fetched "Alpha Guide"`) {
		t.Fatalf("unexpected fetch output:\n%s", out)
	}

	alphaPath := filepath.Join(cfgDir, "docs", "alpha-guide.md")
	st, err := state.Load()
	if err != nil {
		t.Fatalf("state load: %v", err)
	}
	rec, _ := st.Find("alpha-guide")
	if rec == nil || rec.Status != state.StatusUnpublished || rec.LocalHash == "" {
		t.Fatalf("unexpected state after fetch: %+v", rec)
	}

	// 2. Fetch again with no changes -> reports Up to date
	out, err = runWithInput("", "fetch", "alpha-guide")
	if err != nil {
		t.Fatalf("second fetch failed: %v", err)
	}
	if !strings.Contains(out, "Up to date") {
		t.Fatalf("expected Up to date, got:\n%s", out)
	}

	// 3. Remote updates in Linear with NO local edits -> updates automatically without prompt
	docs["doc-1"]["content"] = "# Alpha v2\n\nUpdated from Linear."
	docs["doc-1"]["updatedAt"] = "2026-11-21T10:00:00Z"
	out, err = runWithInput("", "fetch", "alpha-guide")
	if err != nil {
		t.Fatalf("fetch updated doc failed: %v", err)
	}
	if !strings.Contains(out, `Updated "Alpha Guide"`) {
		t.Fatalf("expected Updated message, got:\n%s", out)
	}
	data, _ := os.ReadFile(alphaPath)
	if !strings.Contains(string(data), "# Alpha v2") {
		t.Fatalf("expected local file updated to v2, got:\n%s", string(data))
	}

	// 4. User edits local file -> prompt appears; user answers "n" -> local edits preserved!
	localEdit := "# Alpha LOCAL EDIT\n\nDo not clobber me!\n"
	if err := os.WriteFile(alphaPath, []byte(localEdit), 0o644); err != nil {
		t.Fatalf("write local edit: %v", err)
	}
	docs["doc-1"]["content"] = "# Alpha v3\n\nLatest from Linear."
	docs["doc-1"]["updatedAt"] = "2026-11-21T11:00:00Z"

	out, err = runWithInput("n\n", "fetch", "alpha-guide")
	if err != nil {
		t.Fatalf("fetch with decline failed: %v", err)
	}
	if !strings.Contains(out, "Local changes detected") || !strings.Contains(out, "Kept local changes") {
		t.Fatalf("expected conflict prompt and kept local message, got:\n%s", out)
	}
	data, _ = os.ReadFile(alphaPath)
	if string(data) != localEdit {
		t.Fatalf("local edits were overwritten despite answering n:\n%s", string(data))
	}

	// 5. User runs fetch again and answers "y" -> local file overwritten with Linear v3!
	out, err = runWithInput("y\n", "fetch", "alpha-guide")
	if err != nil {
		t.Fatalf("fetch with confirm failed: %v", err)
	}
	if !strings.Contains(out, `Overwrote "Alpha Guide"`) {
		t.Fatalf("expected Overwrote message, got:\n%s", out)
	}
	data, _ = os.ReadFile(alphaPath)
	if !strings.Contains(string(data), "# Alpha v3") {
		t.Fatalf("expected local file overwritten to v3, got:\n%s", string(data))
	}

	// 6. Test --keep-local and --force flags when local edits exist.
	if err := os.WriteFile(alphaPath, []byte(localEdit), 0o644); err != nil {
		t.Fatalf("write local edit 2: %v", err)
	}
	out, err = runWithInput("", "fetch", "alpha-guide", "--keep-local")
	if err != nil {
		t.Fatalf("fetch --keep-local failed: %v", err)
	}
	if !strings.Contains(out, "Kept local changes") {
		t.Fatalf("expected Kept local changes, got:\n%s", out)
	}
	data, _ = os.ReadFile(alphaPath)
	if string(data) != localEdit {
		t.Fatalf("--keep-local should not overwrite file")
	}

	out, err = runWithInput("", "fetch", "alpha-guide", "--force")
	if err != nil {
		t.Fatalf("fetch --force failed: %v", err)
	}
	if !strings.Contains(out, `Overwrote "Alpha Guide"`) {
		t.Fatalf("expected Overwrote with --force, got:\n%s", out)
	}
	data, _ = os.ReadFile(alphaPath)
	if !strings.Contains(string(data), "# Alpha v3") {
		t.Fatalf("--force should overwrite file to v3, got:\n%s", string(data))
	}

	// 7. Fetch second doc, modify both locally, then run `lsy pull` (no args) and answer "a" (overwrite all).
	if _, err := runWithInput("", "fetch", "https://linear.app/example/document/beta-guide-bbb222"); err != nil {
		t.Fatalf("fetch doc-2 failed: %v", err)
	}
	betaPath := filepath.Join(cfgDir, "docs", "beta-guide.md")
	_ = os.WriteFile(alphaPath, []byte("local-alpha"), 0o644)
	_ = os.WriteFile(betaPath, []byte("local-beta"), 0o644)
	docs["doc-1"]["content"] = "# Alpha v4"
	docs["doc-2"]["content"] = "# Beta v2"

	out, err = runWithInput("a\n", "pull")
	if err != nil {
		t.Fatalf("pull all with overwrite-all failed: %v", err)
	}
	if !strings.Contains(out, "Summary: 2 updated, 0 up to date, 0 kept local") {
		t.Fatalf("unexpected pull summary:\n%s", out)
	}
	aBytes, _ := os.ReadFile(alphaPath)
	bBytes, _ := os.ReadFile(betaPath)
	if !strings.Contains(string(aBytes), "# Alpha v4") || !strings.Contains(string(bBytes), "# Beta v2") {
		t.Fatalf("expected both docs overwritten via 'a': alpha=%q beta=%q", string(aBytes), string(bBytes))
	}
}
