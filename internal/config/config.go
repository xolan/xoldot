package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	ConfigDirEnv  = "XOLDOT_CONFIG_HOME"
	TargetHomeEnv = "XOLDOT_TARGET_HOME"
)

type Paths struct {
	Root        string
	Config      string
	Tools       string
	Aliases     string
	Skills      string
	Profiles    string
	ManagedHome string
	Bootstrap   string
}

type Config struct {
	Git     []GitConfig         `toml:"git"`
	Aliases []AliasOutputConfig `toml:"aliases"`
}

type GitConfig struct {
	Enabled bool   `toml:"enabled"`
	Remote  string `toml:"remote,omitempty"`
	Branch  string `toml:"branch,omitempty"`
}

type AliasOutputConfig struct {
	Dir    string   `toml:"dir"`
	Shells []string `toml:"shells"`
}

func Default() Config {
	return Config{
		Git: []GitConfig{{
			Enabled: false,
			Remote:  "origin",
			Branch:  "main",
		}},
		Aliases: []AliasOutputConfig{{
			Dir:    "~/.aliases",
			Shells: []string{"bash", "zsh", "fish"},
		}},
	}
}

func DefaultRoot() (string, error) {
	if root := os.Getenv(ConfigDirEnv); root != "" {
		return filepath.Abs(root)
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(base, "xoldot"), nil
}

func TargetHome() (string, error) {
	if home := os.Getenv(TargetHomeEnv); home != "" {
		return filepath.Abs(home)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home: %w", err)
	}
	return home, nil
}

func NewPaths(root string) Paths {
	return Paths{
		Root:        root,
		Config:      filepath.Join(root, "xoldot.toml"),
		Tools:       filepath.Join(root, "tools.toml"),
		Aliases:     filepath.Join(root, "files", "aliases.toml"),
		Skills:      filepath.Join(root, "skills.toml"),
		Profiles:    filepath.Join(root, "profiles"),
		ManagedHome: filepath.Join(root, "files", "home"),
		Bootstrap:   filepath.Join(root, "bootstrap.sh"),
	}
}

func Initialize(paths Paths) error {
	for _, dir := range []string{paths.Root, paths.Profiles, paths.ManagedHome, filepath.Dir(paths.Aliases)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	defaultConfig, err := toml.Marshal(Default())
	if err != nil {
		return fmt.Errorf("encode default configuration: %w", err)
	}
	if err := writeIfMissing(paths.Config, defaultConfig, 0o644); err != nil {
		return err
	}
	if err := writeIfMissing(paths.Tools, []byte(defaultToolsTOML), 0o644); err != nil {
		return err
	}
	if err := writeIfMissing(paths.Aliases, []byte(defaultAliasesTOML), 0o644); err != nil {
		return err
	}
	if err := writeIfMissing(paths.Skills, []byte(defaultSkillsTOML), 0o644); err != nil {
		return err
	}
	if err := writeIfMissing(paths.Bootstrap, []byte(bootstrapScript), 0o755); err != nil {
		return err
	}
	return nil
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("configuration not found; run 'xoldot setup'")
		}
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.setDefaults()
	return cfg, nil
}

func Save(path string, cfg Config) error {
	cfg.setDefaults()
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return WriteFile(path, data, 0o644)
}

func (cfg *Config) GitSettings() *GitConfig {
	cfg.setDefaults()
	return &cfg.Git[0]
}

func (cfg *Config) AliasSettings() *AliasOutputConfig {
	cfg.setDefaults()
	return &cfg.Aliases[0]
}

func (cfg *Config) setDefaults() {
	defaults := Default()
	if len(cfg.Git) == 0 {
		cfg.Git = defaults.Git
	}
	if cfg.Git[0].Remote == "" {
		cfg.Git[0].Remote = defaults.Git[0].Remote
	}
	if cfg.Git[0].Branch == "" {
		cfg.Git[0].Branch = defaults.Git[0].Branch
	}
	if len(cfg.Aliases) == 0 {
		cfg.Aliases = defaults.Aliases
	}
	if cfg.Aliases[0].Dir == "" {
		cfg.Aliases[0].Dir = defaults.Aliases[0].Dir
	}
	if len(cfg.Aliases[0].Shells) == 0 {
		cfg.Aliases[0].Shells = defaults.Aliases[0].Shells
	}
}

func ExpandHome(path, home string) (string, error) {
	switch {
	case path == "~":
		return home, nil
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	case strings.HasPrefix(path, "~"):
		return "", fmt.Errorf("only '~' and '~/' home expansion are supported: %s", path)
	case filepath.IsAbs(path):
		return filepath.Clean(path), nil
	default:
		return filepath.Join(home, path), nil
	}
}

func WriteFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", path, err)
	}

	temp, err := os.CreateTemp(filepath.Dir(path), ".xoldot-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary file for %s: %w", path, err)
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set permissions on temporary file for %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary file for %s: %w", path, err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func writeIfMissing(path string, data []byte, mode os.FileMode) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	return WriteFile(path, data, mode)
}

const defaultToolsTOML = `# Add entries with: xoldot tool add <name>
`

const defaultAliasesTOML = `# Add entries with: xoldot alias add <name> <command>
`

const defaultSkillsTOML = `# Add entries with: xoldot skill add <name>@<owner>/<repo>
`

const bootstrapScript = `#!/bin/sh
set -eu

if ! command -v xoldot >/dev/null 2>&1; then
  echo "xoldot is not installed or not on PATH" >&2
  exit 1
fi

exec xoldot apply
`
