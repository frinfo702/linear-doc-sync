package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"

	"github.com/frinfo702/linear-doc-sync/internal/state"
)

func (a *App) runList(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)

	var (
		publishedOnly   bool
		unpublishedOnly bool
		jsonOutput      bool
	)

	fs.BoolVar(&publishedOnly, "published", false, "Show only published documents")
	fs.BoolVar(&unpublishedOnly, "unpublished", false, "Show only unpublished documents")
	fs.BoolVar(&jsonOutput, "json", false, "Output as JSON")
	fs.Usage = func() {
		fmt.Fprint(a.Stdout, `Usage:
  lsy list [flags]

List all locally tracked Linear documents.

Flags:
  --published     Show only published documents
  --unpublished   Show only unpublished documents
  --json          Output as JSON
  -h, --help      Show help
`)
	}

	if _, err := parseInterspersedFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if publishedOnly && unpublishedOnly {
		return errors.New("--published and --unpublished cannot be combined")
	}

	st, err := state.Load()
	if err != nil {
		return err
	}

	filtered := make([]state.Record, 0, len(st.Documents))
	for _, doc := range st.Documents {
		if publishedOnly && doc.Status != state.StatusPublished {
			continue
		}
		if unpublishedOnly && doc.Status != state.StatusUnpublished {
			continue
		}
		filtered = append(filtered, doc)
	}

	if jsonOutput {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(filtered)
	}

	if len(filtered) == 0 {
		fmt.Fprintln(a.Stdout, "No tracked documents found.")
		return nil
	}

	tw := tabwriter.NewWriter(a.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTITLE\tSTATUS\tGITHUB PATH\tLOCAL PATH")
	for _, doc := range filtered {
		remote := "-"
		if doc.RemotePath != "" && doc.Status == state.StatusPublished {
			if doc.GitHubRepo != "" {
				remote = fmt.Sprintf("%s:%s", doc.GitHubRepo, doc.RemotePath)
			} else {
				remote = doc.RemotePath
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", doc.Slug, doc.Title, doc.Status, remote, doc.LocalPath)
	}
	return tw.Flush()
}
