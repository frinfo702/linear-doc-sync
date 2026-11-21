package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureInitializedAndLoadSave(t *testing.T) {
	tmpDir := t.TempDir()
	cfgDir := filepath.Join(tmpDir, ".config", "lsy")
	t.Setenv("LSY_CONFIG_DIR", cfgDir)
	t.Setenv("LINEAR_API_KEY", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("LSY_GITHUB_REPO", "")
	t.Setenv("LSY_GITHUB_BRANCH", "")
	t.Setenv("LSY_DOCS_DIR", "")

	dir, err := EnsureInitialized()
	if err != nil {
		t.Fatalf("EnsureInitialized failed: %v", err)
	}
	if dir != cfgDir {
		t.Fatalf("expected dir %s, got %s", cfgDir, dir)
	}

	if _, err := os.Stat(filepath.Join(cfgDir, "config.json")); err != nil {
		t.Fatalf("config.json not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "docs")); err != nil {
		t.Fatalf("docs dir not created: %v", err)
	}

	cfg, err := LoadRaw()
	if err != nil {
		t.Fatalf("LoadRaw failed: %v", err)
	}
	if cfg.GitHubBranch != DefaultBranch || cfg.RepoPath != DefaultRepoPath || !cfg.UseFrontmatter() {
		t.Fatalf("unexpected default config: %+v", cfg)
	}

	if err := cfg.Set("repo-path", "/content/posts/"); err != nil {
		t.Fatalf("Set repo-path failed: %v", err)
	}
	if err := cfg.Set("github_repo", "example/site"); err != nil {
		t.Fatalf("Set github_repo failed: %v", err)
	}
	if err := cfg.Set("frontmatter", "false"); err != nil {
		t.Fatalf("Set frontmatter failed: %v", err)
	}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.RepoPath != "content/posts" {
		t.Fatalf("expected repo_path content/posts, got %q", loaded.RepoPath)
	}
	if loaded.GitHubRepo != "example/site" {
		t.Fatalf("expected github_repo example/site, got %q", loaded.GitHubRepo)
	}
	if loaded.UseFrontmatter() {
		t.Fatalf("expected frontmatter=false")
	}

	// Verify env override takes precedence.
	t.Setenv("LSY_REPO_PATH", "custom/blog")
	loaded, err = Load()
	if err != nil {
		t.Fatalf("Load with env failed: %v", err)
	}
	if loaded.RepoPath != "custom/blog" {
		t.Fatalf("expected env override custom/blog, got %q", loaded.RepoPath)
	}
}

func TestValidationAndMasking(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Set("github_repo", "not-valid-repo"); err == nil {
		t.Fatalf("expected error for invalid github_repo")
	}
	if err := cfg.Set("unknown_key", "value"); err == nil {
		t.Fatalf("expected error for unknown key")
	}
	if got := MaskSecret("lin_api_1234567890"); got != "lin_**********7890" {
		t.Fatalf("unexpected mask: %q", got)
	}
	if got := NormalizeRepoPath("./"); got != "" {
		t.Fatalf("expected root path to normalize to empty string, got %q", got)
	}
}
