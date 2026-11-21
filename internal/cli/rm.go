package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/frinfo702/linear-doc-sync/internal/config"
	"github.com/frinfo702/linear-doc-sync/internal/github"
	"github.com/frinfo702/linear-doc-sync/internal/state"
)

func (a *App) runRm(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)

	var (
		unpublishFlag bool
		forceFlag     bool
	)

	fs.BoolVar(&unpublishFlag, "unpublish", false, "Also unpublish (delete) the file from GitHub")
	fs.BoolVar(&forceFlag, "force", false, "Ignore missing local file on disk")
	fs.BoolVar(&forceFlag, "f", false, "Ignore missing local file on disk")
	fs.Usage = func() {
		fmt.Fprint(a.Stdout, `Usage:
  lsy rm <url-or-name> [flags]

Remove a document from local storage (~/.config/lsy/docs and state.json).
By default, the remote file on GitHub is NOT deleted unless --unpublish is passed.

Flags:
  --unpublish     Also delete the file from GitHub before removing locally
  -f, --force     Ignore missing local file on disk
  -h, --help      Show help
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
		return errors.New("usage: lsy rm <url-or-name> [flags]")
	}
	target := posArgs[0]

	st, err := state.Load()
	if err != nil {
		return err
	}

	rec, idx := st.Find(target)
	if rec == nil {
		return fmt.Errorf("local document %q not found (run `lsy list` to view tracked documents)", target)
	}

	wasPublished := rec.Status == state.StatusPublished

	if unpublishFlag && wasPublished && rec.GitHubRepo != "" && rec.RemotePath != "" {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		branch := rec.GitHubBranch
		if branch == "" {
			branch = cfg.GitHubBranch
		}
		ghClient := a.NewGitHubClient(cfg.GitHubToken)
		msg := fmt.Sprintf("docs(lsy): unpublish %s", rec.Title)
		if err := ghClient.DeleteFile(ctx, rec.GitHubRepo, branch, rec.RemotePath, "", msg); err != nil && !errors.Is(err, github.ErrNotFound) {
			return fmt.Errorf("failed to unpublish from github before local removal: %w", err)
		}
		if len(rec.Images) > 0 {
			if err := deleteRemoteImages(ctx, ghClient, rec.GitHubRepo, branch, msg, rec.Images); err != nil {
				return fmt.Errorf("failed to unpublish images from github before local removal: %w", err)
			}
		}
		fmt.Fprintf(a.Stdout, "Unpublished %s from %s\n", rec.RemotePath, rec.GitHubRepo)
		wasPublished = false
	}

	if len(rec.Images) > 0 {
		removeLocalImageFiles(rec.Images)
	}

	if rec.LocalPath != "" {
		if err := os.Remove(rec.LocalPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) || !forceFlag {
				if errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("local file %s is already missing (re-run with --force to remove state entry)", rec.LocalPath)
				}
				return fmt.Errorf("remove local file %s: %w", rec.LocalPath, err)
			}
		}
	}

	slug := rec.Slug
	localPath := rec.LocalPath
	remotePath := rec.RemotePath
	remoteRepo := rec.GitHubRepo

	st.RemoveAt(idx)
	if err := st.Save(); err != nil {
		return err
	}

	fmt.Fprintf(a.Stdout, "Removed local document %q (%s)\n", slug, localPath)
	if wasPublished && remotePath != "" {
		fmt.Fprintf(a.Stdout, "Note: remote file %s in %s was NOT deleted (use `lsy unpublish` or `lsy rm --unpublish` to remove it from GitHub).\n", remotePath, remoteRepo)
	}
	return nil
}
