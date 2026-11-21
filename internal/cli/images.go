package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/frinfo702/linear-doc-sync/internal/github"
	"github.com/frinfo702/linear-doc-sync/internal/linear"
	"github.com/frinfo702/linear-doc-sync/internal/markdown"
	"github.com/frinfo702/linear-doc-sync/internal/state"
)

const signedURLTTLSeconds = 300

type downloadedImage struct {
	Asset state.ImageAsset
	Data  []byte
}

func resolveSlugAndFilename(doc *linear.Document, existingRec *state.Record, filenameFlag string) (slug string, fileName string) {
	slug = markdown.Slugify(doc.Title)
	if existingRec != nil && existingRec.Slug != "" {
		slug = existingRec.Slug
	}
	if slug == "document" && doc.SlugID != "" {
		slug = strings.ToLower(doc.SlugID)
	}

	fileName = slug + ".md"
	if strings.TrimSpace(filenameFlag) != "" {
		fileName = filepath.Base(strings.TrimSpace(filenameFlag))
		if !strings.HasSuffix(strings.ToLower(fileName), ".md") {
			fileName += ".md"
		}
		slug = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	} else if existingRec != nil && existingRec.LocalPath != "" {
		fileName = filepath.Base(existingRec.LocalPath)
	}
	return slug, fileName
}

// localizeDocumentImages downloads all remote image URLs referenced in docContent
// into memory and rewrites their Markdown/HTML links to relative local paths
// (`./images/<slug>/<filename>`). No files are written to disk by this function.
func localizeDocumentImages(ctx context.Context, client *linear.Client, docContent, slug, docsDir string) (string, []downloadedImage, error) {
	urlToRel := map[string]string{}
	relSeen := map[string]bool{}
	var downloaded []downloadedImage

	rewritten, err := markdown.RewriteImages(docContent, func(rawURL string) (string, bool, error) {
		if rel, ok := urlToRel[rawURL]; ok {
			return "./" + rel, true, nil
		}

		data, contentType, err := client.DownloadAsset(ctx, rawURL)
		if err != nil {
			return "", false, err
		}

		imgName := markdown.ImageFilename(rawURL, contentType, data)
		relPath := path.Join("images", slug, imgName)
		localPath := filepath.Join(docsDir, "images", slug, imgName)

		urlToRel[rawURL] = relPath
		if !relSeen[relPath] {
			relSeen[relPath] = true
			downloaded = append(downloaded, downloadedImage{
				Asset: state.ImageAsset{
					OriginalURL:  stripURLSignature(rawURL),
					RelativePath: relPath,
					LocalPath:    localPath,
				},
				Data: data,
			})
		}

		return "./" + relPath, true, nil
	})
	if err != nil {
		return "", nil, err
	}
	return rewritten, downloaded, nil
}

func writeLocalImages(images []downloadedImage) error {
	for _, img := range images {
		if err := os.MkdirAll(filepath.Dir(img.Asset.LocalPath), 0o755); err != nil {
			return fmt.Errorf("create image directory %s: %w", filepath.Dir(img.Asset.LocalPath), err)
		}
		if err := os.WriteFile(img.Asset.LocalPath, img.Data, 0o644); err != nil {
			return fmt.Errorf("write local image %s: %w", img.Asset.LocalPath, err)
		}
	}
	return nil
}

// cleanupStaleLocalImages deletes previously tracked local image files that are
// no longer present in currentLocalPaths, and removes their parent directory if empty.
func cleanupStaleLocalImages(existing []state.ImageAsset, current []downloadedImage) {
	keep := make(map[string]bool, len(current))
	for _, img := range current {
		if img.Asset.LocalPath != "" {
			keep[img.Asset.LocalPath] = true
		}
	}
	dirs := map[string]bool{}
	for _, old := range existing {
		if old.LocalPath == "" || keep[old.LocalPath] {
			continue
		}
		_ = os.Remove(old.LocalPath)
		dirs[filepath.Dir(old.LocalPath)] = true
	}
	for dir := range dirs {
		_ = os.Remove(dir) // Only succeeds if directory is empty.
	}
}

// mergeLocalImageState combines newly fetched local images with existing tracked
// remote metadata so remote GitHub paths are preserved until the next publish/unpublish.
func mergeLocalImageState(existing []state.ImageAsset, current []downloadedImage) []state.ImageAsset {
	existingByRel := make(map[string]state.ImageAsset, len(existing))
	for _, old := range existing {
		existingByRel[old.RelativePath] = old
	}

	result := make([]state.ImageAsset, 0, len(current))
	seenRel := make(map[string]bool, len(current))

	for _, img := range current {
		asset := img.Asset
		if prev, ok := existingByRel[asset.RelativePath]; ok {
			asset.RemotePath = prev.RemotePath
			asset.RemoteSHA = prev.RemoteSHA
		}
		result = append(result, asset)
		seenRel[asset.RelativePath] = true
	}

	// Retain previously published remote-only images so a later publish/unpublish can delete them.
	for _, old := range existing {
		if seenRel[old.RelativePath] {
			continue
		}
		if old.RemotePath != "" {
			old.LocalPath = ""
			result = append(result, old)
		}
	}
	return result
}

// deleteRemoteImages deletes any tracked remote image files from GitHub, ignoring 404s.
func deleteRemoteImages(ctx context.Context, ghClient *github.Client, repo, branch, message string, images []state.ImageAsset) error {
	for _, img := range images {
		if img.RemotePath == "" {
			continue
		}
		if err := ghClient.DeleteFile(ctx, repo, branch, img.RemotePath, "", message); err != nil {
			if errors.Is(err, github.ErrNotFound) {
				continue
			}
			return fmt.Errorf("delete remote image %s: %w", img.RemotePath, err)
		}
	}
	return nil
}

// removeLocalImageFiles deletes all local image files for a document and prunes
// empty parent directories.
func removeLocalImageFiles(images []state.ImageAsset) {
	dirs := map[string]bool{}
	for _, img := range images {
		if img.LocalPath == "" {
			continue
		}
		_ = os.Remove(img.LocalPath)
		dirs[filepath.Dir(img.LocalPath)] = true
	}
	for dir := range dirs {
		_ = os.Remove(dir)
	}
}

func stripURLSignature(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	if !q.Has("signature") && !q.Has("expires") {
		return rawURL
	}
	q.Del("signature")
	q.Del("expires")
	u.RawQuery = q.Encode()
	return u.String()
}

func mergePublishedWithExisting(existing []state.ImageAsset, published []state.ImageAsset) []state.ImageAsset {
	byRel := make(map[string]state.ImageAsset, len(published))
	for _, p := range published {
		byRel[p.RelativePath] = p
	}
	merged := make([]state.ImageAsset, 0, len(existing))
	seen := make(map[string]bool, len(existing))
	for _, e := range existing {
		if p, ok := byRel[e.RelativePath]; ok {
			merged = append(merged, p)
		} else {
			merged = append(merged, e)
		}
		seen[e.RelativePath] = true
	}
	for _, p := range published {
		if !seen[p.RelativePath] {
			merged = append(merged, p)
		}
	}
	return merged
}
