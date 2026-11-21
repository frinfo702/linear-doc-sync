package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestParseTarget(t *testing.T) {
	pt := ParseTarget("https://linear.app/example-project/document/my-cool-doc-a1b2c3d4e5f6?foo=bar")
	if !pt.IsURL {
		t.Fatalf("expected IsURL=true")
	}
	if pt.Segment != "my-cool-doc-a1b2c3d4e5f6" {
		t.Fatalf("unexpected segment: %q", pt.Segment)
	}
	if pt.TailSlug != "a1b2c3d4e5f6" {
		t.Fatalf("unexpected tail slug: %q", pt.TailSlug)
	}
	if pt.TitleHint != "my-cool-doc" {
		t.Fatalf("unexpected title hint: %q", pt.TitleHint)
	}
}

func TestResolveDocumentByURLAndTitleFallback(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "lin_api_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req graphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(req.Query, "GetDocument") {
			id, _ := req.Variables["id"].(string)
			if id == "a1b2c3d4e5f6" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": map[string]any{
						"document": map[string]any{
							"id":      "uuid-1",
							"title":   "My Cool Doc",
							"slugId":  "a1b2c3d4e5f6",
							"content": "# Hello from Linear",
							"url":     "https://linear.app/example-project/document/my-cool-doc-a1b2c3d4e5f6",
						},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errors": []map[string]string{{"message": "Entity not found"}},
			})
			return
		}

		// ListDocuments fallback
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"documents": map[string]any{
					"nodes": []map[string]any{
						{
							"id":      "uuid-2",
							"title":   "Architecture Guide",
							"slugId":  "998877665544",
							"content": "# Architecture",
							"url":     "https://linear.app/example-project/document/architecture-guide-998877665544",
						},
					},
					"pageInfo": map[string]any{
						"hasNextPage": false,
						"endCursor":   "",
					},
				},
			},
		})
	})

	client := NewClient("lin_api_test")
	client.Endpoint = "https://linear.test/graphql"
	client.HTTPClient = newTestHTTPClient(handler)

	// 1. Direct URL resolution via slugId tail
	doc, err := client.ResolveDocument(context.Background(), "https://linear.app/example-project/document/my-cool-doc-a1b2c3d4e5f6")
	if err != nil {
		t.Fatalf("ResolveDocument by URL failed: %v", err)
	}
	if doc.ID != "uuid-1" || doc.Title != "My Cool Doc" {
		t.Fatalf("unexpected doc: %+v", doc)
	}

	// 2. Fallback resolution by slugified title / document name
	doc2, err := client.ResolveDocument(context.Background(), "architecture-guide")
	if err != nil {
		t.Fatalf("ResolveDocument by slug name failed: %v", err)
	}
	if doc2.ID != "uuid-2" || doc2.Title != "Architecture Guide" {
		t.Fatalf("unexpected doc2: %+v", doc2)
	}
}
