package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	Scripts     string
	ManagedHome string
}

type Config struct {
	Git     GitConfig         `toml:"git"`
	Aliases AliasOutputConfig `toml:"aliases"`
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
		Git: GitConfig{
			Enabled: false,
			Remote:  "origin",
			Branch:  "main",
		},
		Aliases: AliasOutputConfig{
			Dir:    "~/.aliases",
			Shells: []string{"bash", "zsh", "fish"},
		},
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
		Scripts:     filepath.Join(root, "scripts"),
		ManagedHome: filepath.Join(root, "files", "home"),
	}
}

func Initialize(paths Paths) error {
	for _, dir := range []string{
		paths.Root,
		paths.Profiles,
		filepath.Join(paths.Scripts, "before-apply"),
		filepath.Join(paths.Scripts, "after-apply"),
		paths.ManagedHome,
		filepath.Dir(paths.Aliases),
	} {
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

	cfg, err := decode(data)
	if err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.setDefaults()
	if err := validate(cfg); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return cfg, nil
}

func validate(cfg Config) error {
	cfg.setDefaults()
	if strings.TrimSpace(cfg.Git.Remote) == "" {
		return fmt.Errorf("git remote cannot be empty")
	}
	if strings.TrimSpace(cfg.Git.Branch) == "" {
		return fmt.Errorf("git branch cannot be empty")
	}
	if strings.TrimSpace(cfg.Aliases.Dir) == "" {
		return fmt.Errorf("alias output directory cannot be empty")
	}

	seen := make(map[string]struct{}, len(cfg.Aliases.Shells))
	for _, shell := range cfg.Aliases.Shells {
		if _, exists := seen[shell]; exists {
			return fmt.Errorf("alias shell %q is duplicated", shell)
		}
		seen[shell] = struct{}{}
	}
	return nil
}

func Save(path string, cfg Config) error {
	cfg.setDefaults()
	if err := validate(cfg); err != nil {
		return fmt.Errorf("validate %s: %w", path, err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return WriteFile(path, data, 0o644)
}

func (cfg *Config) GitSettings() *GitConfig {
	cfg.setDefaults()
	return &cfg.Git
}

func (cfg *Config) AliasSettings() *AliasOutputConfig {
	cfg.setDefaults()
	return &cfg.Aliases
}

func (cfg *Config) setDefaults() {
	defaults := Default()
	if cfg.Git.Remote == "" {
		cfg.Git.Remote = defaults.Git.Remote
	}
	if cfg.Git.Branch == "" {
		cfg.Git.Branch = defaults.Git.Branch
	}
	if cfg.Aliases.Dir == "" {
		cfg.Aliases.Dir = defaults.Aliases.Dir
	}
	if len(cfg.Aliases.Shells) == 0 {
		cfg.Aliases.Shells = defaults.Aliases.Shells
	}
}

func decode(data []byte) (Config, error) {
	var document map[string]any
	if err := toml.Unmarshal(data, &document); err != nil {
		return Config{}, err
	}
	for _, section := range []string{"git", "aliases"} {
		if err := normalizeSingleton(document, section); err != nil {
			return Config{}, err
		}
	}
	normalized, err := toml.Marshal(document)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	decoder := toml.NewDecoder(bytes.NewReader(normalized))
	decoder.DisallowUnknownFields()
	return cfg, decoder.Decode(&cfg)
}

func normalizeSingleton(document map[string]any, section string) error {
	value, exists := document[section]
	if !exists || value == nil {
		return nil
	}
	array := reflect.ValueOf(value)
	if array.Kind() != reflect.Slice {
		return nil
	}
	if array.Len() > 1 {
		return fmt.Errorf("multiple %s settings are not supported", section)
	}
	if array.Len() == 0 {
		delete(document, section)
		return nil
	}
	document[section] = array.Index(0).Interface()
	return nil
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
