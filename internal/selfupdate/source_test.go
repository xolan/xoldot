package selfupdate

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/status"
)

func TestDevelopmentUpdatePullsCurrentSourceBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	const branch = "feature/self-update"
	remote := filepath.Join(t.TempDir(), "remote.git")
	runTestGit(t, "", "init", "--bare", "--initial-branch="+branch, remote)

	seed := t.TempDir()
	runTestGit(t, seed, "init", "-b", branch)
	runTestGit(t, seed, "config", "user.name", "Xoldot Test")
	runTestGit(t, seed, "config", "user.email", "xoldot@example.invalid")
	writeTestSourceFile(t, seed, "go.mod", "module github.com/xolan/xoldot\n\ngo 1.27.0\n")
	writeTestSourceFile(t, seed, "marker", "old\n")
	runTestGit(t, seed, "add", ".")
	runTestGit(t, seed, "commit", "-m", "initial")
	runTestGit(t, seed, "remote", "add", "origin", remote)
	runTestGit(t, seed, "push", "-u", "origin", branch)

	checkout := filepath.Join(t.TempDir(), "checkout")
	runTestGit(t, "", "clone", remote, checkout)
	writeTestSourceFile(t, seed, "marker", "new\n")
	runTestGit(t, seed, "add", "marker")
	runTestGit(t, seed, "commit", "-m", "update")
	runTestGit(t, seed, "push", "origin", branch)

	nested := filepath.Join(checkout, "internal", "selfupdate")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	var reports strings.Builder
	updater := Updater{
		Version:          "dev",
		Stdout:           &output,
		Stderr:           &output,
		workingDirectory: nested,
		Reporter: status.ReporterFunc(func(_ status.Kind, text string) error {
			reports.WriteString(text)
			reports.WriteByte('\n')
			return nil
		}),
	}
	if err := updater.Update(context.Background()); err != nil {
		t.Fatalf("Update() error = %v\n%s", err, output.String())
	}
	contents, err := os.ReadFile(filepath.Join(checkout, "marker"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "new\n" {
		t.Errorf("marker = %q, want new contents", contents)
	}
	for _, want := range []string{"Pulling origin/" + branch, "Updated the source checkout on " + branch} {
		if !strings.Contains(reports.String(), want) {
			t.Errorf("reports = %q, want %q", reports.String(), want)
		}
	}
}

func TestDevelopmentUpdateRefusesAnotherGoModule(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	runTestGit(t, root, "init", "-b", "main")
	writeTestSourceFile(t, root, "go.mod", "module example.com/another/project\n")
	updater := Updater{Version: "dev", workingDirectory: root}
	err := updater.Update(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not \"github.com/xolan/xoldot\"") {
		t.Fatalf("Update() error = %v, want wrong module error", err)
	}
}

func writeTestSourceFile(t *testing.T, root, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runTestGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}
