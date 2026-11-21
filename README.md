# Linear doc sync (`lsy`)

CLI to publish documents from [Linear](https://linear.app/) to GitHub as Markdown files.

See [docs/SPEC.md](docs/SPEC.md) for the full specification.

## Installation

### Homebrew

Install directly from this repository:

```bash
brew tap frinfo702/lsy https://github.com/frinfo702/linear-doc-sync
brew install frinfo702/lsy/lsy
```

*(If you also mirror the formula to `frinfo702/homebrew-tap` via `HOMEBREW_TAP_TOKEN`, users can install via `brew install frinfo702/tap/lsy`.)*

### curl (prebuilt binary)

```bash
curl -fsSL https://raw.githubusercontent.com/frinfo702/linear-doc-sync/main/install.sh | sh
```

You can pin a version or install directory with environment variables:

```bash
VERSION=v0.1.0 INSTALL_DIR=/usr/local/bin \
  curl -fsSL https://raw.githubusercontent.com/frinfo702/linear-doc-sync/main/install.sh | sh
```

### Configuration directory

After installation (and automatically on first run), `lsy` initializes `~/.config/lsy`:

```text
~/.config/lsy/
├── config.json   # API keys, target GitHub repo/branch, default repo_path
├── state.json    # Tracked local & published documents
└── docs/         # Local Markdown clones of Linear documents
```

## Quick Start

Configure your Linear API key, GitHub token, repository, and default destination directory:

```bash
# Interactive setup
lsy config init

# Or set individual keys
lsy config set linear_api_key lin_api_...
lsy config set github_token ghp_...
lsy config set github_repo owner/repo
lsy config set repo-path content/docs
```

You can also provide credentials via environment variables (`LINEAR_API_KEY`, `GITHUB_TOKEN`, `LSY_GITHUB_REPO`, `LSY_GITHUB_BRANCH`, `LSY_REPO_PATH`).

## Usage

```bash
# Publish a Linear document (clones locally to ~/.config/lsy/docs and pushes to GitHub)
lsy publish https://linear.app/uyuthropic/document/document-name

# Publish to a custom directory in GitHub for a single command
lsy publish https://linear.app/uyuthropic/document/document-name --repo-path blog/posts

# Unpublish a document from GitHub (keeps the local Markdown copy)
lsy unpublish <url or document-name>

# List all locally tracked documents and their publish status
lsy list

# Delete a document from local storage (pass --unpublish to also delete from GitHub)
lsy rm <url or document-name>

# View or update configuration
lsy config list
lsy config set repo-path docs/guides
```

## How it works

1. When you run `lsy publish`, `lsy` fetches the Linear document via the Linear GraphQL API and saves a local Markdown clone (with optional YAML frontmatter) in `~/.config/lsy/docs/<slug>.md`.
2. It then publishes (creates or updates) that Markdown file directly to your configured GitHub repository via the GitHub Contents API at `<repo-path>/<slug>.md`.
3. Your downstream web app (Next.js, Astro, Hugo, Docusaurus, etc.) can render the Markdown files however you like.
4. Use `--repo-path` per command or `lsy config set repo-path <dir>` to control the destination directory in GitHub.

## Releasing (GitHub Actions)

1. Go to **Actions → Release → Run workflow** in GitHub.
2. Select which version part to bump (`patch`, `minor`, or `major`) and click **Run workflow**.
3. The workflow automatically:
   - Runs formatting, `go vet`, and `go test -race`
   - Computes the next semantic version tag (`vX.Y.Z`)
   - Cross-compiles static binaries for macOS (`arm64`, `amd64`) and Linux (`arm64`, `amd64`) + `checksums.txt`
   - Tags the commit and publishes a GitHub Release
   - Updates `Formula/lsy.rb` with the new version and SHA256 checksums (and mirrors to `homebrew-tap` if `HOMEBREW_TAP_TOKEN` is set)
