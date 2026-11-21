package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/frinfo702/linear-doc-sync/internal/config"
	"github.com/frinfo702/linear-doc-sync/internal/markdown"
)

const (
	StatusPublished   = "published"
	StatusUnpublished = "unpublished"
)

// ImageAsset tracks a localized image file associated with a document.
type ImageAsset struct {
	OriginalURL  string `json:"original_url,omitempty"`
	RelativePath string `json:"relative_path"`
	LocalPath    string `json:"local_path,omitempty"`
	RemotePath   string `json:"remote_path,omitempty"`
	RemoteSHA    string `json:"remote_sha,omitempty"`
}

// Record represents a tracked Linear document synced to local disk and optionally GitHub.
type Record struct {
	LinearID        string       `json:"linear_id"`
	SlugID          string       `json:"slug_id"`
	Slug            string       `json:"slug"`
	Title           string       `json:"title"`
	LinearURL       string       `json:"linear_url"`
	LocalPath       string       `json:"local_path"`
	LocalHash       string       `json:"local_hash,omitempty"`
	Images          []ImageAsset `json:"images,omitempty"`
	GitHubRepo      string       `json:"github_repo,omitempty"`
	GitHubBranch    string       `json:"github_branch,omitempty"`
	RemotePath      string       `json:"remote_path,omitempty"`
	RemoteSHA       string       `json:"remote_sha,omitempty"`
	Status          string       `json:"status"`
	LinearUpdatedAt time.Time    `json:"linear_updated_at,omitempty"`
	UpdatedAt       time.Time    `json:"updated_at"`
	PublishedAt     time.Time    `json:"published_at,omitempty"`
}

// Store holds all tracked documents.
type Store struct {
	Documents []Record `json:"documents"`
}

// Load reads state.json from ~/.config/lsy/state.json. If it does not exist,
// an empty Store is returned.
func Load() (*Store, error) {
	if _, err := config.EnsureInitialized(); err != nil {
		return nil, err
	}
	p, err := config.StateFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &Store{Documents: []Record{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state %s: %w", p, err)
	}
	var st Store
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, fmt.Errorf("parse state %s: %w", p, err)
		}
	}
	if st.Documents == nil {
		st.Documents = []Record{}
	}
	return &st, nil
}

// Save writes state.json atomically.
func (s *Store) Save() error {
	p, err := config.StateFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	sort.Slice(s.Documents, func(i, j int) bool {
		return s.Documents[i].Slug < s.Documents[j].Slug
	})
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	data = append(data, '\n')

	tmpPath := p + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write temp state %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, p); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("save state %s: %w", p, err)
	}
	return nil
}

// Upsert inserts or replaces a document record matched by LinearID, SlugID, or Slug.
func (s *Store) Upsert(rec Record) {
	for i := range s.Documents {
		cur := s.Documents[i]
		if (rec.LinearID != "" && cur.LinearID == rec.LinearID) ||
			(rec.SlugID != "" && cur.SlugID == rec.SlugID) ||
			(rec.Slug != "" && cur.Slug == rec.Slug) {
			s.Documents[i] = rec
			return
		}
	}
	s.Documents = append(s.Documents, rec)
}

// Find looks up a tracked document by Linear URL, LinearID, SlugID, Slug,
// local filename (with or without .md), remote path, or title.
func (s *Store) Find(query string) (*Record, int) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, -1
	}
	qLower := strings.ToLower(q)
	qCleanURL := strings.TrimRight(strings.Split(strings.Split(qLower, "?")[0], "#")[0], "/")
	qNoExt := strings.TrimSuffix(qLower, ".md")
	qSlug := markdown.Slugify(strings.TrimSuffix(q, ".md"))

	// Extract URL tail if query is a Linear document URL.
	urlSeg, urlTail := extractURLParts(qCleanURL)

	for i := range s.Documents {
		rec := s.Documents[i]
		recURL := strings.TrimRight(strings.ToLower(rec.LinearURL), "/")
		baseName := strings.ToLower(filepath.Base(rec.LocalPath))
		baseNoExt := strings.TrimSuffix(baseName, ".md")

		switch {
		case recURL != "" && recURL == qCleanURL:
			return &s.Documents[i], i
		case rec.LinearID != "" && strings.EqualFold(rec.LinearID, q):
			return &s.Documents[i], i
		case rec.SlugID != "" && (strings.EqualFold(rec.SlugID, q) || strings.EqualFold(rec.SlugID, urlTail) || strings.EqualFold(rec.SlugID, urlSeg)):
			return &s.Documents[i], i
		case rec.Slug != "" && (strings.EqualFold(rec.Slug, qNoExt) || strings.EqualFold(rec.Slug, qSlug) || (urlSeg != "" && strings.EqualFold(rec.Slug, urlSeg))):
			return &s.Documents[i], i
		case baseName != "" && (baseName == qLower || baseNoExt == qNoExt):
			return &s.Documents[i], i
		case rec.RemotePath != "" && strings.EqualFold(rec.RemotePath, q):
			return &s.Documents[i], i
		case rec.Title != "" && (strings.EqualFold(rec.Title, q) || markdown.Slugify(rec.Title) == qSlug):
			return &s.Documents[i], i
		}
	}
	return nil, -1
}

// RemoveAt deletes the record at index idx.
func (s *Store) RemoveAt(idx int) {
	if idx < 0 || idx >= len(s.Documents) {
		return
	}
	s.Documents = append(s.Documents[:idx], s.Documents[idx+1:]...)
}

func extractURLParts(cleanLowerURL string) (segment string, tail string) {
	idx := strings.Index(cleanLowerURL, "/document/")
	if idx == -1 {
		return "", ""
	}
	rest := cleanLowerURL[idx+len("/document/"):]
	rest = strings.Trim(rest, "/")
	if slash := strings.Index(rest, "/"); slash != -1 {
		rest = rest[:slash]
	}
	segment = rest
	if dash := strings.LastIndex(rest, "-"); dash != -1 && dash+1 < len(rest) {
		tail = rest[dash+1:]
	}
	return segment, tail
}
