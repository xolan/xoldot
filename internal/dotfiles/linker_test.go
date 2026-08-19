package dotfiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinkCreatesAndKeepsManagedLinks(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "config", "files", "home")
	home := filepath.Join(root, "home")
	source := filepath.Join(managed, ".config", "app", "config.toml")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Link(managed, home, filepath.Join(root, "config"))
	if err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	if result.Created != 1 {
		t.Errorf("created = %d, want 1", result.Created)
	}
	target := filepath.Join(home, ".config", "app", "config.toml")
	destination, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}
	if destination != source {
		t.Errorf("link destination = %q, want %q", destination, source)
	}
	for _, directory := range []string{
		filepath.Join(home, ".config"),
		filepath.Join(home, ".config", "app"),
	} {
		info, err := os.Lstat(directory)
		if err != nil {
			t.Fatalf("Lstat(%s) error = %v", directory, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s mode = %v, want an ordinary directory", directory, info.Mode())
		}
	}

	result, err = Link(managed, home, filepath.Join(root, "config"))
	if err != nil {
		t.Fatalf("second Link() error = %v", err)
	}
	if result.Current != 1 {
		t.Errorf("current = %d, want 1", result.Current)
	}
}

func TestLinkRefusesOrdinaryFileConflict(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, ".vimrc"), []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".vimrc"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Link(managed, home, filepath.Join(root, "config")); err == nil {
		t.Fatal("Link() error = nil, want conflict")
	}
	data, err := os.ReadFile(filepath.Join(home, ".vimrc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "local" {
		t.Errorf("conflicting file was changed: %q", data)
	}
}

func TestLinkPlansAllTargetsBeforeMutation(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, ".a-managed"), []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, ".z-conflict"), []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".z-conflict"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Link(managed, home, filepath.Join(root, "config")); err == nil {
		t.Fatal("Link() error = nil, want conflict")
	}
	if _, err := os.Lstat(filepath.Join(home, ".a-managed")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("early target was linked before conflict detection, Lstat error = %v", err)
	}
}

func TestLinkRefusesTargetInsideConfigRoot(t *testing.T) {
	root := t.TempDir()
	configRoot := filepath.Join(root, "home", ".config", "xoldot")
	managed := filepath.Join(configRoot, "files", "home")
	home := filepath.Join(root, "home")
	source := filepath.Join(managed, ".config", "xoldot", "recursive")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Link(managed, home, configRoot); err == nil {
		t.Fatal("Link() error = nil, want recursive target error")
	}
}

func TestLinkRefusesSourceDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	configRoot := filepath.Join(root, "config")
	managed := filepath.Join(configRoot, "files", "home")
	home := filepath.Join(root, "home")
	directory := filepath.Join(root, "shared-directory")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(directory, filepath.Join(managed, "linked-directory")); err != nil {
		t.Fatal(err)
	}

	if _, err := Link(managed, home, configRoot); err == nil || !strings.Contains(err.Error(), "directory symlink") {
		t.Fatalf("Link() error = %v, want directory symlink error", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "linked-directory")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("target directory symlink exists, Lstat error = %v", err)
	}
}

func TestLinkResolvesConfigRootBeforeRecursionCheck(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	configRoot := filepath.Join(home, ".config", "xoldot")
	managed := filepath.Join(configRoot, "files", "home")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	configLink := filepath.Join(root, "config-link")
	if err := os.Symlink(configRoot, configLink); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(managed, ".config", "xoldot", "recursive")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	linkedManaged := filepath.Join(configLink, "files", "home")
	if _, err := Link(linkedManaged, home, configLink); err == nil {
		t.Fatal("Link() error = nil, want recursive target error through config symlink")
	}
}

func TestLinkReplacementPreservesSimilarUserFile(t *testing.T) {
	root := t.TempDir()
	configRoot := filepath.Join(root, "config")
	managed := filepath.Join(configRoot, "files", "home")
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(managed, ".vimrc")
	if err := os.WriteFile(source, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".vimrc")
	if err := os.Symlink(filepath.Join(managed, "old-vimrc"), target); err != nil {
		t.Fatal(err)
	}
	neighbor := target + ".xoldot-new"
	if err := os.WriteFile(neighbor, []byte("user data"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Link(managed, home, configRoot)
	if err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	if result.Updated != 1 {
		t.Errorf("updated = %d, want 1", result.Updated)
	}
	data, err := os.ReadFile(neighbor)
	if err != nil {
		t.Fatalf("read neighboring user file: %v", err)
	}
	if string(data) != "user data" {
		t.Errorf("neighboring user file = %q", data)
	}
}
