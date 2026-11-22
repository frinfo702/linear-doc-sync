package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/frinfo702/linear-doc-sync/internal/config"
	"github.com/frinfo702/linear-doc-sync/internal/github"
	"github.com/frinfo702/linear-doc-sync/internal/linear"
)

// BuildInfo carries version metadata injected at build time.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// App encapsulates CLI dependencies for testability and execution.
type App struct {
	Build           BuildInfo
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
	NewLinearClient func(apiKey string) *linear.Client
	NewGitHubClient func(token string) *github.Client
}

// NewApp returns an App configured with standard IO and real API clients.
func NewApp(build BuildInfo) *App {
	return &App{
		Build:           build,
		Stdin:           os.Stdin,
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
		NewLinearClient: linear.NewClient,
		NewGitHubClient: github.NewClient,
	}
}

// Run executes the CLI with the given arguments (excluding argv[0]).
func (a *App) Run(ctx context.Context, args []string) error {
	if _, err := config.EnsureInitialized(); err != nil {
		return err
	}

	if len(args) == 0 {
		a.printRootUsage()
		return nil
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "-h", "--help", "help":
		a.printRootUsage()
		return nil
	case "-v", "--version", "version":
		a.printVersion()
		return nil
	case "publish":
		return a.runPublish(ctx, rest)
	case "fetch", "pull":
		return a.runFetch(ctx, rest)
	case "unpublish":
		return a.runUnpublish(ctx, rest)
	case "toggle", "switch":
		return a.runToggle(ctx, rest)
	case "list", "ls":
		return a.runList(ctx, rest)
	case "rm", "remove":
		return a.runRm(ctx, rest)
	case "config":
		return a.runConfig(ctx, rest)
	default:
		return fmt.Errorf("unknown command %q (run `lsy --help` for usage)", cmd)
	}
}

func (a *App) printVersion() {
	v := a.Build.Version
	if v == "" {
		v = "dev"
	}
	fmt.Fprintf(a.Stdout, "lsy %s", v)
	if a.Build.Commit != "" && a.Build.Commit != "none" {
		short := a.Build.Commit
		if len(short) > 7 {
			short = short[:7]
		}
		fmt.Fprintf(a.Stdout, " (%s", short)
		if a.Build.Date != "" && a.Build.Date != "unknown" {
			fmt.Fprintf(a.Stdout, ", %s", a.Build.Date)
		}
		fmt.Fprint(a.Stdout, ")")
	}
	fmt.Fprintln(a.Stdout)
}

func (a *App) printRootUsage() {
	fmt.Fprint(a.Stdout, `lsy - Publish Linear documents to GitHub as Markdown

Usage:
  lsy <command> [arguments] [flags]

Commands:
  publish <url|name>    Fetch a Linear document locally and publish it to GitHub
  fetch [url|name]      Fetch document(s) from Linear and update local state (alias: pull)
  unpublish <url|name>  Remove a published document from GitHub (keeps local copy)
  toggle                Interactively toggle documents between published and private (alias: switch)
  list                  List locally tracked documents (alias: ls)
  rm <url|name>         Delete a document from local storage
  config [subcommand]   Manage ~/.config/lsy configuration (init, set, get, list, path)
  version               Show version information

Global Flags:
  -h, --help            Show help for lsy or a subcommand
  -v, --version         Show version information

Examples:
  lsy config init
  lsy config set repo-path content/docs
  lsy config set with-image true
  lsy publish https://linear.app/workspace/document/my-doc-a1b2c3d4e5f6 --with-image
  lsy publish https://linear.app/workspace/document/my-doc-a1b2c3d4e5f6 --repo-path blog/posts
  lsy fetch
  lsy fetch my-doc --with-image
  lsy toggle
  lsy list
  lsy list -i
  lsy unpublish my-doc
  lsy rm my-doc

Run 'lsy <command> --help' for command-specific flags.
`)
}

// parseInterspersedFlags parses flags regardless of whether they appear before
// or after positional arguments (e.g., `lsy publish <url> --repo-path docs`).
func parseInterspersedFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	// Identify boolean flags so we know which flags take a separate value argument.
	boolFlags := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			boolFlags[f.Name] = true
		}
	})

	var flagArgs []string
	var posArgs []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			posArgs = append(posArgs, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flagArgs = append(flagArgs, arg)
			if strings.Contains(arg, "=") {
				continue
			}
			name := strings.TrimLeft(arg, "-")
			if name == "h" || name == "help" || boolFlags[name] {
				continue
			}
			if i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		} else {
			posArgs = append(posArgs, arg)
		}
	}

	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}
	return append(posArgs, fs.Args()...), nil
}
