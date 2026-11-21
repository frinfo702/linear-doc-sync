package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultBranch   = "main"
	DefaultRepoPath = "docs"
)

// Config represents user settings persisted in ~/.config/lsy/config.json.
type Config struct {
	LinearAPIKey string `json:"linear_api_key"`
	GitHubToken  string `json:"github_token"`
	GitHubRepo   string `json:"github_repo"`
	GitHubBranch string `json:"github_branch"`
	RepoPath     string `json:"repo_path"`
	DocsDir      string `json:"docs_dir"`
	Frontmatter  *bool  `json:"frontmatter"`
	WithImage    bool   `json:"with_image"`
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() Config {
	fm := true
	return Config{
		GitHubBranch: DefaultBranch,
		RepoPath:     DefaultRepoPath,
		Frontmatter:  &fm,
	}
}

// Dir returns the lsy configuration directory (~/.config/lsy or $LSY_CONFIG_DIR).
func Dir() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("LSY_CONFIG_DIR")); custom != "" {
		return custom, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "lsy"), nil
}

// FilePath returns the path to config.json inside the config directory.
func FilePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// StateFilePath returns the path to state.json inside the config directory.
func StateFilePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.json"), nil
}

// EnsureInitialized creates ~/.config/lsy, ~/.config/lsy/docs, and a default
// config.json if they do not yet exist.
func EnsureInitialized() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config directory %s: %w", dir, err)
	}
	docsDir := filepath.Join(dir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		return "", fmt.Errorf("create docs directory %s: %w", docsDir, err)
	}

	cfgPath := filepath.Join(dir, "config.json")
	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		if err := Save(DefaultConfig()); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", fmt.Errorf("stat config file %s: %w", cfgPath, err)
	}
	return dir, nil
}

// LoadRaw reads config.json from disk without applying environment variable overrides.
func LoadRaw() (Config, error) {
	if _, err := EnsureInitialized(); err != nil {
		return Config{}, err
	}
	cfgPath, err := FilePath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", cfgPath, err)
	}

	cfg := DefaultConfig()
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", cfgPath, err)
		}
	}
	if cfg.Frontmatter == nil {
		fm := true
		cfg.Frontmatter = &fm
	}
	if strings.TrimSpace(cfg.GitHubBranch) == "" {
		cfg.GitHubBranch = DefaultBranch
	}
	return cfg, nil
}

// Load reads config.json and overlays environment variable overrides.
func Load() (Config, error) {
	cfg, err := LoadRaw()
	if err != nil {
		return Config{}, err
	}
	if v := strings.TrimSpace(os.Getenv("LINEAR_API_KEY")); v != "" {
		cfg.LinearAPIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); v != "" {
		cfg.GitHubToken = v
	}
	if v := strings.TrimSpace(os.Getenv("LSY_GITHUB_REPO")); v != "" {
		cfg.GitHubRepo = v
	}
	if v := strings.TrimSpace(os.Getenv("LSY_GITHUB_BRANCH")); v != "" {
		cfg.GitHubBranch = v
	}
	if v, ok := os.LookupEnv("LSY_REPO_PATH"); ok {
		cfg.RepoPath = NormalizeRepoPath(v)
	} else {
		cfg.RepoPath = NormalizeRepoPath(cfg.RepoPath)
	}
	if v := strings.TrimSpace(os.Getenv("LSY_DOCS_DIR")); v != "" {
		cfg.DocsDir = v
	}
	if v := strings.TrimSpace(os.Getenv("LSY_WITH_IMAGE")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid boolean for LSY_WITH_IMAGE: %q (use true or false)", v)
		}
		cfg.WithImage = b
	}
	return cfg, nil
}

// Save writes config.json atomically with 0600 permissions.
func Save(cfg Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", dir, err)
	}
	if cfg.Frontmatter == nil {
		fm := true
		cfg.Frontmatter = &fm
	}
	cfg.RepoPath = NormalizeRepoPath(cfg.RepoPath)

	cfgPath := filepath.Join(dir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')

	tmpPath := cfgPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write temp config %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, cfgPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("save config %s: %w", cfgPath, err)
	}
	return nil
}

// EffectiveDocsDir returns the directory where local markdown clones are stored.
func (c Config) EffectiveDocsDir() (string, error) {
	if strings.TrimSpace(c.DocsDir) != "" {
		return expandHome(strings.TrimSpace(c.DocsDir))
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "docs"), nil
}

