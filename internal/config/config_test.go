package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitializeCreatesDefaultLayout(t *testing.T) {
	root := t.TempDir()
	paths := NewPaths(filepath.Join(root, "xoldot"))

	if err := Initialize(paths); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	for _, path := range []string{paths.Config, paths.Tools, paths.Aliases, paths.Skills} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	for _, path := range []string{paths.Profiles, paths.ManagedHome} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Errorf("expected directory %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(paths.Root, "bootstrap.sh")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("bootstrap.sh should not be generated: %v", err)
	}

	cfg, err := Load(paths.Config)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GitSettings().Enabled {
		t.Error("Git should be disabled by default")
	}
	if got := cfg.AliasSettings().Dir; got != "~/.aliases" {
		t.Errorf("alias dir = %q, want ~/.aliases", got)
	}
}

func TestInitializeDoesNotOverwriteExistingFiles(t *testing.T) {
	paths := NewPaths(t.TempDir())
	if err := os.WriteFile(paths.Config, []byte("custom = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Initialize(paths); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	data, err := os.ReadFile(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "custom = true\n" {
		t.Errorf("config overwritten: %q", got)
	}
}

func TestExpandHome(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "home", "test")
	got, err := ExpandHome("~/.aliases", home)
	if err != nil {
		t.Fatalf("ExpandHome() error = %v", err)
	}
	want := filepath.Join(home, ".aliases")
	if got != want {
		t.Errorf("ExpandHome() = %q, want %q", got, want)
	}
}

func TestLoadMigratesLegacySingletonArrays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xoldot.toml")
	legacy := `[[git]]
enabled = true
remote = "upstream"
branch = "trunk"

[[aliases]]
dir = "~/shell-aliases"
shells = ["zsh"]
`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.GitSettings(); !got.Enabled || got.Remote != "upstream" || got.Branch != "trunk" {
		t.Errorf("git settings = %#v", got)
	}
	if got := cfg.AliasSettings(); got.Dir != "~/shell-aliases" || len(got.Shells) != 1 || got.Shells[0] != "zsh" {
		t.Errorf("alias settings = %#v", got)
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[[git]]") || !strings.Contains(string(data), "[git]") {
		t.Errorf("saved config did not use singleton tables:\n%s", data)
	}
}

func TestLoadRejectsMultipleLegacySingletons(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xoldot.toml")
	data := `[[git]]
enabled = false

[[git]]
enabled = true
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "multiple git") {
		t.Fatalf("Load() error = %v, want multiple git error", err)
	}
}

func TestLoadRejectsUnknownFieldsAndDuplicateShells(t *testing.T) {
	for _, data := range []string{
		"unknown = true\n",
		"[aliases]\nshells = [\"bash\", \"bash\"]\n",
	} {
		path := filepath.Join(t.TempDir(), "xoldot.toml")
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("Load(%q) error = nil", data)
		}
	}
}
