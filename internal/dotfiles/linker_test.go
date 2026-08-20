package dotfiles

import (
	"errors"
	"io"
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

	result, err := Link(managed, home, filepath.Join(root, "config"), io.Discard, false)
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

	result, err = Link(managed, home, filepath.Join(root, "config"), io.Discard, false)
	if err != nil {
		t.Fatalf("second Link() error = %v", err)
	}
	if result.Current != 1 {
		t.Errorf("current = %d, want 1", result.Current)
	}
}

func TestLinkPreservesManagedRelativeFileSymlink(t *testing.T) {
	root := t.TempDir()
	configRoot := filepath.Join(root, "config")
	managed := filepath.Join(configRoot, "files", "home")
	home := filepath.Join(root, "home")
	canonical := filepath.Join(managed, ".agents", "skills", "example", "SKILL.md")
	compatibility := filepath.Join(managed, ".claude", "skills", "example", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(compatibility), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(filepath.Dir(compatibility), canonical)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relative, compatibility); err != nil {
		t.Fatal(err)
	}

	result, err := Link(managed, home, configRoot, io.Discard, false)
	if err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	if result.Created != 2 {
		t.Fatalf("created = %d, want 2", result.Created)
	}
	homeCompatibility := filepath.Join(home, ".claude", "skills", "example", "SKILL.md")
	destination, err := os.Readlink(homeCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Rel(filepath.Dir(homeCompatibility), filepath.Join(home, ".agents", "skills", "example", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if destination != want {
		t.Errorf("compatibility destination = %q, want %q", destination, want)
	}
	data, err := os.ReadFile(homeCompatibility)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "managed" {
		t.Errorf("compatibility contents = %q", data)
	}

	if err := os.RemoveAll(filepath.Join(managed, ".agents", "skills", "example")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(managed, ".claude", "skills", "example")); err != nil {
		t.Fatal(err)
	}
	result, err = Link(managed, home, configRoot, io.Discard, false)
	if err != nil {
		t.Fatalf("Link() after skill removal error = %v", err)
	}
	if result.Removed != 2 {
		t.Errorf("removed = %d, want 2", result.Removed)
	}
	for _, target := range []string{
		filepath.Join(home, ".agents", "skills", "example", "SKILL.md"),
		homeCompatibility,
	} {
		if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("stale skill link %s remains: %v", target, statErr)
		}
	}
}

func TestLinkMapsRelativeFileSymlinkThroughResolvedParents(t *testing.T) {
	root := t.TempDir()
	configRoot := filepath.Join(root, "config")
	managed := filepath.Join(configRoot, "files", "home")
	home := filepath.Join(root, "home")
	actualAgents := filepath.Join(root, "actual-agents")
	actualClaude := filepath.Join(root, "actual-claude")
	for _, directory := range []string{home, actualAgents, actualClaude} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(actualAgents, filepath.Join(home, ".agents")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actualClaude, filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}

	canonical := filepath.Join(managed, ".agents", "skills", "example", "SKILL.md")
	compatibility := filepath.Join(managed, ".claude", "skills", "example", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(compatibility), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	managedDestination, err := filepath.Rel(filepath.Dir(compatibility), canonical)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(managedDestination, compatibility); err != nil {
		t.Fatal(err)
	}

	if _, err := Link(managed, home, configRoot, io.Discard, false); err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	compatibilityTarget := filepath.Join(actualClaude, "skills", "example", "SKILL.md")
	destination, err := os.Readlink(compatibilityTarget)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Rel(
		filepath.Dir(compatibilityTarget),
		filepath.Join(actualAgents, "skills", "example", "SKILL.md"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if destination != want {
		t.Errorf("compatibility destination = %q, want %q", destination, want)
	}
	data, err := os.ReadFile(compatibilityTarget)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "managed" {
		t.Errorf("compatibility contents = %q", data)
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

	if _, err := Link(managed, home, filepath.Join(root, "config"), io.Discard, false); err == nil {
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

	if _, err := Link(managed, home, filepath.Join(root, "config"), io.Discard, false); err == nil {
		t.Fatal("Link() error = nil, want conflict")
	}
	if _, err := os.Lstat(filepath.Join(home, ".a-managed")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("early target was linked before conflict detection, Lstat error = %v", err)
	}
}

func TestLinkRemovesOnlyStaleLinksItStillOwns(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	home := filepath.Join(root, "home")
	configRoot := filepath.Join(root, "config")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(managed, ".owned")
	if err := os.WriteFile(source, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Link(managed, home, configRoot, io.Discard, false); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".owned")
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}

	result, err := Link(managed, home, configRoot, io.Discard, false)
	if err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	if result.Removed != 1 {
		t.Errorf("removed = %d, want 1", result.Removed)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale owned target remains: %v", err)
	}

	if err := os.WriteFile(source, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Link(managed, home, configRoot, io.Discard, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "other")
	if err := os.WriteFile(other, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}

	result, err = Link(managed, home, configRoot, io.Discard, false)
	if err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	if result.Removed != 0 {
		t.Errorf("removed = %d, want 0", result.Removed)
	}
	destination, err := os.Readlink(target)
	if err != nil {
		t.Fatal(err)
	}
	if destination != other {
		t.Errorf("user target destination = %q, want %q", destination, other)
	}
}

func TestLinkRejectsLedgerTargetsOutsideHome(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	home := filepath.Join(root, "home")
	configRoot := filepath.Join(root, "config")
	for _, directory := range []string{managed, home, configRoot} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	outsideSource := filepath.Join(managed, "old")
	outsideTarget := filepath.Join(root, "outside")
	if err := os.Symlink(outsideSource, outsideTarget); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(home, ".local", "state", "xoldot", "links.json")
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	ledger := `{"version":1,"links":[{"target":"` + outsideTarget + `","destination":"` + outsideSource + `"}]}`
	if err := os.WriteFile(ledgerPath, []byte(ledger), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Link(managed, home, configRoot, io.Discard, false); err == nil || !strings.Contains(err.Error(), "outside the target home") {
		t.Fatalf("Link() error = %v, want invalid ledger error", err)
	}
	if _, err := os.Lstat(outsideTarget); err != nil {
		t.Errorf("outside target was changed: %v", err)
	}
}

func TestLinkReservesItsLedgerPath(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	home := filepath.Join(root, "home")
	configRoot := filepath.Join(root, "config")
	source := filepath.Join(managed, ".local", "state", "xoldot", "links.json")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Link(managed, home, configRoot, io.Discard, false); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("Link() error = %v, want reserved path error", err)
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

	if _, err := Link(managed, home, configRoot, io.Discard, false); err == nil {
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

	if _, err := Link(managed, home, configRoot, io.Discard, false); err == nil || !strings.Contains(err.Error(), "directory symlink") {
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
	if _, err := Link(linkedManaged, home, configLink, io.Discard, false); err == nil {
		t.Fatal("Link() error = nil, want recursive target error through config symlink")
	}
}

func TestLinkRefusesMismatchedManagedSymlink(t *testing.T) {
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
	oldSource := filepath.Join(managed, "old-vimrc")
	if err := os.WriteFile(oldSource, []byte("old managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".vimrc")
	if err := os.Symlink(oldSource, target); err != nil {
		t.Fatal(err)
	}

	if _, err := Link(managed, home, configRoot, io.Discard, false); err == nil {
		t.Fatal("Link() error = nil, want conflict")
	}
	destination, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}
	if destination != oldSource {
		t.Errorf("link destination = %q, want unchanged %q", destination, oldSource)
	}
}

func TestLinkDryPlansWithoutMutating(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "config", "files", "home")
	home := filepath.Join(root, "home")
	source := filepath.Join(managed, ".vimrc")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}

	var output strings.Builder
	result, err := Link(managed, home, filepath.Join(root, "config"), &output, true)
	if err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	if result.Created != 1 {
		t.Errorf("created = %d, want 1", result.Created)
	}
	if !strings.Contains(output.String(), "would link") {
		t.Errorf("output = %q", output.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".vimrc")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry run created a link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "xoldot", "links.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("dry run saved the link ledger")
	}
}
