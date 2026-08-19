package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitializeCreatesDefaultLayout(t *testing.T) {
	root := t.TempDir()
	paths := NewPaths(filepath.Join(root, "xoldot"))

	if err := Initialize(paths); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	for _, path := range []string{paths.Config, paths.Tools, paths.Aliases, paths.Bootstrap} {
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
