package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/frinfo702/linear-doc-sync/internal/config"
)

func (a *App) runConfig(_ context.Context, args []string) error {
	if len(args) == 0 {
		return a.runConfigList(nil)
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "-h", "--help", "help":
		a.printConfigUsage()
		return nil
	case "init":
		return a.runConfigInit(rest)
	case "set":
		return a.runConfigSet(rest)
	case "get":
		return a.runConfigGet(rest)
	case "list", "ls", "show":
		return a.runConfigList(rest)
	case "path":
		p, err := config.FilePath()
		if err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout, p)
		return nil
	default:
		return fmt.Errorf("unknown config subcommand %q (valid: init, set, get, list, path)", sub)
	}
}

func (a *App) printConfigUsage() {
	fmt.Fprint(a.Stdout, `Usage:
  lsy config [subcommand]

Manage ~/.config/lsy/config.json settings.

Subcommands:
  init                 Interactive setup wizard
  set <key> <value>    Set a configuration value
  get <key> [--reveal] Get a configuration value
  list [--reveal]      List all configuration values (default)
  path                 Print the path to config.json

Keys:
  linear_api_key       Linear Personal API Key (env: LINEAR_API_KEY)
  github_token         GitHub Personal Access Token (env: GITHUB_TOKEN)
  github_repo          Target GitHub repo in owner/repo format (env: LSY_GITHUB_REPO)
  github_branch        Target GitHub branch (default: main, env: LSY_GITHUB_BRANCH)
  repo_path            Default directory path in GitHub (default: docs, env: LSY_REPO_PATH)
  docs_dir             Local directory for cloned Markdown files (env: LSY_DOCS_DIR)
  frontmatter          Prepend YAML frontmatter (true|false, default: true)

Examples:
  lsy config init
  lsy config set github_repo frinfo702/my-blog
  lsy config set repo-path content/docs
  lsy config list
`)
}

func (a *App) runConfigInit(args []string) error {
	fs := flag.NewFlagSet("config init", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	if _, err := parseInterspersedFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(a.Stdout, "Usage: lsy config init")
			return nil
		}
		return err
	}

	cfg, err := config.LoadRaw()
	if err != nil {
		return err
	}

	cfgPath, err := config.FilePath()
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Configuring lsy (%s)\nPress Enter to keep the current value.\n\n", cfgPath)

	reader := bufio.NewReader(a.Stdin)
	prompt := func(label, current string, secret bool) (string, error) {
		display := current
		if secret {
			display = config.MaskSecret(current)
		}
		if display != "" {
			fmt.Fprintf(a.Stdout, "%s [%s]: ", label, display)
		} else {
			fmt.Fprintf(a.Stdout, "%s: ", label)
		}
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return current, nil
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return current, nil
		}
		return line, nil
	}

	if v, err := prompt("Linear API Key", cfg.LinearAPIKey, true); err == nil {
		cfg.LinearAPIKey = v
	}
	if v, err := prompt("GitHub Token", cfg.GitHubToken, true); err == nil {
		cfg.GitHubToken = v
	}
	if v, err := prompt("GitHub Repository (owner/repo)", cfg.GitHubRepo, false); err == nil {
		if err := cfg.Set("github_repo", v); err != nil {
			return err
		}
	}
	if v, err := prompt("GitHub Branch", cfg.GitHubBranch, false); err == nil {
		if err := cfg.Set("github_branch", v); err != nil {
			return err
		}
	}
	if v, err := prompt("Default GitHub Directory (--repo-path, empty for repo root)", cfg.RepoPath, false); err == nil {
		if err := cfg.Set("repo_path", v); err != nil {
			return err
		}
	}

	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "\nSaved configuration to %s\n", cfgPath)
	return nil
}

func (a *App) runConfigSet(args []string) error {
	fs := flag.NewFlagSet("config set", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	posArgs, err := parseInterspersedFlags(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(a.Stdout, "Usage: lsy config set <key> <value>")
			return nil
		}
		return err
	}
	if len(posArgs) != 2 {
		return errors.New("usage: lsy config set <key> <value>")
	}

	key, val := posArgs[0], posArgs[1]
	cfg, err := config.LoadRaw()
	if err != nil {
		return err
	}
	if err := cfg.Set(key, val); err != nil {
		return err
	}
	if err := config.Save(cfg); err != nil {
		return err
	}

	canonical := config.NormalizeKey(key)
	shown, _ := cfg.Get(canonical, false)
	fmt.Fprintf(a.Stdout, "Set %s = %s\n", canonical, shown)
	return nil
}

func (a *App) runConfigGet(args []string) error {
	fs := flag.NewFlagSet("config get", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var reveal bool
	fs.BoolVar(&reveal, "reveal", false, "Show unmasked secret values")

	posArgs, err := parseInterspersedFlags(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(a.Stdout, "Usage: lsy config get <key> [--reveal]")
			return nil
		}
		return err
	}
	if len(posArgs) != 1 {
		return errors.New("usage: lsy config get <key> [--reveal]")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	val, err := cfg.Get(posArgs[0], reveal)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, val)
	return nil
}

func (a *App) runConfigList(args []string) error {
	fs := flag.NewFlagSet("config list", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var reveal bool
	fs.BoolVar(&reveal, "reveal", false, "Show unmasked secret values")

	if _, err := parseInterspersedFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(a.Stdout, "Usage: lsy config list [--reveal]")
			return nil
		}
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfgPath, err := config.FilePath()
	if err != nil {
		return err
	}

	fmt.Fprintf(a.Stdout, "Config file: %s\n\n", cfgPath)
	tw := tabwriter.NewWriter(a.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tVALUE\tSOURCE")
	for _, key := range config.ValidKeys() {
		val, _ := cfg.Get(key, reveal)
		if val == "" {
			val = "(not set)"
		}
		src := "config.json"
		if env := config.EnvOverrideName(key); env != "" {
			src = "env:" + env
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", key, val, src)
	}
	return tw.Flush()
}
