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
	)

	fs.StringVar(&repoFlag, "repo", "", "Target GitHub repository in owner/repo format (overrides config)")
	fs.StringVar(&branchFlag, "branch", "", "Target GitHub branch (overrides config)")
	fs.StringVar(&repoPathFlag, "repo-path", "", "Directory path inside the GitHub repository (overrides config)")
	fs.StringVar(&filenameFlag, "filename", "", "Custom output Markdown filename (default: <slug>.md)")
	fs.StringVar(&messageFlag, "message", "", "Custom Git commit message")
	fs.BoolVar(&noFrontmatterFlag, "no-frontmatter", false, "Do not prepend YAML frontmatter to Markdown")
	fs.BoolVar(&localOnlyFlag, "local-only", false, "Only fetch and save locally without pushing to GitHub")
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

	// Determine whether --repo-path was explicitly passed (so `--repo-path ""` can target repo root).
	repoPathProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "repo-path" {
			repoPathProvided = true
		}
	})

	// If target matches an existing tracked record, prefer its Linear ID for reliable re-sync.
	existingRec, _ := st.Find(target)
	lookupTarget := target
	if existingRec != nil && existingRec.LinearID != "" {
		lookupTarget = existingRec.LinearID
	}

	linearClient := a.NewLinearClient(cfg.LinearAPIKey)
	doc, err := linearClient.ResolveDocument(ctx, lookupTarget)
	if err != nil {
		return err
	}

	slug := markdown.Slugify(doc.Title)
	if existingRec != nil && existingRec.Slug != "" {
		slug = existingRec.Slug
	}
	if slug == "document" && doc.SlugID != "" {
		slug = strings.ToLower(doc.SlugID)
	}

	fileName := slug + ".md"
	if strings.TrimSpace(filenameFlag) != "" {
		fileName = filepath.Base(strings.TrimSpace(filenameFlag))
		if !strings.HasSuffix(strings.ToLower(fileName), ".md") {
			fileName += ".md"
		}
		slug = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	} else if existingRec != nil && existingRec.LocalPath != "" {
		fileName = filepath.Base(existingRec.LocalPath)
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
	}, doc.Content, useFrontmatter)

	docsDir, err := cfg.EffectiveDocsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		return fmt.Errorf("create local docs directory %s: %w", docsDir, err)
	}

	localPath := filepath.Join(docsDir, fileName)
	if err := os.WriteFile(localPath, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write local markdown %s: %w", localPath, err)
	}

	now := time.Now().UTC()
	rec := state.Record{
		LinearID:  doc.ID,
		SlugID:    doc.SlugID,
		Slug:      slug,
		Title:     doc.Title,
		LinearURL: doc.URL,
		LocalPath: localPath,
		Status:    state.StatusUnpublished,
		UpdatedAt: now,
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
	res, err := ghClient.UpsertFile(ctx, repo, branch, remotePath, commitMsg, []byte(rendered))
	if err != nil {
		// Persist the local clone in state even if the GitHub push fails.
		st.Upsert(rec)
		_ = st.Save()
		return fmt.Errorf("publish to github failed (local copy saved at %s): %w", localPath, err)
	}

	rec.GitHubRepo = repo
	rec.GitHubBranch = branch
	rec.RemotePath = remotePath
	rec.RemoteSHA = res.SHA
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
	if res.HTMLURL != "" {
		fmt.Fprintf(a.Stdout, "  URL:    %s\n", res.HTMLURL)
	}
	return nil
}
