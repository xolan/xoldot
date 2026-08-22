package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/managedhome"
)

func TestStatusReportsMachineStateAndUncheckedTools(t *testing.T) {
	root, home := inspectionFixture(t)
	paths := config.NewPaths(root)
	source := filepath.Join(paths.ManagedHome, ".vimrc")
	if err := os.WriteFile(source, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := `[[tool]]
name = "ripgrep"
check = "command -v rg"
`
	if err := os.WriteFile(paths.Tools, []byte(tools), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := Run([]string{"--config-dir", root, "status"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(
		"Managed home:\n  missing %s -> %s\nAliases:\n  missing %s\nSkills:\n  none declared\n"+
			"Tools:\n  unchecked 1 declared tool; checks were not run because status is read-only and tool checks are user-authored commands\n",
		filepath.Join(home, ".vimrc"),
		source,
		filepath.Join(home, ".aliases", "alias.bash"),
	)
	if output.String() != want {
		t.Errorf("status output = %q, want %q", output.String(), want)
	}
}

func TestDiffPrintsPlannedLinksAndAliasCreation(t *testing.T) {
	root, home := inspectionFixture(t)
	paths := config.NewPaths(root)
	source := filepath.Join(paths.ManagedHome, ".vimrc")
	if err := os.WriteFile(source, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := Run([]string{"--config-dir", root, "diff"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(
		"Would link %s -> %s\nWould create alias output %s\n",
		filepath.Join(home, ".vimrc"),
		source,
		filepath.Join(home, ".aliases", "alias.bash"),
	)
	if output.String() != want {
		t.Errorf("diff output = %q, want %q", output.String(), want)
	}
}

func TestStatusAndDiffStyleSemanticPrefixesForTerminals(t *testing.T) {
	root, _ := inspectionFixture(t)
	paths := config.NewPaths(root)
	if err := os.WriteFile(filepath.Join(paths.ManagedHome, ".vimrc"), []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	application := app{configDir: root, output: &output, style: styler{enabled: true}}
	if err := application.machineStatus(""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"\x1b[1mManaged home:\x1b[0m",
		"\x1b[36mmissing\x1b[0m",
		"\x1b[2mnone declared\x1b[0m",
		"\x1b[2munchecked\x1b[0m",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("styled status does not contain %q:\n%s", want, output.String())
		}
	}

	output.Reset()
	if err := application.machineDiff(""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "\x1b[36mWould\x1b[0m link") {
		t.Errorf("styled diff has no progress prefix:\n%s", output.String())
	}
}

func TestStatusLeavesUnknownStatesUnstyled(t *testing.T) {
	style := styler{enabled: true}
	for name, got := range map[string]string{
		"managed home": styleManagedHomeState(style, managedhome.State("future")),
		"backup":       styleBackupState(style, managedhome.BackupState("future")),
		"alias":        styleAliasState(style, aliases.State("future")),
	} {
		if got != "future" {
			t.Errorf("%s unknown state = %q, want unstyled state", name, got)
		}
	}
}

func TestStatusAndDiffDoNotExecuteToolChecks(t *testing.T) {
	root, _ := inspectionFixture(t)
	paths := config.NewPaths(root)
	marker := filepath.Join(t.TempDir(), "tool-check-ran")
	tools := fmt.Sprintf("[[tool]]\nname = \"unsafe\"\ncheck = \"touch %s\"\n", marker)
	if err := os.WriteFile(paths.Tools, []byte(tools), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, command := range []string{"status", "diff"} {
		var output bytes.Buffer
		if err := Run([]string{"--config-dir", root, command}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
			t.Fatalf("%s error = %v", command, err)
		}
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s executed the tool check: %v", command, err)
		}
	}
}

func TestStatusAndDiffDoNotChangeFilesystem(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "config")
	home := filepath.Join(base, "missing-home")
	if err := config.Initialize(config.NewPaths(root)); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.TargetHomeEnv, home)
	t.Setenv("XOLDOT_SHELL", "bash")
	before := filesystemSnapshot(t, base)

	for _, command := range []string{"status", "diff"} {
		var output bytes.Buffer
		if err := Run([]string{"--config-dir", root, command}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
			t.Fatalf("%s error = %v", command, err)
		}
		if after := filesystemSnapshot(t, base); after != before {
			t.Fatalf("%s changed the filesystem\nbefore:\n%s\nafter:\n%s", command, before, after)
		}
	}
}

func TestCurrentMachineHasNoStatusOrDiffChanges(t *testing.T) {
	root, home := inspectionFixture(t)
	paths := config.NewPaths(root)
	source := filepath.Join(paths.ManagedHome, ".vimrc")
	if err := os.WriteFile(source, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Run([]string{"--config-dir", root, "apply"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatalf("apply error = %v", err)
	}

	output.Reset()
	if err := Run([]string{"--config-dir", root, "status"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatal(err)
	}
	statusOutput := output.String()
	for _, want := range []string{
		"current " + filepath.Join(home, ".vimrc") + " -> " + source,
		"current " + filepath.Join(home, ".aliases", "alias.bash"),
	} {
		if !strings.Contains(statusOutput, want) {
			t.Errorf("status output does not contain %q: %q", want, statusOutput)
		}
	}
	for _, pending := range []string{"missing ", "stale ", "replaceable ", "conflict "} {
		if strings.Contains(statusOutput, pending) {
			t.Errorf("current status contains pending state %q: %q", pending, statusOutput)
		}
	}

	output.Reset()
	if err := Run([]string{"--config-dir", root, "diff"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "No managed home or Alias changes.\n"; got != want {
		t.Errorf("diff output = %q, want %q", got, want)
	}
}

func TestDiffPrintsOwnedAliasUnifiedDiff(t *testing.T) {
	root, _ := inspectionFixture(t)
	var output bytes.Buffer
	for _, arguments := range [][]string{
		{"--config-dir", root, "alias", "add", "ll", "ls -l"},
		{"--config-dir", root, "apply"},
		{"--config-dir", root, "alias", "add", "ll", "ls -la"},
	} {
		if err := Run(arguments, bytes.NewReader(nil), &output, &output, "test"); err != nil {
			t.Fatalf("%v error = %v", arguments, err)
		}
	}

	output.Reset()
	if err := Run([]string{"--config-dir", root, "diff"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--- ", "+++ ", "-alias ll='ls -l'", "+alias ll='ls -la'"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("diff output does not contain %q:\n%s", want, output.String())
		}
	}
}

func TestStatusAndDiffReportConflictsWithoutCommandError(t *testing.T) {
	root, home := inspectionFixture(t)
	paths := config.NewPaths(root)
	if err := os.WriteFile(filepath.Join(paths.ManagedHome, ".vimrc"), []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".aliases"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".vimrc"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".aliases", "alias.bash"), []byte("local aliases\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, command := range []string{"status", "diff"} {
		var output bytes.Buffer
		if err := Run([]string{"--config-dir", root, command}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
			t.Fatalf("%s error = %v", command, err)
		}
		if !strings.Contains(strings.ToLower(output.String()), "conflict") {
			t.Errorf("%s output does not describe conflicts: %q", command, output.String())
		}
	}
}

func TestStatusAndDiffReturnConfigurationErrors(t *testing.T) {
	root, _ := inspectionFixture(t)
	paths := config.NewPaths(root)
	if err := os.WriteFile(paths.Tools, []byte("[[tool]]\nname = [\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, command := range []string{"status", "diff"} {
		var output bytes.Buffer
		err := Run([]string{"--config-dir", root, command}, bytes.NewReader(nil), &output, &output, "test")
		if err == nil || !strings.Contains(err.Error(), "parse") {
			t.Errorf("%s error = %v, want configuration parse error", command, err)
		}
	}
}

func inspectionFixture(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "config")
	home := filepath.Join(t.TempDir(), "home")
	if err := config.Initialize(config.NewPaths(root)); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.TargetHomeEnv, home)
	t.Setenv("XOLDOT_SHELL", "bash")
	return root, home
}

func filesystemSnapshot(t *testing.T, root string) string {
	t.Helper()
	var snapshot strings.Builder
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&snapshot, "%s %s\n", relative, info.Mode())
		if entry.Type()&os.ModeSymlink != 0 {
			destination, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&snapshot, "-> %s\n", destination)
		} else if info.Mode().IsRegular() {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&snapshot, "%q\n", contents)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.String()
}
