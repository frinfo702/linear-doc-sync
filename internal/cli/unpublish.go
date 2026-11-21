package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/frinfo702/linear-doc-sync/internal/config"
	"github.com/frinfo702/linear-doc-sync/internal/github"
	"github.com/frinfo702/linear-doc-sync/internal/markdown"
	"github.com/frinfo702/linear-doc-sync/internal/state"
)

func (a *App) runUnpublish(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("unpublish", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)

	var (
		repoFlag     string
		branchFlag   string
		repoPathFlag string
		messageFlag  string
	)

	fs.StringVar(&repoFlag, "repo", "", "Override GitHub repository (owner/repo)")
	fs.StringVar(&branchFlag, "branch", "", "Override GitHub branch")
	fs.StringVar(&repoPathFlag, "repo-path", "", "Override GitHub directory path (used when document is not in local state)")
	fs.StringVar(&messageFlag, "message", "", "Custom Git commit message")
	fs.Usage = func() {
		fmt.Fprint(a.Stdout, `Usage:
  lsy unpublish <url-or-name> [flags]

Delete a published document from GitHub while keeping its local Markdown copy.

Flags:
  --repo <owner/repo>   Override GitHub repository
  --branch <branch>     Override GitHub branch
  --repo-path <dir>     Override GitHub directory path
  --message <msg>       Custom Git commit message
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
		return errors.New("usage: lsy unpublish <url-or-name> [flags]")
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

	repoPathProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "repo-path" {
			repoPathProvided = true
		}
	})

	rec, idx := st.Find(target)

	repo := strings.TrimSpace(repoFlag)
	branch := strings.TrimSpace(branchFlag)
	remotePath := ""
	remoteSHA := ""
	label := target

	if rec != nil {
		label = rec.Title
		if label == "" {
			label = rec.Slug
		}
		if repo == "" {
			repo = rec.GitHubRepo
		}
		if branch == "" {
			branch = rec.GitHubBranch
		}
		remotePath = rec.RemotePath
		remoteSHA = rec.RemoteSHA
	}

	if repo == "" {
		repo = strings.TrimSpace(cfg.GitHubRepo)
	}
	if branch == "" {
		branch = strings.TrimSpace(cfg.GitHubBranch)
	}
	if branch == "" {
		branch = config.DefaultBranch
	}

	if repo == "" {
		return errors.New("GitHub repository is not configured (run `lsy config set github_repo owner/repo` or pass --repo)")
	}

	if remotePath == "" || repoPathProvided {
		repoPath := cfg.RepoPath
		if repoPathProvided {
			repoPath = config.NormalizeRepoPath(repoPathFlag)
		}
		name := target
		if rec != nil && rec.Slug != "" {
			name = rec.Slug
		} else {
			name = markdown.Slugify(strings.TrimSuffix(target, ".md"))
		}
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			name += ".md"
		}
		if repoPath != "" {
			remotePath = path.Join(repoPath, name)
		} else {
			remotePath = name
		}
		remoteSHA = ""
	}

	commitMsg := strings.TrimSpace(messageFlag)
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("docs(lsy): unpublish %s", label)
	}

	ghClient := a.NewGitHubClient(cfg.GitHubToken)
	// Always query the latest SHA (or fall back cleanly) to avoid stale-SHA 409 conflicts.
	if err := ghClient.DeleteFile(ctx, repo, branch, remotePath, "", commitMsg); err != nil {
		if errors.Is(err, github.ErrNotFound) {
			if rec != nil && rec.Status == state.StatusPublished {
				st.Documents[idx].Status = state.StatusUnpublished
				st.Documents[idx].RemoteSHA = ""
				st.Documents[idx].UpdatedAt = time.Now().UTC()
				_ = st.Save()
			}
			return fmt.Errorf("file %s not found in %s (%s)", remotePath, repo, branch)
		}
		// Fallback to stored SHA only if needed; otherwise surface error directly.
		_ = remoteSHA
		return fmt.Errorf("unpublish from github failed: %w", err)
	}

	if rec != nil {
		if len(rec.Images) > 0 {
			if err := deleteRemoteImages(ctx, ghClient, repo, branch, commitMsg, rec.Images); err != nil {
				return fmt.Errorf("unpublish images from github failed: %w", err)
			}
			keptImages := make([]state.ImageAsset, 0, len(rec.Images))
			for _, img := range rec.Images {
				img.RemotePath = ""
				img.RemoteSHA = ""
				if img.LocalPath != "" {
					keptImages = append(keptImages, img)
				}
			}
			st.Documents[idx].Images = keptImages
		}
		st.Documents[idx].Status = state.StatusUnpublished
		st.Documents[idx].RemoteSHA = ""
		st.Documents[idx].UpdatedAt = time.Now().UTC()
		if err := st.Save(); err != nil {
			return err
		}
	}

	fmt.Fprintf(a.Stdout, "Unpublished %q from %s (%s:%s)\n", label, remotePath, repo, branch)
	if rec != nil && rec.LocalPath != "" {
		fmt.Fprintf(a.Stdout, "  Local copy kept at: %s\n", rec.LocalPath)
	}
	return nil
}
