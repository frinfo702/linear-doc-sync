# Linear doc sync (`lsy`)

CLI to publish documents from [Linear](https://linear.app/) to GitHub as Markdown files.

## Installation

### Homebrew

```bash
brew install frinfo702/tap/lsy
```

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

You can also provide credentials via environment variables (`LINEAR_API_KEY`, `GITHUB_TOKEN`, `LSY_GITHUB_REPO`, `LSY_GITHUB_BRANCH`, `LSY_REPO_PATH`, `LSY_WITH_IMAGE`).

## Usage

```bash
# Publish a Linear document (clones locally to ~/.config/lsy/docs and pushes to GitHub)
lsy publish https://linear.app/<your-project>/document/document-name

# Publish with images downloaded locally and uploaded to GitHub so Linear images render on GitHub
lsy publish https://linear.app/<your-project>/document/document-name --with-image

# Or enable image downloading by default in config
lsy config set with-image true

# Publish to a custom directory in GitHub for a single command
lsy publish https://linear.app/<your-project>/document/document-name --repo-path blog/posts

# Fetch/refresh tracked document(s) from Linear and update local state without pushing to GitHub
# (prompts before overwriting if local changes are detected)
lsy fetch
lsy fetch <url or document-name> --with-image
lsy fetch --force        # overwrite local changes without prompting
lsy fetch --keep-local   # skip files with local changes without prompting

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
2. When `--with-image` (or `with_image: true` in config) is enabled, `lsy` downloads authenticated Linear images (`uploads.linear.app`) to `~/.config/lsy/docs/images/<slug>/`, rewrites image links to relative `./images/<slug>/<file>` paths, and uploads those images alongside the Markdown file in GitHub so they render properly without Linear authentication.
3. It then publishes (creates or updates) the Markdown file directly to your configured GitHub repository via the GitHub Contents API at `<repo-path>/<slug>.md`.
4. Use `lsy fetch` (alias `pull`) to pull the latest updates from Linear into local storage and `state.json` without pushing to GitHub; if local edits are detected, `lsy` lets you choose whether to overwrite or keep your local changes.
5. Your downstream web app (Next.js, Astro, Hugo, Docusaurus, etc.) can render the Markdown files however you like.
6. Use `--repo-path` per command or `lsy config set repo-path <dir>` to control the destination directory in GitHub.
