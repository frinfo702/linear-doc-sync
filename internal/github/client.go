package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultAPIBase = "https://api.github.com"

// ErrNotFound is returned when a target file does not exist in the repository.
var ErrNotFound = errors.New("github file not found")

// Client interacts with the GitHub Contents REST API.
type Client struct {
	APIBase    string
	Token      string
	HTTPClient *http.Client
}

// NewClient constructs a GitHub API client.
func NewClient(token string) *Client {
	return &Client{
		APIBase: DefaultAPIBase,
		Token:   strings.TrimSpace(token),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// FileInfo describes a file in a GitHub repository.
type FileInfo struct {
	Path    string `json:"path"`
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
}

// UpsertResult describes the result of creating or updating a file in GitHub.
type UpsertResult struct {
	Path      string
	SHA       string
	HTMLURL   string
	CommitSHA string
	Created   bool
}

func (c *Client) contentsURL(repo, filePath string) (string, error) {
	parts := strings.Split(strings.TrimSpace(repo), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("invalid github repository %q: expected owner/repo", repo)
	}
	owner := url.PathEscape(parts[0])
	name := url.PathEscape(parts[1])

	cleanPath := strings.Trim(strings.ReplaceAll(filePath, "\\", "/"), "/")
	if cleanPath == "" {
		return "", errors.New("github file path cannot be empty")
	}
	pathParts := strings.Split(cleanPath, "/")
	for i, seg := range pathParts {
		pathParts[i] = url.PathEscape(seg)
	}

	base := strings.TrimRight(c.APIBase, "/")
	if base == "" {
		base = DefaultAPIBase
	}
	return fmt.Sprintf("%s/repos/%s/%s/contents/%s", base, owner, name, strings.Join(pathParts, "/")), nil
}

func (c *Client) do(ctx context.Context, method, endpoint string, body any, out any) (int, error) {
	if c.Token == "" {
		return 0, errors.New("GitHub token is not configured (run `lsy config set github_token <token>` or set GITHUB_TOKEN)")
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("marshal github request: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, fmt.Errorf("create github request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "lsy-cli")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("github api request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("read github response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, ErrNotFound
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return resp.StatusCode, fmt.Errorf("github api authentication/permission error (HTTP %d): %s", resp.StatusCode, extractGHMessage(respBytes))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("github api error (HTTP %d): %s", resp.StatusCode, extractGHMessage(respBytes))
	}

	if out != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode github response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

func extractGHMessage(body []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Message != "" {
		return e.Message
	}
	return strings.TrimSpace(string(body))
}

// GetFile retrieves metadata (including blob SHA) for a file on a branch.
func (c *Client) GetFile(ctx context.Context, repo, branch, filePath string) (*FileInfo, error) {
	u, err := c.contentsURL(repo, filePath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(branch) != "" {
		u += "?ref=" + url.QueryEscape(strings.TrimSpace(branch))
	}
	var info FileInfo
	_, err = c.do(ctx, http.MethodGet, u, nil, &info)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// UpsertFile creates or updates a file at filePath in repo on branch.
func (c *Client) UpsertFile(ctx context.Context, repo, branch, filePath, message string, content []byte) (*UpsertResult, error) {
	u, err := c.contentsURL(repo, filePath)
	if err != nil {
		return nil, err
	}

	existingSHA := ""
	existing, err := c.GetFile(ctx, repo, branch, filePath)
	if err == nil && existing != nil {
		existingSHA = existing.SHA
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	payload := map[string]any{
		"message": message,
		"content": base64.StdEncoding.EncodeToString(content),
	}
	if strings.TrimSpace(branch) != "" {
		payload["branch"] = strings.TrimSpace(branch)
	}
	if existingSHA != "" {
		payload["sha"] = existingSHA
	}

	var resp struct {
		Content struct {
			Path    string `json:"path"`
			SHA     string `json:"sha"`
			HTMLURL string `json:"html_url"`
		} `json:"content"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}

	status, err := c.do(ctx, http.MethodPut, u, payload, &resp)
	if err != nil {
		return nil, err
	}
	return &UpsertResult{
		Path:      resp.Content.Path,
		SHA:       resp.Content.SHA,
		HTMLURL:   resp.Content.HTMLURL,
		CommitSHA: resp.Commit.SHA,
		Created:   status == http.StatusCreated,
	}, nil
}

// DeleteFile deletes a file at filePath in repo on branch.
func (c *Client) DeleteFile(ctx context.Context, repo, branch, filePath, sha, message string) error {
	u, err := c.contentsURL(repo, filePath)
	if err != nil {
		return err
	}

	if strings.TrimSpace(sha) == "" {
		info, err := c.GetFile(ctx, repo, branch, filePath)
		if err != nil {
			return err
		}
		sha = info.SHA
	}

	payload := map[string]any{
		"message": message,
		"sha":     sha,
	}
	if strings.TrimSpace(branch) != "" {
		payload["branch"] = strings.TrimSpace(branch)
	}

	_, err = c.do(ctx, http.MethodDelete, u, payload, nil)
	return err
}
