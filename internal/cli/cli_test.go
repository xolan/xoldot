package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/config"
)

func TestSetupWithoutRemoteThenAliasToolAndApply(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv(config.TargetHomeEnv, home)
	t.Setenv("XOLDOT_SHELL", "bash")

	var output bytes.Buffer
	if err := Run([]string{"--config-dir", root, "setup"}, bytes.NewBufferString("\n"), &output, &output, "test"); err != nil {
		t.Fatalf("setup error = %v", err)
	}
	if err := Run([]string{"--config-dir", root, "alias", "add", "ll", "ls -la"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatalf("alias add error = %v", err)
	}
	if err := Run([]string{"--config-dir", root, "tool", "add", "sh"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatalf("tool add error = %v", err)
	}

	managedFile := filepath.Join(root, "files", "home", ".example")
	if err := os.WriteFile(managedFile, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run([]string{"--config-dir", root, "apply"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatalf("apply error = %v\noutput:\n%s", err, output.String())
	}

	destination, err := os.Readlink(filepath.Join(home, ".example"))
	if err != nil {
		t.Fatalf("dotfile link error = %v", err)
	}
	if destination != managedFile {
		t.Errorf("dotfile destination = %q, want %q", destination, managedFile)
	}
	aliasData, err := os.ReadFile(filepath.Join(home, ".aliases", "alias.bash"))
	if err != nil {
		t.Fatalf("read rendered aliases: %v", err)
	}
	if !bytes.Contains(aliasData, []byte("alias ll='ls -la'")) {
		t.Errorf("rendered aliases = %q", aliasData)
	}
}

func TestSetupRefusesToOverwriteLocalConfigWithExistingRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := filepath.Join(t.TempDir(), "config")
	var output bytes.Buffer
	if err := Run([]string{"--config-dir", root, "setup"}, bytes.NewBufferString("\n"), &output, &output, "test"); err != nil {
		t.Fatalf("initial setup error = %v", err)
	}
	localTools := []byte("# local configuration\n")
	if err := os.WriteFile(filepath.Join(root, "tools.toml"), localTools, 0o644); err != nil {
		t.Fatal(err)
	}

	remote := seedRemote(t)
	err := Run(
		[]string{"--config-dir", root, "setup"},
		bytes.NewBufferString(remote+"\n\n"),
		&output,
		&output,
		"test",
	)
	if err == nil || !strings.Contains(err.Error(), "move it aside") {
		t.Fatalf("setup error = %v, want safe migration instructions", err)
	}
	got, readErr := os.ReadFile(filepath.Join(root, "tools.toml"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, localTools) {
		t.Errorf("local tools changed: %q", got)
	}
}

func TestApplyValidatesShellBeforeLinking(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	paths := config.NewPaths(root)
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	managedFile := filepath.Join(paths.ManagedHome, ".example")
	if err := os.WriteFile(managedFile, []byte("managed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.TargetHomeEnv, home)
	t.Setenv("XOLDOT_SHELL", "nushell")

	var output bytes.Buffer
	err := Run([]string{"--config-dir", root, "apply"}, bytes.NewReader(nil), &output, &output, "test")
	if err == nil || !strings.Contains(err.Error(), "unsupported shell") {
		t.Fatalf("apply error = %v, want unsupported shell", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".example")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dotfile was linked before validation, Lstat error = %v", err)
	}
}

func TestSetupDoesNotMislabelRemoteFailureAsLocalConflict(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := filepath.Join(t.TempDir(), "config")
	missingRemote := filepath.Join(t.TempDir(), "missing.git")
	var output bytes.Buffer
	err := Run(
		[]string{"--config-dir", root, "setup"},
		bytes.NewBufferString(missingRemote+"\n\n"),
		&output,
		&output,
		"test",
	)
	if err == nil {
		t.Fatal("setup error = nil, want missing remote error")
	}
	if strings.Contains(err.Error(), "move it aside") {
		t.Fatalf("setup mislabeled remote error as local conflict: %v", err)
	}
}

func TestSkillsPluralAliasUpdatesEmptyCatalog(t *testing.T) {
	root := t.TempDir()
	if err := config.Initialize(config.NewPaths(root)); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Run(
		[]string{"--config-dir", root, "skills", "update"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	); err != nil {
		t.Fatalf("skills update error = %v", err)
	}
	if !strings.Contains(output.String(), "No skills to update") {
		t.Errorf("output = %q", output.String())
	}
}

func TestToolListPrintsCatalogNamesInAlphabeticalOrder(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	tools := `[[tool]]
name = "ripgrep"

[[tool]]
name = "jq"
`
	if err := os.WriteFile(paths.Tools, []byte(tools), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := Run(
		[]string{"--config-dir", root, "tool", "list"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	); err != nil {
		t.Fatalf("tool list error = %v", err)
	}
	if got, want := output.String(), "jq\nripgrep\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestSkillsListPrintsCatalogNamesInAlphabeticalOrder(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	skills := `[[skill]]
name = "write-tests"
source = "https://example.com/write-tests"

[[skill]]
name = "code-review"
source = "https://example.com/code-review"
`
	if err := os.WriteFile(paths.Skills, []byte(skills), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := Run(
		[]string{"--config-dir", root, "skills", "list"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	); err != nil {
		t.Fatalf("skills list error = %v", err)
	}
	if got, want := output.String(), "code-review\nwrite-tests\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestHelpIncludesSkillCommands(t *testing.T) {
	var output bytes.Buffer
	if err := Run([]string{"help"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"skill add", "skill list", "skill remove", "skill update"} {
		if !strings.Contains(output.String(), command) {
			t.Errorf("help does not include %q", command)
		}
	}
}

func seedRemote(t *testing.T) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", "--initial-branch=main", remote)
	seed := t.TempDir()
	runGit(t, seed, "init", "-b", "main")
	runGit(t, seed, "config", "user.name", "Xoldot Test")
	runGit(t, seed, "config", "user.email", "xoldot@example.invalid")
	if err := os.WriteFile(filepath.Join(seed, "xoldot.toml"), []byte("remote = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "xoldot.toml")
	runGit(t, seed, "commit", "-m", "seed")
	runGit(t, seed, "remote", "add", "origin", remote)
	runGit(t, seed, "push", "-u", "origin", "main")
	return remote
}

func runGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}
