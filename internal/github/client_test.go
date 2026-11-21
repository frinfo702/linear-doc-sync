package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestUpsertAndDeleteFile(t *testing.T) {
	files := map[string]struct {
		content string
		sha     string
	}{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gh_test_token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		key := r.URL.Path
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			f, ok := files[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
				return
			}
			_ = json.NewEncoder(w).Encode(FileInfo{
				Path:    "docs/hello.md",
				SHA:     f.sha,
				HTMLURL: "https://github.com/owner/repo/blob/main/docs/hello.md",
			})
		case http.MethodPut:
			var body struct {
				Message string `json:"message"`
				Content string `json:"content"`
				SHA     string `json:"sha"`
				Branch  string `json:"branch"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode put body: %v", err)
			}
			decoded, err := base64.StdEncoding.DecodeString(body.Content)
			if err != nil {
				t.Fatalf("base64 decode: %v", err)
			}
			_, existed := files[key]
			newSHA := "sha-v1"
			status := http.StatusCreated
			if existed {
				if body.SHA != files[key].sha {
					w.WriteHeader(http.StatusConflict)
					_ = json.NewEncoder(w).Encode(map[string]string{"message": "SHA mismatch"})
					return
				}
				newSHA = "sha-v2"
				status = http.StatusOK
			}
			files[key] = struct {
				content string
				sha     string
			}{content: string(decoded), sha: newSHA}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": map[string]string{
					"path":     "docs/hello.md",
					"sha":      newSHA,
					"html_url": "https://github.com/owner/repo/blob/main/docs/hello.md",
				},
				"commit": map[string]string{"sha": "commit-1"},
			})
		case http.MethodDelete:
			var body struct {
				SHA string `json:"sha"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode delete body: %v", err)
			}
			f, ok := files[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if body.SHA != f.sha {
				w.WriteHeader(http.StatusConflict)
				return
			}
			delete(files, key)
			w.WriteHeader(http.StatusOK)
		}
	})

	client := NewClient("gh_test_token")
	client.APIBase = "https://github.test"
	client.HTTPClient = newTestHTTPClient(handler)

	// 1. Create
	res1, err := client.UpsertFile(context.Background(), "owner/repo", "main", "docs/hello.md", "publish", []byte("# Hello"))
	if err != nil {
		t.Fatalf("create UpsertFile failed: %v", err)
	}
	if !res1.Created || res1.SHA != "sha-v1" {
		t.Fatalf("unexpected create result: %+v", res1)
	}

	// 2. Update
	res2, err := client.UpsertFile(context.Background(), "owner/repo", "main", "docs/hello.md", "update", []byte("# Hello v2"))
	if err != nil {
		t.Fatalf("update UpsertFile failed: %v", err)
	}
	if res2.Created || res2.SHA != "sha-v2" {
		t.Fatalf("unexpected update result: %+v", res2)
	}

	// 3. Delete
	if err := client.DeleteFile(context.Background(), "owner/repo", "main", "docs/hello.md", "", "unpublish"); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected file to be deleted")
	}
}

func TestUpsertFileIfChangedSkipsIdenticalBlob(t *testing.T) {
	putCount := 0
	content := []byte("binary-image-data")
	expectedSHA := GitBlobSHA(content)
	remoteSHA := ""

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			if remoteSHA == "" {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
				return
			}
			_ = json.NewEncoder(w).Encode(FileInfo{
				Path: "docs/images/doc/img.png",
				SHA:  remoteSHA,
			})
		case http.MethodPut:
			putCount++
			remoteSHA = expectedSHA
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": map[string]string{
					"path": "docs/images/doc/img.png",
					"sha":  expectedSHA,
				},
				"commit": map[string]string{"sha": "c1"},
			})
		}
	})

	client := NewClient("gh_test")
	client.APIBase = "https://github.test"
	client.HTTPClient = newTestHTTPClient(handler)

	res1, err := client.UpsertFileIfChanged(context.Background(), "owner/repo", "main", "docs/images/doc/img.png", "add image", content)
	if err != nil {
		t.Fatalf("first UpsertFileIfChanged failed: %v", err)
	}
	if res1.Unchanged || !res1.Created || putCount != 1 {
		t.Fatalf("expected upload on first call: res=%+v putCount=%d", res1, putCount)
	}

	res2, err := client.UpsertFileIfChanged(context.Background(), "owner/repo", "main", "docs/images/doc/img.png", "add image", content)
	if err != nil {
		t.Fatalf("second UpsertFileIfChanged failed: %v", err)
	}
	if !res2.Unchanged || putCount != 1 {
		t.Fatalf("expected second call to skip PUT: res=%+v putCount=%d", res2, putCount)
	}
}
