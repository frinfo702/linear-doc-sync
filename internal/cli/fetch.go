package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/frinfo702/linear-doc-sync/internal/config"
	"github.com/frinfo702/linear-doc-sync/internal/markdown"
	"github.com/frinfo702/linear-doc-sync/internal/state"
)

type conflictPolicy int

const (
	conflictPrompt conflictPolicy = iota
	conflictOverwriteAll
	conflictKeepAll
)

type fetchOutcome int

const (
	outcomeFetched fetchOutcome = iota
	outcomeUpdated
	outcomeOverwritten
	outcomeUpToDate
	outcomeKeptLocal
)

func (a *App) runFetch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)

	var (
		allFlag           bool
		withImageFlag     bool
		filenameFlag      string
		noFrontmatterFlag bool
		forceFlag         bool
		keepLocalFlag     bool
	)

	fs.BoolVar(&allFlag, "all", false, "Fetch all tracked documents in state.json")
	fs.BoolVar(&withImageFlag, "with-image", false, "Download images locally and rewrite Markdown links (overrides config)")
	fs.StringVar(&filenameFlag, "filename", "", "Custom output Markdown filename when fetching a single document")
	fs.BoolVar(&noFrontmatterFlag, "no-frontmatter", false, "Do not prepend YAML frontmatter to Markdown")
	fs.BoolVar(&forceFlag, "force", false, "Overwrite local changes without prompting")
	fs.BoolVar(&forceFlag, "f", false, "Overwrite local changes without prompting")
	fs.BoolVar(&keepLocalFlag, "keep-local", false, "Keep local changes without prompting (skip modified files)")
	fs.Usage = func() {
		fmt.Fprint(a.Stdout, `Usage:
  lsy fetch [url-or-name] [flags]

Fetch document(s) from Linear and update local Markdown clones and state.json
without pushing to GitHub. When no argument is given, fetches all tracked
documents in state.json.

If local modifications are detected, you will be prompted to choose whether to
overwrite the local file or keep your local changes.

Aliases:
  fetch, pull

Flags:
  --all                 Fetch all tracked documents (default when no argument is given)
  --with-image          Download images locally and rewrite links (overrides config)
  --filename <name>     Override Markdown filename (single document only)
  --no-frontmatter      Omit YAML frontmatter from generated Markdown
  -f, --force           Overwrite local changes without prompting
  --keep-local          Keep local changes without prompting
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

	if forceFlag && keepLocalFlag {
		return errors.New("--force and --keep-local cannot be combined")
	}
	if len(posArgs) > 1 {
		return errors.New("usage: lsy fetch [url-or-name] [flags]")
	}
	if allFlag && len(posArgs) > 0 {
		return errors.New("--all cannot be combined with a document argument")
	}

	fetchAll := allFlag || len(posArgs) == 0
	if fetchAll && strings.TrimSpace(filenameFlag) != "" {
		return errors.New("--filename can only be used when fetching a single document")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, err := state.Load()
	if err != nil {
		return err
	}

	withImage := cfg.WithImage
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "with-image" {
			withImage = withImageFlag
		}
	})
	useFrontmatter := cfg.UseFrontmatter() && !noFrontmatterFlag

	docsDir, err := cfg.EffectiveDocsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		return fmt.Errorf("create local docs directory %s: %w", docsDir, err)
	}

	linearClient := a.NewLinearClient(cfg.LinearAPIKey)
	if withImage {
		linearClient.SignedFileURLTTL = signedURLTTLSeconds
	}

	policy := conflictPrompt
	if forceFlag {
		policy = conflictOverwriteAll
	} else if keepLocalFlag {
		policy = conflictKeepAll
	}
	reader := bufio.NewReader(a.Stdin)

	type fetchJob struct {
		target      string
		existingRec *state.Record
	}

	var jobs []fetchJob
	if fetchAll {
		if len(st.Documents) == 0 {
			fmt.Fprintln(a.Stdout, "No tracked documents to fetch. Pass a Linear document URL or name: lsy fetch <url-or-name>")
			return nil
		}
		for i := range st.Documents {
			recCopy := st.Documents[i]
			lookup := recCopy.LinearID
			if lookup == "" {
				lookup = recCopy.SlugID
			}
			if lookup == "" {
				lookup = recCopy.LinearURL
			}
			jobs = append(jobs, fetchJob{
				target:      lookup,
				existingRec: &recCopy,
			})
		}
	} else {
		target := posArgs[0]
		existingRec, _ := st.Find(target)
		lookup := target
		var existingCopy *state.Record
		if existingRec != nil {
			cp := *existingRec
			existingCopy = &cp
			if cp.LinearID != "" {
				lookup = cp.LinearID
			}
		}
		jobs = append(jobs, fetchJob{
			target:      lookup,
			existingRec: existingCopy,
		})
	}

	multi := len(jobs) > 1
	var (
		updatedCount   int
		upToDateCount  int
		keptLocalCount int
		failedCount    int
		firstErr       error
	)

	for _, job := range jobs {
		doc, err := linearClient.ResolveDocument(ctx, job.target)
		if err != nil {
			if !multi {
				return err
			}
			failedCount++
			if firstErr == nil {
				firstErr = err
			}
			label := job.target
			if job.existingRec != nil && job.existingRec.Title != "" {
				label = job.existingRec.Title
			}
			fmt.Fprintf(a.Stderr, "Warning: failed to fetch %q: %v\n", label, err)
			continue
		}

		// Also match existing record by resolved Linear ID if target was a URL/title not matched earlier.
		existingRec := job.existingRec
		if existingRec == nil {
			if found, _ := st.Find(doc.ID); found != nil {
				cp := *found
				existingRec = &cp
			}
		}

		slug, fileName := resolveSlugAndFilename(doc, existingRec, filenameFlag)

		content := doc.Content
		var downloaded []downloadedImage
		if withImage {
			content, downloaded, err = localizeDocumentImages(ctx, linearClient, doc.Content, slug, docsDir)
			if err != nil {
				if !multi {
					return err
				}
				failedCount++
				if firstErr == nil {
					firstErr = err
				}
				fmt.Fprintf(a.Stderr, "Warning: failed to download images for %q: %v\n", doc.Title, err)
				continue
			}
		}

		docUpdatedAt := doc.UpdatedAt
		if docUpdatedAt.IsZero() {
			docUpdatedAt = time.Now().UTC()
		}

		rendered := markdown.Render(markdown.Metadata{
			Title:     doc.Title,
			LinearID:  doc.ID,
			SlugID:    doc.SlugID,
			Slug:      slug,
			LinearURL: doc.URL,
			UpdatedAt: docUpdatedAt,
		}, content, useFrontmatter)

		renderedBytes := []byte(rendered)
		newHash := markdown.ContentHash(renderedBytes)
		localPath := filepath.Join(docsDir, fileName)

		sameAsDisk, hasLocalChanges, err := checkLocalFileState(localPath, renderedBytes, existingRec)
		if err != nil {
			if !multi {
				return err
			}
			failedCount++
			if firstErr == nil {
				firstErr = err
			}
			fmt.Fprintf(a.Stderr, "Warning: failed to inspect local file %s: %v\n", localPath, err)
			continue
		}

		now := time.Now().UTC()
		rec := state.Record{
			LinearID:        doc.ID,
			SlugID:          doc.SlugID,
			Slug:            slug,
			Title:           doc.Title,
			LinearURL:       doc.URL,
			LocalPath:       localPath,
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
			rec.LocalHash = existingRec.LocalHash
			rec.Images = existingRec.Images
		}

		var outcome fetchOutcome
		if sameAsDisk {
			if withImage {
				if err := ensureLocalImagesPresent(downloaded); err != nil {
					return err
				}
				if existingRec != nil {
					rec.Images = mergeLocalImageState(existingRec.Images, downloaded)
				} else {
					rec.Images = mergeLocalImageState(nil, downloaded)
				}
			}
			rec.LocalHash = newHash
			st.Upsert(rec)
			outcome = outcomeUpToDate
		} else if hasLocalChanges {
			overwrite := false
			switch policy {
			case conflictOverwriteAll:
				overwrite = true
			case conflictKeepAll:
				overwrite = false
			case conflictPrompt:
				choice, nextPolicy := a.promptOverwriteLocal(reader, doc.Title, localPath, multi)
				overwrite = choice
				policy = nextPolicy
			}

			if !overwrite {
				// Keep local file and local images untouched; if already tracked,
				// update Linear metadata in state.json while preserving LocalHash.
				if existingRec != nil {
					rec.LocalPath = existingRec.LocalPath
					st.Upsert(rec)
				}
				outcome = outcomeKeptLocal
			} else {
				if err := writeLocalImages(downloaded); err != nil {
					return err
				}
				if existingRec != nil {
					cleanupStaleLocalImages(existingRec.Images, downloaded)
					rec.Images = mergeLocalImageState(existingRec.Images, downloaded)
				} else {
					rec.Images = mergeLocalImageState(nil, downloaded)
				}
				if err := os.WriteFile(localPath, renderedBytes, 0o644); err != nil {
					return fmt.Errorf("write local markdown %s: %w", localPath, err)
				}
				rec.LocalHash = newHash
				st.Upsert(rec)
				outcome = outcomeOverwritten
			}
		} else {
			if err := writeLocalImages(downloaded); err != nil {
				return err
			}
			if existingRec != nil {
				cleanupStaleLocalImages(existingRec.Images, downloaded)
				rec.Images = mergeLocalImageState(existingRec.Images, downloaded)
			} else {
				rec.Images = mergeLocalImageState(nil, downloaded)
			}
			if err := os.WriteFile(localPath, renderedBytes, 0o644); err != nil {
				return fmt.Errorf("write local markdown %s: %w", localPath, err)
			}
			rec.LocalHash = newHash
			st.Upsert(rec)
			if existingRec == nil {
				outcome = outcomeFetched
			} else {
				outcome = outcomeUpdated
			}
		}

		switch outcome {
		case outcomeFetched:
			updatedCount++
			fmt.Fprintf(a.Stdout, "Fetched %q\n", doc.Title)
			fmt.Fprintf(a.Stdout, "  Local:  %s\n", localPath)
			if len(downloaded) > 0 {
				fmt.Fprintf(a.Stdout, "  Images: %d saved locally\n", len(downloaded))
			}
		case outcomeUpdated:
			updatedCount++
			fmt.Fprintf(a.Stdout, "Updated %q\n", doc.Title)
			fmt.Fprintf(a.Stdout, "  Local:  %s\n", localPath)
			if len(downloaded) > 0 {
				fmt.Fprintf(a.Stdout, "  Images: %d saved locally\n", len(downloaded))
			}
		case outcomeOverwritten:
			updatedCount++
			fmt.Fprintf(a.Stdout, "Overwrote %q\n", doc.Title)
			fmt.Fprintf(a.Stdout, "  Local:  %s\n", localPath)
			if len(downloaded) > 0 {
				fmt.Fprintf(a.Stdout, "  Images: %d saved locally\n", len(downloaded))
			}
		case outcomeUpToDate:
			upToDateCount++
			fmt.Fprintf(a.Stdout, "Up to date: %q (%s)\n", doc.Title, localPath)
		case outcomeKeptLocal:
			keptLocalCount++
			fmt.Fprintf(a.Stdout, "Kept local changes for %q (%s)\n", doc.Title, localPath)
		}
	}

	if err := st.Save(); err != nil {
		return err
	}

	if multi {
		fmt.Fprintf(a.Stdout, "\nSummary: %d updated, %d up to date, %d kept local", updatedCount, upToDateCount, keptLocalCount)
		if failedCount > 0 {
			fmt.Fprintf(a.Stdout, ", %d failed", failedCount)
		}
		fmt.Fprintln(a.Stdout)
	}

	if failedCount > 0 {
		return fmt.Errorf("failed to fetch %d document(s): %w", failedCount, firstErr)
	}
	return nil
}

func checkLocalFileState(localPath string, newBytes []byte, existingRec *state.Record) (sameAsDisk bool, hasLocalChanges bool, err error) {
	diskBytes, err := os.ReadFile(localPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}

	diskHash := markdown.ContentHash(diskBytes)
	newHash := markdown.ContentHash(newBytes)
	if diskHash == newHash {
		return true, false, nil
	}

	if existingRec != nil && existingRec.LocalHash != "" {
		return false, diskHash != existingRec.LocalHash, nil
	}
	// Existing file on disk without a recorded baseline hash: treat cautiously as locally modified.
	return false, true, nil
}

func ensureLocalImagesPresent(images []downloadedImage) error {
	for _, img := range images {
		if _, err := os.Stat(img.Asset.LocalPath); errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(filepath.Dir(img.Asset.LocalPath), 0o755); err != nil {
				return fmt.Errorf("create image directory %s: %w", filepath.Dir(img.Asset.LocalPath), err)
			}
			if err := os.WriteFile(img.Asset.LocalPath, img.Data, 0o644); err != nil {
				return fmt.Errorf("write local image %s: %w", img.Asset.LocalPath, err)
			}
		}
	}
	return nil
}

func (a *App) promptOverwriteLocal(reader *bufio.Reader, title, localPath string, multi bool) (overwrite bool, nextPolicy conflictPolicy) {
	fmt.Fprintf(a.Stdout, "Local changes detected in %s (%q).\n", localPath, title)
	if multi {
		fmt.Fprint(a.Stdout, "Overwrite local file with latest version from Linear? [y/N/a(ll)/s(kip-all)]: ")
	} else {
		fmt.Fprint(a.Stdout, "Overwrite local file with latest version from Linear? [y/N]: ")
	}

	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false, conflictPrompt
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	switch ans {
	case "y", "yes":
		return true, conflictPrompt
	case "a", "all":
		if multi {
			return true, conflictOverwriteAll
		}
		return true, conflictPrompt
	case "s", "skip", "skip-all":
		if multi {
			return false, conflictKeepAll
		}
		return false, conflictPrompt
	default:
		return false, conflictPrompt
	}
}
