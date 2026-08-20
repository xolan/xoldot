package gitops

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncPushesInitialCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", "--initial-branch=main", remote)

	var output bytes.Buffer
	runner := Runner{Dir: root, Stdout: &output, Stderr: &output}
	if err := runner.Configure(remote, "main"); err != nil {
		t.Fatalf("Configure() error = %v\n%s", err, output.String())
	}
	if strings.Contains(output.String(), "No such remote") {
		t.Fatalf("Configure() leaked expected remote probe error:\n%s", output.String())
	}
	runGit(t, root, "config", "user.name", "Xoldot Test")
	runGit(t, root, "config", "user.email", "xoldot@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "xoldot.toml"), []byte("test = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runner.Sync("origin", "main", false); err != nil {
		t.Fatalf("Sync() error = %v\n%s", err, output.String())
	}

	got := runGit(t, "", "--git-dir", remote, "show", "main:xoldot.toml")
	if got != "test = true\n" {
		t.Errorf("remote file = %q", got)
	}
}

func TestCheckoutRemoteRestoresExistingConfiguration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", "--initial-branch=main", remote)
	seed := t.TempDir()
	runGit(t, seed, "init", "-b", "main")
	runGit(t, seed, "config", "user.name", "Xoldot Test")
	runGit(t, seed, "config", "user.email", "xoldot@example.invalid")
	if err := os.WriteFile(filepath.Join(seed, "xoldot.toml"), []byte("restored = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "xoldot.toml")
	runGit(t, seed, "commit", "-m", "seed")
	runGit(t, seed, "remote", "add", "origin", remote)
	runGit(t, seed, "push", "-u", "origin", "main")

	root := t.TempDir()
	var output bytes.Buffer
	runner := Runner{Dir: root, Stdout: &output, Stderr: &output}
	if err := runner.Configure(remote, "main"); err != nil {
		t.Fatalf("Configure() error = %v\n%s", err, output.String())
	}
	checkedOut, err := runner.CheckoutRemote("origin", "main")
	if err != nil {
		t.Fatalf("CheckoutRemote() error = %v\n%s", err, output.String())
	}
	if !checkedOut {
		t.Fatal("CheckoutRemote() checkedOut = false, want true")
	}
	data, err := os.ReadFile(filepath.Join(root, "xoldot.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "restored = true\n" {
		t.Errorf("restored config = %q", data)
	}
}

func TestCommandLeavesStdinNil(t *testing.T) {
	runner := Runner{Dir: t.TempDir()}
	if got := runner.command("status").Stdin; got != nil {
		t.Errorf("command stdin = %T, want nil (non-file stdin hangs Wait)", got)
	}
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}

func TestSyncDryLeavesRepositoryUntouched(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", "--initial-branch=main", remote)

	var output bytes.Buffer
	runner := Runner{Dir: root, Stdout: &output, Stderr: &output}
	if err := runner.Configure(remote, "main"); err != nil {
		t.Fatalf("Configure() error = %v\n%s", err, output.String())
	}
	if err := os.WriteFile(filepath.Join(root, "xoldot.toml"), []byte("test = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runner.Sync("origin", "main", true); err != nil {
		t.Fatalf("Sync() error = %v\n%s", err, output.String())
	}
	for _, want := range []string{"would commit", "would push to origin/main"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output missing %q:\n%s", want, output.String())
		}
	}
	if got := runGit(t, root, "status", "--porcelain"); !strings.Contains(got, "xoldot.toml") {
		t.Errorf("dry run staged or committed the change: %q", got)
	}
	if got := runGit(t, "", "--git-dir", remote, "branch", "--list"); strings.TrimSpace(got) != "" {
		t.Errorf("dry run pushed to the remote: %q", got)
	}
}
