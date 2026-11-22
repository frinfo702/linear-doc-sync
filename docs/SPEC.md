# `lsy` — Linear Doc Sync Specification

## 1. Overview

`lsy` (**L**inear doc **SY**nc) is a standalone command-line tool that fetches documents from [Linear](https://linear.app/) via the Linear GraphQL API, stores local Markdown clones, and publishes/unpublishes them to a GitHub repository via the GitHub REST Contents API.

Because documents are published as plain Markdown files (with optional YAML frontmatter) into a configurable path inside any GitHub repository, downstream web applications (Next.js, Astro, Hugo, Docusaurus, custom static/SSR sites, etc.) can render Linear documents with full control over presentation.

## 2. Implementation Architecture

- **Language**: Go (standard library only, zero external runtime or build dependencies)
- **Distribution**:
  - Single static binary (`CGO_ENABLED=0`) cross-compiled for `darwin/arm64`, `darwin/amd64`, `linux/arm64`, `linux/amd64`
  - **Homebrew**: via `Formula/lsy.rb` (supports direct repo tap and optional `homebrew-tap` repo)
  - **curl installer**: `install.sh` downloads the matching release tarball, verifies its SHA256 checksum, installs `lsy` to PATH, and creates `~/.config/lsy`
- **External APIs**:
  - **Linear GraphQL API** (`https://api.linear.app/graphql`): resolves documents by URL, `slugId`, UUID, or title/name
  - **GitHub REST Contents API** (`https://api.github.com/repos/{owner}/{repo}/contents/{path}`): creates, updates, and deletes Markdown files directly in the configured branch without requiring a local git clone

## 3. Configuration & Local Storage

### Directory Layout

By default, all configuration, state, and local document clones live under `~/.config/lsy` (overridable via `LSY_CONFIG_DIR`). Both the `install.sh` installer and every `lsy` invocation ensure this directory structure exists automatically.

```text
~/.config/lsy/
├── config.json   # Configuration (0600 permissions)
├── state.json    # Tracked documents metadata & publish status (0600 permissions)
└── docs/         # Local Markdown clones of fetched Linear documents
    ├── <slug>.md
    └── images/
        └── <slug>/
            └── <image>-<hash>.<ext>
```

### `config.json` Schema

```json
{
  "linear_api_key": "",
  "github_token": "",
  "github_repo": "owner/repo",
  "github_branch": "main",
  "repo_path": "docs",
  "docs_dir": "",
  "frontmatter": true,
  "with_image": false
}
```

| Key | Type | Default | Env Override | Description |
| --- | --- | --- | --- | --- |
| `linear_api_key` | string | `""` | `LINEAR_API_KEY` | Linear Personal API Key (`lin_api_...`) or `Bearer <oauth_token>` |
| `github_token` | string | `""` | `GITHUB_TOKEN` | GitHub PAT with `repo` or `contents:write` permission |
| `github_repo` | string | `""` | `LSY_GITHUB_REPO` | Target GitHub repository (`owner/repo`) |
| `github_branch` | string | `"main"` | `LSY_GITHUB_BRANCH` | Target GitHub branch |
| `repo_path` | string | `"docs"` | `LSY_REPO_PATH` | Default directory path inside the GitHub repository (`""` for repo root) |
| `docs_dir` | string | `~/.config/lsy/docs` | `LSY_DOCS_DIR` | Local directory for cloned Markdown files |
| `frontmatter` | bool | `true` | — | Prepend YAML frontmatter (`title`, `linear_id`, `slug`, `linear_url`, `updated_at`) |
| `with_image` | bool | `false` | `LSY_WITH_IMAGE` | Download images locally and upload them to GitHub with rewritten relative links |

Environment variables take precedence over values stored in `config.json`.

### `state.json` Schema

Tracks every fetched document so `unpublish`, `list`, `rm`, and re-`publish` work seamlessly by URL, slug, filename, or title:

```json
{
  "documents": [
    {
      "linear_id": "uuid",
      "slug_id": "a1b2c3d4e5f6",
      "slug": "document-name",
      "title": "Document Name",
      "linear_url": "https://linear.app/workspace/document/document-name-a1b2c3d4e5f6",
      "local_path": "/Users/you/.config/lsy/docs/document-name.md",
      "github_repo": "owner/repo",
      "github_branch": "main",
      "remote_path": "docs/document-name.md",
      "remote_sha": "abc123...",
      "status": "published",
      "updated_at": "2026-01-01T00:00:00Z",
      "published_at": "2026-01-01T00:00:00Z"
    }
  ]
}
```

## 4. Command Reference

### Global Flags

- `-h`, `--help`: Show usage help
- `-v`, `--version`: Print version, commit hash, and build timestamp

### `lsy publish <url-or-name> [flags]`

Fetches a Linear document, saves/updates its local Markdown clone, and pushes it to GitHub.

- **Argument**: `<url-or-name>` — Linear document URL (e.g. `https://linear.app/org/document/doc-name-abc123`), `slugId`, UUID, document title, or previously tracked slug/name.
- **Flags**:
  - `--repo <owner/repo>`: Override configured `github_repo`
  - `--branch <branch>`: Override configured `github_branch`
  - `--repo-path <dir>`: Override default directory path in the GitHub repository
  - `--filename <name>`: Override Markdown filename (default: `<slug>.md`)
  - `--message <msg>`: Custom Git commit message (default: `docs(lsy): publish <title>`)
  - `--no-frontmatter`: Do not prepend YAML frontmatter
  - `--with-image`: Download images locally to `images/<slug>/`, rewrite links to `./images/<slug>/<file>`, and push them to GitHub (overrides `with_image` config)
  - `--local-only`: Fetch and save locally without pushing to GitHub

### `lsy fetch [url-or-name] [flags]` (alias: `pull`)

Fetches document(s) from Linear and updates local Markdown clones and `state.json` without pushing to GitHub. When no argument is provided, refreshes all tracked documents in `state.json`. If local modifications are detected (compared against the last synced `local_hash`), prompts the user to choose whether to overwrite the local file or keep local changes.

- **Argument**: `[url-or-name]` (optional) — Linear document URL, `slugId`, UUID, title, or tracked name/slug.
- **Flags**:
  - `--all`: Fetch all tracked documents (default when no argument is given)
  - `--with-image`: Download images locally and rewrite Markdown links (overrides `with_image` config)
  - `--filename <name>`: Override Markdown filename (single document only)
  - `--no-frontmatter`: Do not prepend YAML frontmatter
  - `-f`, `--force`: Overwrite local changes without prompting
  - `--keep-local`: Keep local changes without prompting (skip modified files)

### `lsy unpublish <url-or-name> [flags]`

Deletes the published Markdown file from GitHub while retaining the local Markdown clone and marking the document as `unpublished` in `state.json`.

- **Argument**: `<url-or-name>` — URL, slug, filename, Linear ID, or document title.
- **Flags**:
  - `--repo <owner/repo>`: Override GitHub repository
  - `--branch <branch>`: Override GitHub branch
  - `--repo-path <dir>`: Override GitHub directory path (used when document is not in local state)
  - `--message <msg>`: Custom Git commit message (default: `docs(lsy): unpublish <title>`)

### `lsy toggle [flags]` (alias: `switch`)

Launches an interactive terminal checklist (`[x]` = published, `[ ]` = private) for locally tracked documents so you can switch multiple documents between published and private (unpublished) and press Enter to apply all changes at once.

- **Controls**:
  - `↑` / `↓` (or `j` / `k`): Move cursor
  - `Space` (or `x` / `←` / `→`): Toggle `[ ]` / `[x]`
  - `a`: Toggle all documents
  - `r`: Reset selections to initial state
  - `Enter`: Confirm and apply changes to GitHub and `state.json`
  - `q` / `Esc` / `Ctrl+C`: Cancel without making changes
- **Flags**:
  - `--repo <owner/repo>`: Override GitHub repository
  - `--branch <branch>`: Override GitHub branch
  - `--repo-path <dir>`: Override GitHub directory path
  - `--message <msg>`: Custom Git commit message
  - `--fetch`: Refresh newly published documents from Linear before pushing to GitHub
  - `--with-image`: Download images locally when `--fetch` is enabled (overrides config)
  - `--no-frontmatter`: Omit YAML frontmatter when `--fetch` is enabled
  - `--published`: Show only currently published documents
  - `--unpublished`: Show only currently private (unpublished) documents
  - `--dry-run`: Preview planned changes after confirmation without modifying GitHub or `state.json`

### `lsy list [flags]` (alias: `ls`)

Lists locally tracked documents and their publish status.

- **Flags**:
  - `--published`: Show only published documents
  - `--unpublished`: Show only unpublished documents
  - `-i`, `--interactive`: Launch interactive publish/private toggle view
  - `--json`: Output machine-readable JSON

### `lsy rm <url-or-name> [flags]`

Removes the local Markdown file and deletes its tracking record from `state.json`. By default, does **not** remove the file from GitHub.

- **Argument**: `<url-or-name>` — URL, slug, filename, Linear ID, or document title.
- **Flags**:
  - `--unpublish`: Also delete the file from GitHub before removing locally
  - `-f`, `--force`: Ignore missing local file on disk

### `lsy config [subcommand]`

Manages `~/.config/lsy/config.json`.

- `lsy config` / `lsy config list [--reveal]`: Print all config keys, effective values, and env-var override indicators (secrets redacted unless `--reveal`)
- `lsy config init`: Interactive setup wizard for Linear API key, GitHub token, repository, branch, and default `repo_path`
- `lsy config set <key> <value>`: Update a config value (accepts both `snake_case` and `kebab-case`, e.g. `repo-path` or `repo_path`)
- `lsy config get <key> [--reveal]`: Print a single config value
- `lsy config path`: Print the path to `config.json`

### `lsy version`

Prints build version metadata.

## 5. Document Resolution Rules

When resolving `<url-or-name>` against Linear:
1. If the input matches a tracked document in local `state.json` (by URL, `slug_id`, `slug`, `linear_id`, filename, or title), its stored `linear_id` / `slug_id` is tried first.
2. If the input is a Linear URL (`https://linear.app/<workspace>/document/<segment>`):
   - Extract `<segment>` (stripping query strings, fragments, and trailing slashes).
   - If `<segment>` contains `-`, try the trailing token as `slugId` via `document(id: $id)` first, then try the full `<segment>` as `slugId`/ID, then fall back to querying workspace documents and matching by slugified title.
3. Otherwise:
   - Try `document(id: $id)` directly (handles UUIDs and `slugId`s).
   - Fall back to filtering workspace documents by exact title, case-insensitive title, or slugified title.

## 6. CI/CD & Release Workflow

### Continuous Integration (`.github/workflows/ci.yml`)
Runs on every push to `main` and every pull request:
- Code formatting verification (`gofmt`)
- Static analysis & type checking (`go vet ./...` & `go build ./...`)
- Unit & integration test suite with race detector (`go test -race -cover ./...`)
- Installer script syntax check (`sh -n install.sh`)
- Cross-compilation verification matrix (`darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`)

### Manual Release Workflow (`.github/workflows/release.yml`)
Triggered via **GitHub Actions → Release → Run workflow**:
1. Prompts with a dropdown choice `bump`: `patch` | `minor` | `major`.
2. Runs verification (`go vet`, `go test -race ./...`).
3. Computes the next semantic version tag (`vX.Y.Z`) from the latest git tag (starting at `v0.0.0` if none exist).
4. Builds stripped static release tarballs (`lsy_<version>_<os>_<arch>.tar.gz`) for macOS (arm64, amd64) and Linux (arm64, amd64) with version metadata injected via `-ldflags`, plus `checksums.txt`.
5. Tags the release commit, pushes the tag, and publishes a GitHub Release with all binaries and `checksums.txt`.
6. Updates `Formula/lsy.rb` with the new version and SHA256 checksums and commits it to `main` (and optionally mirrors it to a dedicated `homebrew-tap` repo if `HOMEBREW_TAP_TOKEN` is configured).
