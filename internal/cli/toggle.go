package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/frinfo702/linear-doc-sync/internal/config"
	"github.com/frinfo702/linear-doc-sync/internal/github"
	"github.com/frinfo702/linear-doc-sync/internal/markdown"
	"github.com/frinfo702/linear-doc-sync/internal/state"
)

type toggleOptions struct {
	repoFlag          string
	branchFlag        string
	repoPathFlag      string
	repoPathProvided  bool
	messageFlag       string
	fetchFlag         bool
	withImage         bool
	withImageProvided bool
	noFrontmatterFlag bool
	dryRunFlag        bool
	publishedOnly     bool
	unpublishedOnly   bool
}

func (a *App) runToggle(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("toggle", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)

	var opts toggleOptions
	fs.StringVar(&opts.repoFlag, "repo", "", "Override GitHub repository (owner/repo)")
	fs.StringVar(&opts.branchFlag, "branch", "", "Override GitHub branch")
	fs.StringVar(&opts.repoPathFlag, "repo-path", "", "Override GitHub directory path")
	fs.StringVar(&opts.messageFlag, "message", "", "Custom Git commit message")
	fs.BoolVar(&opts.fetchFlag, "fetch", false, "Refresh document from Linear before publishing")
	fs.BoolVar(&opts.withImage, "with-image", false, "Download images locally when used with --fetch (overrides config)")
	fs.BoolVar(&opts.noFrontmatterFlag, "no-frontmatter", false, "Omit YAML frontmatter when used with --fetch")
	fs.BoolVar(&opts.dryRunFlag, "dry-run", false, "Show planned changes after confirmation without modifying GitHub or state")
	fs.BoolVar(&opts.publishedOnly, "published", false, "Show only currently published documents")
	fs.BoolVar(&opts.unpublishedOnly, "unpublished", false, "Show only currently private (unpublished) documents")
	fs.Usage = func() {
		fmt.Fprint(a.Stdout, `Usage:
  lsy toggle [flags]

Interactively switch tracked documents between published ([x]) and private ([ ]).
Use arrow keys (or j/k) to navigate, Space to toggle, and Enter to apply changes.

Aliases:
  toggle, switch

Flags:
  --repo <owner/repo>   Override GitHub repository
  --branch <branch>     Override GitHub branch
  --repo-path <dir>     Override GitHub directory path
  --message <msg>       Custom Git commit message
  --fetch               Refresh from Linear before publishing newly enabled documents
  --with-image          Download images locally when --fetch is enabled (overrides config)
  --no-frontmatter      Omit YAML frontmatter when --fetch is enabled
  --published           Show only currently published documents
  --unpublished         Show only currently private (unpublished) documents
  --dry-run             Preview changes after confirmation without modifying GitHub or state
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
	if len(posArgs) > 0 {
		return errors.New("usage: lsy toggle [flags]")
	}
	if opts.publishedOnly && opts.unpublishedOnly {
		return errors.New("--published and --unpublished cannot be combined")
	}

	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "repo-path":
			opts.repoPathProvided = true
		case "with-image":
			opts.withImageProvided = true
		}
	})

	return a.runInteractiveToggle(ctx, opts)
}

func (a *App) runInteractiveToggle(ctx context.Context, opts toggleOptions) error {
	st, err := state.Load()
	if err != nil {
		return err
	}

	// Sort deterministically by slug so display order matches state.Save().
	sort.Slice(st.Documents, func(i, j int) bool {
		return st.Documents[i].Slug < st.Documents[j].Slug
	})

	var candidates []state.Record
	for _, doc := range st.Documents {
		if opts.publishedOnly && doc.Status != state.StatusPublished {
			continue
		}
		if opts.unpublishedOnly && doc.Status != state.StatusUnpublished {
			continue
		}
		candidates = append(candidates, doc)
	}

	if len(candidates) == 0 {
		fmt.Fprintln(a.Stdout, "No tracked documents found. Fetch or publish a document first (e.g. `lsy fetch <url-or-name>`).")
		return nil
	}

	items := make([]pickerItem, len(candidates))
	for i, doc := range candidates {
		pub := doc.Status == state.StatusPublished
		items[i] = pickerItem{
			Title:    doc.Title,
			Slug:     doc.Slug,
			Initial:  pub,
			Selected: pub,
		}
	}

	confirmed, err := runCheckboxPicker(a.Stdin, a.Stdout, items)
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Fprintln(a.Stdout, "Cancelled. No changes made.")
		return nil
	}

	var toPublish []state.Record
	var toUnpublish []state.Record
	for i, it := range items {
		if !it.Initial && it.Selected {
			toPublish = append(toPublish, candidates[i])
		} else if it.Initial && !it.Selected {
			toUnpublish = append(toUnpublish, candidates[i])
		}
	}

	if len(toPublish) == 0 && len(toUnpublish) == 0 {
		fmt.Fprintln(a.Stdout, "No changes made.")
		return nil
	}

	if opts.dryRunFlag {
		fmt.Fprintln(a.Stdout, "Dry run (no changes applied):")
		for _, rec := range toPublish {
			fmt.Fprintf(a.Stdout, "  [publish] %s (%s)\n", recordLabel(rec), rec.Slug)
		}
		for _, rec := range toUnpublish {
			fmt.Fprintf(a.Stdout, "  [private] %s (%s)\n", recordLabel(rec), rec.Slug)
		}
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !opts.withImageProvided {
		opts.withImage = cfg.WithImage
	}

	ghClient := a.NewGitHubClient(cfg.GitHubToken)

	var (
		publishedCount   int
		unpublishedCount int
		failedCount      int
		firstErr         error
	)

	for _, rec := range toPublish {
		updated, err := a.publishTrackedDocument(ctx, cfg, ghClient, rec, opts)
		if err != nil {
			failedCount++
			if firstErr == nil {
				firstErr = err
			}
			fmt.Fprintf(a.Stderr, "Error publishing %q: %v\n", recordLabel(rec), err)
			continue
		}
		st.Upsert(updated)
		if err := st.Save(); err != nil {
			return err
		}
		publishedCount++
		fmt.Fprintf(a.Stdout, "Published %q -> %s (%s:%s)\n", recordLabel(updated), updated.RemotePath, updated.GitHubRepo, updated.GitHubBranch)
	}

	for _, rec := range toUnpublish {
		updated, alreadyAbsent, err := a.unpublishTrackedDocument(ctx, cfg, ghClient, rec, opts)
		if err != nil {
			failedCount++
			if firstErr == nil {
				firstErr = err
			}
			fmt.Fprintf(a.Stderr, "Error making %q private: %v\n", recordLabel(rec), err)
			continue
		}
		st.Upsert(updated)
		if err := st.Save(); err != nil {
			return err
		}
		unpublishedCount++
		if alreadyAbsent {
			fmt.Fprintf(a.Stdout, "Marked %q private (remote file %s was already absent; local copy kept)\n", recordLabel(updated), updated.RemotePath)
		} else {
			fmt.Fprintf(a.Stdout, "Made %q private (removed %s from %s:%s; local copy kept)\n", recordLabel(updated), updated.RemotePath, updated.GitHubRepo, updated.GitHubBranch)
		}
	}

	fmt.Fprintf(a.Stdout, "\nSummary: %d published, %d made private", publishedCount, unpublishedCount)
	if failedCount > 0 {
		fmt.Fprintf(a.Stdout, ", %d failed", failedCount)
	}
	fmt.Fprintln(a.Stdout)

	if failedCount > 0 {
		return fmt.Errorf("failed to update %d document(s): %w", failedCount, firstErr)
	}
	return nil
}

func (a *App) publishTrackedDocument(ctx context.Context, cfg config.Config, ghClient *github.Client, existing state.Record, opts toggleOptions) (state.Record, error) {
	rec := existing
	now := time.Now().UTC()

	var (
		renderedBytes []byte
		downloaded    []downloadedImage
		prevImages    = existing.Images
	)

	if opts.fetchFlag {
		lookup := existing.LinearID
		if lookup == "" {
			lookup = existing.SlugID
		}
		if lookup == "" {
			lookup = existing.LinearURL
		}
		if lookup == "" {
			lookup = existing.Slug
		}

		linearClient := a.NewLinearClient(cfg.LinearAPIKey)
		if opts.withImage {
			linearClient.SignedFileURLTTL = signedURLTTLSeconds
		}
		doc, err := linearClient.ResolveDocument(ctx, lookup)
		if err != nil {
			return rec, err
		}

		slug, fileName := resolveSlugAndFilename(doc, &existing, "")
		docsDir, err := cfg.EffectiveDocsDir()
		if err != nil {
			return rec, err
		}
		if err := os.MkdirAll(docsDir, 0o755); err != nil {
			return rec, fmt.Errorf("create local docs directory %s: %w", docsDir, err)
		}

		content := doc.Content
		if opts.withImage {
			content, downloaded, err = localizeDocumentImages(ctx, linearClient, doc.Content, slug, docsDir)
			if err != nil {
				return rec, err
			}
			if err := writeLocalImages(downloaded); err != nil {
				return rec, err
			}
		}
		cleanupStaleLocalImages(prevImages, downloaded)

		useFrontmatter := cfg.UseFrontmatter() && !opts.noFrontmatterFlag
		docUpdatedAt := doc.UpdatedAt
		if docUpdatedAt.IsZero() {
			docUpdatedAt = now
		}

		rendered := markdown.Render(markdown.Metadata{
			Title:     doc.Title,
			LinearID:  doc.ID,
			SlugID:    doc.SlugID,
			Slug:      slug,
			LinearURL: doc.URL,
			UpdatedAt: docUpdatedAt,
		}, content, useFrontmatter)

		renderedBytes = []byte(rendered)
		localPath := filepath.Join(docsDir, fileName)
		if err := os.WriteFile(localPath, renderedBytes, 0o644); err != nil {
			return rec, fmt.Errorf("write local markdown %s: %w", localPath, err)
		}

		rec.LinearID = doc.ID
		rec.SlugID = doc.SlugID
		rec.Slug = slug
		rec.Title = doc.Title
		rec.LinearURL = doc.URL
		rec.LocalPath = localPath
		rec.LocalHash = markdown.ContentHash(renderedBytes)
		rec.LinearUpdatedAt = doc.UpdatedAt
		rec.Images = mergeLocalImageState(prevImages, downloaded)
	} else {
		if rec.LocalPath == "" {
			return rec, fmt.Errorf("local markdown path is not recorded for %q (re-run with --fetch)", recordLabel(rec))
		}
		data, err := os.ReadFile(rec.LocalPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return rec, fmt.Errorf("local file %s is missing (re-run with --fetch or run `lsy fetch %s`)", rec.LocalPath, rec.Slug)
			}
			return rec, fmt.Errorf("read local markdown %s: %w", rec.LocalPath, err)
		}
		renderedBytes = data
		rec.LocalHash = markdown.ContentHash(renderedBytes)

		for _, img := range rec.Images {
			if img.LocalPath == "" {
				continue
			}
			imgData, err := os.ReadFile(img.LocalPath)
			if err != nil {
				return rec, fmt.Errorf("read local image %s: %w", img.LocalPath, err)
			}
			downloaded = append(downloaded, downloadedImage{
				Asset: img,
				Data:  imgData,
			})
		}
	}

	repo := strings.TrimSpace(opts.repoFlag)
	if repo == "" {
		repo = strings.TrimSpace(existing.GitHubRepo)
	}
	if repo == "" {
		repo = strings.TrimSpace(cfg.GitHubRepo)
	}
	if repo == "" {
		return rec, errors.New("GitHub repository is not configured (run `lsy config set github_repo owner/repo` or pass --repo)")
	}

	branch := strings.TrimSpace(opts.branchFlag)
	if branch == "" {
		branch = strings.TrimSpace(existing.GitHubBranch)
	}
	if branch == "" {
		branch = strings.TrimSpace(cfg.GitHubBranch)
	}
	if branch == "" {
		branch = config.DefaultBranch
	}

	fileName := filepath.Base(rec.LocalPath)
	if fileName == "" || fileName == "." {
		fileName = rec.Slug + ".md"
	}

	remotePath := existing.RemotePath
	if opts.repoPathProvided {
		repoPath := config.NormalizeRepoPath(opts.repoPathFlag)
		if repoPath != "" {
			remotePath = path.Join(repoPath, fileName)
		} else {
			remotePath = fileName
		}
	} else if remotePath == "" {
		if cfg.RepoPath != "" {
			remotePath = path.Join(cfg.RepoPath, fileName)
		} else {
			remotePath = fileName
		}
	}

	remoteDir := path.Dir(remotePath)
	if remoteDir == "." {
		remoteDir = ""
	}

	commitMsg := strings.TrimSpace(opts.messageFlag)
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("docs(lsy): publish %s", recordLabel(rec))
	}

	publishedAssets := make([]state.ImageAsset, 0, len(downloaded))
	uploadedRemotePaths := make(map[string]bool, len(downloaded))
	for _, img := range downloaded {
		imgRemotePath := img.Asset.RelativePath
		if remoteDir != "" {
			imgRemotePath = path.Join(remoteDir, img.Asset.RelativePath)
		}
		imgMsg := strings.TrimSpace(opts.messageFlag)
		if imgMsg == "" {
			imgMsg = fmt.Sprintf("docs(lsy): publish image %s for %s", path.Base(img.Asset.RelativePath), recordLabel(rec))
		}
		imgRes, err := ghClient.UpsertFileIfChanged(ctx, repo, branch, imgRemotePath, imgMsg, img.Data)
		if err != nil {
			return rec, fmt.Errorf("publish image %s to github failed: %w", imgRemotePath, err)
		}
		asset := img.Asset
		asset.RemotePath = imgRemotePath
		asset.RemoteSHA = imgRes.SHA
		publishedAssets = append(publishedAssets, asset)
		uploadedRemotePaths[imgRemotePath] = true
	}

	res, err := ghClient.UpsertFile(ctx, repo, branch, remotePath, commitMsg, renderedBytes)
	if err != nil {
		return rec, fmt.Errorf("publish to github failed: %w", err)
	}

	if len(prevImages) > 0 {
		staleRepo := repo
		staleBranch := branch
		if existing.GitHubRepo != "" {
			staleRepo = existing.GitHubRepo
		}
		if existing.GitHubBranch != "" {
			staleBranch = existing.GitHubBranch
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
			cleanupMsg := fmt.Sprintf("docs(lsy): remove unused images for %s", recordLabel(rec))
			if err := deleteRemoteImages(ctx, ghClient, staleRepo, staleBranch, cleanupMsg, staleRemote); err != nil {
				fmt.Fprintf(a.Stderr, "Warning: failed to remove stale remote images for %q: %v\n", recordLabel(rec), err)
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
	return rec, nil
}

func (a *App) unpublishTrackedDocument(ctx context.Context, cfg config.Config, ghClient *github.Client, existing state.Record, opts toggleOptions) (state.Record, bool, error) {
	rec := existing

	repo := strings.TrimSpace(opts.repoFlag)
	if repo == "" {
		repo = strings.TrimSpace(existing.GitHubRepo)
	}
	if repo == "" {
		repo = strings.TrimSpace(cfg.GitHubRepo)
	}
	if repo == "" {
		return rec, false, errors.New("GitHub repository is not configured (run `lsy config set github_repo owner/repo` or pass --repo)")
	}

	branch := strings.TrimSpace(opts.branchFlag)
	if branch == "" {
		branch = strings.TrimSpace(existing.GitHubBranch)
	}
	if branch == "" {
		branch = strings.TrimSpace(cfg.GitHubBranch)
	}
	if branch == "" {
		branch = config.DefaultBranch
	}

	remotePath := existing.RemotePath
	if remotePath == "" || opts.repoPathProvided {
		repoPath := cfg.RepoPath
		if opts.repoPathProvided {
			repoPath = config.NormalizeRepoPath(opts.repoPathFlag)
		}
		name := existing.Slug
		if name == "" && existing.LocalPath != "" {
			name = filepath.Base(existing.LocalPath)
		}
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			name += ".md"
		}
		if repoPath != "" {
			remotePath = path.Join(repoPath, name)
		} else {
			remotePath = name
		}
	}

	commitMsg := strings.TrimSpace(opts.messageFlag)
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("docs(lsy): unpublish %s", recordLabel(existing))
	}

	alreadyAbsent := false
	if err := ghClient.DeleteFile(ctx, repo, branch, remotePath, "", commitMsg); err != nil {
		if errors.Is(err, github.ErrNotFound) {
			alreadyAbsent = true
		} else {
			return rec, false, fmt.Errorf("unpublish from github failed: %w", err)
		}
	}

	if len(rec.Images) > 0 {
		if err := deleteRemoteImages(ctx, ghClient, repo, branch, commitMsg, rec.Images); err != nil {
			return rec, alreadyAbsent, fmt.Errorf("unpublish images from github failed: %w", err)
		}
		keptImages := make([]state.ImageAsset, 0, len(rec.Images))
		for _, img := range rec.Images {
			img.RemotePath = ""
			img.RemoteSHA = ""
			if img.LocalPath != "" {
				keptImages = append(keptImages, img)
			}
		}
		rec.Images = keptImages
	}

	rec.GitHubRepo = repo
	rec.GitHubBranch = branch
	rec.RemotePath = remotePath
	rec.Status = state.StatusUnpublished
	rec.RemoteSHA = ""
	rec.UpdatedAt = time.Now().UTC()
	return rec, alreadyAbsent, nil
}

func recordLabel(rec state.Record) string {
	if strings.TrimSpace(rec.Title) != "" {
		return rec.Title
	}
	return rec.Slug
}