// UseFrontmatter reports whether YAML frontmatter should be prepended.
func (c Config) UseFrontmatter() bool {
	if c.Frontmatter == nil {
		return true
	}
	return *c.Frontmatter
}

// NormalizeKey maps both kebab-case and snake_case config keys to canonical names.
func NormalizeKey(key string) string {
	k := strings.ToLower(strings.TrimSpace(key))
	k = strings.ReplaceAll(k, "-", "_")
	switch k {
	case "repo", "github_repository":
		return "github_repo"
	case "branch":
		return "github_branch"
	case "path":
		return "repo_path"
	case "with_images", "images":
		return "with_image"
	default:
		return k
	}
}

// ValidKeys returns the ordered list of supported configuration keys.
func ValidKeys() []string {
	return []string{
		"linear_api_key",
		"github_token",
		"github_repo",
		"github_branch",
		"repo_path",
		"docs_dir",
		"frontmatter",
		"with_image",
	}
}

// Set updates a single key on Config.
func (c *Config) Set(key, value string) error {
	switch NormalizeKey(key) {
	case "linear_api_key":
		c.LinearAPIKey = strings.TrimSpace(value)
	case "github_token":
		c.GitHubToken = strings.TrimSpace(value)
	case "github_repo":
		v := strings.TrimSpace(value)
		if v != "" && !isValidRepo(v) {
			return fmt.Errorf("invalid github_repo %q: expected owner/repo format", v)
		}
		c.GitHubRepo = v
	case "github_branch":
		v := strings.TrimSpace(value)
		if v == "" {
			v = DefaultBranch
		}
		c.GitHubBranch = v
	case "repo_path":
		c.RepoPath = NormalizeRepoPath(value)
	case "docs_dir":
		c.DocsDir = strings.TrimSpace(value)
	case "frontmatter":
		b, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("invalid boolean for frontmatter: %q (use true or false)", value)
		}
		c.Frontmatter = &b
	case "with_image":
		b, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("invalid boolean for with_image: %q (use true or false)", value)
		}
		c.WithImage = b
	default:
		return fmt.Errorf("unknown config key %q (valid keys: %s)", key, strings.Join(ValidKeys(), ", "))
	}
	return nil
}

// Get retrieves a config value by key. Secrets are masked unless reveal is true.
func (c Config) Get(key string, reveal bool) (string, error) {
	switch NormalizeKey(key) {
	case "linear_api_key":
		if !reveal {
			return MaskSecret(c.LinearAPIKey), nil
		}
		return c.LinearAPIKey, nil
	case "github_token":
		if !reveal {
			return MaskSecret(c.GitHubToken), nil
		}
		return c.GitHubToken, nil
	case "github_repo":
		return c.GitHubRepo, nil
	case "github_branch":
		return c.GitHubBranch, nil
	case "repo_path":
		return c.RepoPath, nil
	case "docs_dir":
		return c.DocsDir, nil
	case "frontmatter":
		return strconv.FormatBool(c.UseFrontmatter()), nil
	case "with_image":
		return strconv.FormatBool(c.WithImage), nil
	default:
		return "", fmt.Errorf("unknown config key %q (valid keys: %s)", key, strings.Join(ValidKeys(), ", "))
	}
}

// EnvOverrideName returns the environment variable name overriding key if currently set.
func EnvOverrideName(key string) string {
	var envName string
	switch NormalizeKey(key) {
	case "linear_api_key":
		envName = "LINEAR_API_KEY"
	case "github_token":
		envName = "GITHUB_TOKEN"
	case "github_repo":
		envName = "LSY_GITHUB_REPO"
	case "github_branch":
		envName = "LSY_GITHUB_BRANCH"
	case "repo_path":
		envName = "LSY_REPO_PATH"
	case "docs_dir":
		envName = "LSY_DOCS_DIR"
	case "with_image":
		envName = "LSY_WITH_IMAGE"
	default:
		return ""
	}
	if v, ok := os.LookupEnv(envName); ok && (envName == "LSY_REPO_PATH" || strings.TrimSpace(v) != "") {
		return envName
	}
	return ""
}

// NormalizeRepoPath cleans a repository directory path into forward-slash relative form.
// Root paths ("" or "." or "/") normalize to "".
func NormalizeRepoPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return ""
	}
	parts := strings.Split(p, "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		clean = append(clean, part)
	}
	return strings.Join(clean, "/")
}

// MaskSecret redacts sensitive tokens for display.
func MaskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return strings.Repeat("*", len(s))
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}

func isValidRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	return len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != ""
}

func expandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}
