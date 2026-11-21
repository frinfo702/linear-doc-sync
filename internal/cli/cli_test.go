package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frinfo702/linear-doc-sync/internal/github"
	"github.com/frinfo702/linear-doc-sync/internal/linear"
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
					"url":     "https://linear.app/uyuthropic/document/launch-plan-abc123456789",
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
				"path": strings.TrimPrefix(p, "/repos/frinfo702/web/contents/"),
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
			rel := strings.TrimPrefix(p, "/repos/frinfo702/web/contents/")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": map[string]string{
					"path":     rel,
					"sha":      "blob-sha-1",
					"html_url": "https://github.com/frinfo702/web/blob/main/" + rel,
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
	if _, err := run("config", "set", "github_repo", "frinfo702/web"); err != nil {
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
	out, err := run("publish", "https://linear.app/uyuthropic/document/launch-plan-abc123456789", "--repo-path", "blog/posts")
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if !strings.Contains(out, "blog/posts/launch-plan.md") {
		t.Fatalf("expected remote path blog/posts/launch-plan.md in output, got:\n%s", out)
	}
	if _, ok := remoteFiles["/repos/frinfo702/web/contents/blog/posts/launch-plan.md"]; !ok {
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
