package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/frinfo702/linear-doc-sync/internal/config"
	"github.com/frinfo702/linear-doc-sync/internal/markdown"
	"github.com/frinfo702/linear-doc-sync/internal/state"
)

func (a *App) runPublish(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)

	var (
		repoFlag          string
		branchFlag        string
		repoPathFlag      string
		filenameFlag      string
		messageFlag       string
		noFrontmatterFlag bool
		localOnlyFlag     bool
		withImageFlag     bool
	)

	fs.StringVar(&repoFlag, "repo", "", "Target GitHub repository in owner/repo format (overrides config)")
	fs.StringVar(&branchFlag, "branch", "", "Target GitHub branch (overrides config)")
	fs.StringVar(&repoPathFlag, "repo-path", "", "Directory path inside the GitHub repository (overrides config)")
	fs.StringVar(&filenameFlag, "filename", "", "Custom output Markdown filename (default: <slug>.md)")
	fs.StringVar(&messageFlag, "message", "", "Custom Git commit message")
	fs.BoolVar(&noFrontmatterFlag, "no-frontmatter", false, "Do not prepend YAML frontmatter to Markdown")
	fs.BoolVar(&localOnlyFlag, "local-only", false, "Only fetch and save locally without pushing to GitHub")
	fs.BoolVar(&withImageFlag, "with-image", false, "Download images locally and upload them to GitHub (overrides config)")
	fs.Usage = func() {
		fmt.Fprint(a.Stdout, `Usage:
  lsy publish <url-or-name> [flags]

Fetch a Linear document, save a local Markdown clone in ~/.config/lsy/docs,
and publish it to the configured GitHub repository.

Flags:
  --repo <owner/repo>   Override configured GitHub repository
  --branch <branch>     Override configured GitHub branch
  --repo-path <dir>     Override default directory path in GitHub (e.g. content/docs)
  --filename <name>     Override Markdown filename (default: <slug>.md)
  --message <msg>       Custom Git commit message
  --no-frontmatter      Omit YAML frontmatter from generated Markdown
  --with-image          Download images locally and push them to GitHub (overrides config)
  --local-only          Save locally without pushing to GitHub
  -h, --help            Show help
`)
	}

	posArgs, err := parseInterspersedFlags(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if len(posArgs) != 1 {
		return errors.New("usage: lsy publish <url-or-name> [flags]")
	}
	target := posArgs[0]

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, err := state.Load()
	if err != nil {
		return err
	}

	// Determine whether --repo-path or --with-image was explicitly passed.
	repoPathProvided := false
	withImage := cfg.WithImage
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "repo-path":
			repoPathProvided = true
		case "with-image":
			withImage = withImageFlag
		}
	})

	// If target matches an existing tracked record, prefer its Linear ID for reliable re-sync.
	existingRec, _ := st.Find(target)
	lookupTarget := target
	if existingRec != nil && existingRec.LinearID != "" {
		lookupTarget = existingRec.LinearID
	}

	linearClient := a.NewLinearClient(cfg.LinearAPIKey)
	if withImage {
		linearClient.SignedFileURLTTL = signedURLTTLSeconds
	}
	doc, err := linearClient.ResolveDocument(ctx, lookupTarget)
	if err != nil {
		return err
	}

	if existingRec == nil {
		if found, _ := st.Find(doc.ID); found != nil {
			existingRec = found
		}
	}

	slug, fileName := resolveSlugAndFilename(doc, existingRec, filenameFlag)

	docsDir, err := cfg.EffectiveDocsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		return fmt.Errorf("create local docs directory %s: %w", docsDir, err)
	}

	content := doc.Content
	var downloaded []downloadedImage
	if withImage {
		content, downloaded, err = localizeDocumentImages(ctx, linearClient, doc.Content, slug, docsDir)
		if err != nil {
			return err
		}
		if err := writeLocalImages(downloaded); err != nil {
			return err
		}
	}

	var prevImages []state.ImageAsset
	if existingRec != nil {
		prevImages = existingRec.Images
		cleanupStaleLocalImages(prevImages, downloaded)
	}

	useFrontmatter := cfg.UseFrontmatter() && !noFrontmatterFlag
	updatedAt := doc.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	rendered := markdown.Render(markdown.Metadata{
		Title:     doc.Title,
		LinearID:  doc.ID,
		SlugID:    doc.SlugID,
		Slug:      slug,
		LinearURL: doc.URL,
		UpdatedAt: updatedAt,
	}, content, useFrontmatter)

	renderedBytes := []byte(rendered)
	localPath := filepath.Join(docsDir, fileName)
	if err := os.WriteFile(localPath, renderedBytes, 0o644); err != nil {
		return fmt.Errorf("write local markdown %s: %w", localPath, err)
	}

	now := time.Now().UTC()
	rec := state.Record{
		LinearID:        doc.ID,
		SlugID:          doc.SlugID,
		Slug:            slug,
		Title:           doc.Title,
		LinearURL:       doc.URL,
		LocalPath:       localPath,
		LocalHash:       markdown.ContentHash(renderedBytes),
		Images:          mergeLocalImageState(prevImages, downloaded),
		Status:          state.StatusUnpublished,
		LinearUpdatedAt: doc.UpdatedAt,
		UpdatedAt:       now,
	}
	if existingRec != nil {
		rec.GitHubRepo = existingRec.GitHubRepo
		rec.GitHubBranch = existingRec.GitHubBranch
		rec.RemotePath = existingRec.RemotePath
		rec.RemoteSHA = existingRec.RemoteSHA
		rec.Status = existingRec.Status
		rec.PublishedAt = existingRec.PublishedAt
	}

	if localOnlyFlag {
		st.Upsert(rec)
		if err := st.Save(); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "Saved locally: %s\n", localPath)
		if len(downloaded) > 0 {
			fmt.Fprintf(a.Stdout, "  Images: %d saved locally\n", len(downloaded))
		}
		return nil
	}

	repo := strings.TrimSpace(repoFlag)
	if repo == "" {
		repo = strings.TrimSpace(cfg.GitHubRepo)
	}
	if repo == "" && existingRec != nil {
		repo = existingRec.GitHubRepo
	}
	if repo == "" {
		return errors.New("GitHub repository is not configured (run `lsy config set github_repo owner/repo` or pass --repo)")
	}

	branch := strings.TrimSpace(branchFlag)
	if branch == "" {
		branch = strings.TrimSpace(cfg.GitHubBranch)
	}
	if branch == "" {
		branch = config.DefaultBranch
	}

	repoPath := cfg.RepoPath
	if repoPathProvided {
		repoPath = config.NormalizeRepoPath(repoPathFlag)
	}

	remotePath := fileName
	if repoPath != "" {
		remotePath = path.Join(repoPath, fileName)
	}

	commitMsg := strings.TrimSpace(messageFlag)
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("docs(lsy): publish %s", doc.Title)
	}

	ghClient := a.NewGitHubClient(cfg.GitHubToken)

	// Upload new/changed image assets to GitHub so relative Markdown image links render on GitHub.
	publishedAssets := make([]state.ImageAsset, 0, len(downloaded))
	uploadedRemotePaths := make(map[string]bool, len(downloaded))
	imagesUploaded := 0
	for _, img := range downloaded {
		imgRemotePath := img.Asset.RelativePath
		if repoPath != "" {
			imgRemotePath = path.Join(repoPath, img.Asset.RelativePath)
		}
		imgMsg := strings.TrimSpace(messageFlag)
		if imgMsg == "" {
			imgMsg = fmt.Sprintf("docs(lsy): publish image %s for %s", path.Base(img.Asset.RelativePath), doc.Title)
		}
		imgRes, err := ghClient.UpsertFileIfChanged(ctx, repo, branch, imgRemotePath, imgMsg, img.Data)
		if err != nil {
			// Persist local clone & any already-uploaded images in state before returning error.
			rec.Images = mergePublishedWithExisting(rec.Images, publishedAssets)
			st.Upsert(rec)
			_ = st.Save()
			return fmt.Errorf("publish image %s to github failed (local copy saved at %s): %w", imgRemotePath, localPath, err)
		}
		if !imgRes.Unchanged {
			imagesUploaded++
		}
		asset := img.Asset
		asset.RemotePath = imgRemotePath
		asset.RemoteSHA = imgRes.SHA
		publishedAssets = append(publishedAssets, asset)
		uploadedRemotePaths[imgRemotePath] = true
	}

	// Keep track of uploaded images even if the Markdown upload fails.
	rec.Images = mergePublishedWithExisting(rec.Images, publishedAssets)

	res, err := ghClient.UpsertFile(ctx, repo, branch, remotePath, commitMsg, renderedBytes)
	if err != nil {
		// Persist the local clone in state even if the GitHub push fails.
		st.Upsert(rec)
		_ = st.Save()
		return fmt.Errorf("publish to github failed (local copy saved at %s): %w", localPath, err)
	}

	// Delete previously published remote images that are no longer referenced by this document.
	if len(prevImages) > 0 {
		staleRepo := repo
		staleBranch := branch
		if existingRec != nil && existingRec.GitHubRepo != "" {
			staleRepo = existingRec.GitHubRepo
		}
		if existingRec != nil && existingRec.GitHubBranch != "" {
			staleBranch = existingRec.GitHubBranch
		}
		var staleRemote []state.ImageAsset
		for _, old := range prevImages {
			if old.RemotePath == "" {
				continue
			}
			if staleRepo == repo && staleBranch == branch && uploadedRemotePaths[old.RemotePath] {
				continue
			}
			staleRemote = append(staleRemote, old)
		}
		if len(staleRemote) > 0 {
			cleanupMsg := fmt.Sprintf("docs(lsy): remove unused images for %s", doc.Title)
			if err := deleteRemoteImages(ctx, ghClient, staleRepo, staleBranch, cleanupMsg, staleRemote); err != nil {
				// Non-fatal: keep stale entries in state so they can be retried later.
				fmt.Fprintf(a.Stderr, "Warning: failed to remove stale remote images: %v\n", err)
				for _, s := range staleRemote {
					s.LocalPath = ""
					publishedAssets = append(publishedAssets, s)
				}
			}
		}
	}

	rec.GitHubRepo = repo
	rec.GitHubBranch = branch
	rec.RemotePath = remotePath
	rec.RemoteSHA = res.SHA
	rec.Images = publishedAssets
	rec.Status = state.StatusPublished
	rec.PublishedAt = now
	rec.UpdatedAt = now

	st.Upsert(rec)
	if err := st.Save(); err != nil {
		return err
	}

	action := "Updated"
	if res.Created {
		action = "Published"
	}
	fmt.Fprintf(a.Stdout, "%s %q\n", action, doc.Title)
	fmt.Fprintf(a.Stdout, "  Local:  %s\n", localPath)
	fmt.Fprintf(a.Stdout, "  GitHub: %s (%s:%s)\n", remotePath, repo, branch)
	if len(downloaded) > 0 {
		fmt.Fprintf(a.Stdout, "  Images: %d total (%d uploaded)\n", len(downloaded), imagesUploaded)
	}
	if res.HTMLURL != "" {
		fmt.Fprintf(a.Stdout, "  URL:    %s\n", res.HTMLURL)
	}
	return nil
}
