package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/frinfo702/linear-doc-sync/internal/markdown"
)

const DefaultEndpoint = "https://api.linear.app/graphql"

// ErrNotFound is returned when a Linear document cannot be found.
var ErrNotFound = errors.New("linear document not found")

// Document represents a Linear document fetched from the GraphQL API.
type Document struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	SlugID    string    `json:"slugId"`
	Content   string    `json:"content"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Client communicates with the Linear GraphQL API.
type Client struct {
	Endpoint   string
	APIKey     string
	HTTPClient *http.Client
}

// NewClient constructs a Linear API client.
func NewClient(apiKey string) *Client {
	return &Client{
		Endpoint: DefaultEndpoint,
		APIKey:   strings.TrimSpace(apiKey),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphQLError struct {
	Message string `json:"message"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []graphQLError  `json:"errors"`
}

func (c *Client) do(ctx context.Context, query string, variables map[string]any, out any) error {
	if c.APIKey == "" {
		return errors.New("Linear API key is not configured (run `lsy config set linear_api_key <key>` or set LINEAR_API_KEY)")
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	payload, err := json.Marshal(graphQLRequest{
		Query:     query,
		Variables: variables,
	})
	if err != nil {
		return fmt.Errorf("marshal graphql request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create linear request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.APIKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("linear api request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read linear api response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("linear api authentication failed (HTTP %d): check your LINEAR_API_KEY", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("linear api error (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var gqlResp graphQLResponse
	if err := json.Unmarshal(body, &gqlResp); err != nil {
		return fmt.Errorf("decode linear graphql response: %w", err)
	}
	if len(gqlResp.Errors) > 0 {
		msgs := make([]string, 0, len(gqlResp.Errors))
		for _, e := range gqlResp.Errors {
			msgs = append(msgs, e.Message)
		}
		joined := strings.Join(msgs, "; ")
		if strings.Contains(strings.ToLower(joined), "not found") || strings.Contains(strings.ToLower(joined), "entity not found") {
			return fmt.Errorf("%w: %s", ErrNotFound, joined)
		}
		return fmt.Errorf("linear graphql error: %s", joined)
	}

	if out != nil && len(gqlResp.Data) > 0 {
		if err := json.Unmarshal(gqlResp.Data, out); err != nil {
			return fmt.Errorf("decode linear graphql data: %w", err)
		}
	}
	return nil
}

const getDocumentByIDQuery = `
query GetDocument($id: String!) {
  document(id: $id) {
    id
    title
    slugId
    content
    url
    createdAt
    updatedAt
  }
}
`

const listDocumentsQuery = `
query ListDocuments($after: String) {
  documents(first: 100, after: $after) {
    nodes {
      id
      title
      slugId
      content
      url
      createdAt
      updatedAt
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}
`

// GetByID fetches a single Linear document by its UUID or slugId.
func (c *Client) GetByID(ctx context.Context, id string) (*Document, error) {
	var data struct {
		Document *Document `json:"document"`
	}
	err := c.do(ctx, getDocumentByIDQuery, map[string]any{"id": id}, &data)
	if err != nil {
		return nil, err
	}
	if data.Document == nil {
		return nil, ErrNotFound
	}
	return data.Document, nil
}

// ParseDocumentTarget extracts candidate lookup identifiers from a Linear URL
// or document name/slug.
type ParsedTarget struct {
	Raw       string
	IsURL     bool
	Segment   string // path segment after /document/
	TailSlug  string // token after last '-' in Segment (Linear's typical slugId position)
	TitleHint string // portion before TailSlug if present, or Segment
}

// ParseTarget parses a Linear document URL or plain identifier/name.
func ParseTarget(input string) ParsedTarget {
	raw := strings.TrimSpace(input)
	pt := ParsedTarget{Raw: raw}

	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		if u, err := url.Parse(raw); err == nil {
			pt.IsURL = true
			path := strings.Trim(u.Path, "/")
			parts := strings.Split(path, "/")
			for i, p := range parts {
				if p == "document" && i+1 < len(parts) {
					pt.Segment = parts[i+1]
					break
				}
			}
			if pt.Segment == "" && len(parts) > 0 {
				pt.Segment = parts[len(parts)-1]
			}
			if idx := strings.LastIndex(pt.Segment, "-"); idx > 0 && idx+1 < len(pt.Segment) {
				pt.TailSlug = pt.Segment[idx+1:]
				pt.TitleHint = pt.Segment[:idx]
			} else {
				pt.TitleHint = pt.Segment
			}
		}
	}
	return pt
}

// ResolveDocument fetches a Linear document given a URL, UUID, slugId, slug, or title.
func (c *Client) ResolveDocument(ctx context.Context, target string) (*Document, error) {
	pt := ParseTarget(target)
	if pt.Raw == "" {
		return nil, errors.New("document URL or name is required")
	}

	// 1. Build direct ID/slugId candidates.
	candidates := make([]string, 0, 3)
	seen := map[string]bool{}
	addCandidate := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			candidates = append(candidates, s)
		}
	}

	if pt.IsURL {
		addCandidate(pt.TailSlug)
		addCandidate(pt.Segment)
	} else {
		addCandidate(pt.Raw)
		if idx := strings.LastIndex(pt.Raw, "-"); idx > 0 && idx+1 < len(pt.Raw) {
			addCandidate(pt.Raw[idx+1:])
		}
	}

	for _, id := range candidates {
		doc, err := c.GetByID(ctx, id)
		if err == nil && doc != nil {
			return doc, nil
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			// Linear sometimes returns generic GraphQL validation errors when an arbitrary
			// title string is passed to document(id:); keep trying fallback lookup unless
			// it was an auth/network failure.
			if strings.Contains(err.Error(), "authentication failed") || strings.Contains(err.Error(), "request failed") {
				return nil, err
			}
		}
	}

	// 2. Fallback: paginate workspace documents and match by URL, slugId, title, or slugified title.
	return c.findInWorkspace(ctx, pt)
}

func (c *Client) findInWorkspace(ctx context.Context, pt ParsedTarget) (*Document, error) {
	rawLower := strings.ToLower(strings.TrimRight(strings.Split(strings.Split(pt.Raw, "?")[0], "#")[0], "/"))
	rawSlug := markdown.Slugify(strings.TrimSuffix(pt.Raw, ".md"))
	segSlug := markdown.Slugify(pt.Segment)
	hintSlug := markdown.Slugify(pt.TitleHint)

	var after any = nil
	for page := 0; page < 20; page++ {
		var data struct {
			Documents struct {
				Nodes    []Document `json:"nodes"`
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"documents"`
		}
		vars := map[string]any{}
		if after != nil {
			vars["after"] = after
		}
		if err := c.do(ctx, listDocumentsQuery, vars, &data); err != nil {
			return nil, err
		}

		for i := range data.Documents.Nodes {
			doc := data.Documents.Nodes[i]
			docURL := strings.ToLower(strings.TrimRight(doc.URL, "/"))
			docTitleSlug := markdown.Slugify(doc.Title)

			if pt.IsURL && docURL != "" && docURL == rawLower {
				return &doc, nil
			}
			if doc.SlugID != "" && (strings.EqualFold(doc.SlugID, pt.Raw) ||
				strings.EqualFold(doc.SlugID, pt.TailSlug) ||
				strings.EqualFold(doc.SlugID, pt.Segment)) {
				return &doc, nil
			}
			if strings.EqualFold(doc.Title, pt.Raw) ||
				docTitleSlug == rawSlug ||
				(pt.Segment != "" && docTitleSlug == segSlug) ||
				(pt.TitleHint != "" && docTitleSlug == hintSlug) {
				return &doc, nil
			}
		}

		if !data.Documents.PageInfo.HasNextPage || data.Documents.PageInfo.EndCursor == "" {
			break
		}
		after = data.Documents.PageInfo.EndCursor
	}

	return nil, fmt.Errorf("%w: %q", ErrNotFound, pt.Raw)
}
