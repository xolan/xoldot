package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/managedhome"
)

func TestApplyBackupAndRestoreCommands(t *testing.T) {
	fixture := newApplyFixture(t)
	writeTestFile(t, fixture.managedTarget, []byte("local content\n"))
	if err := os.Chmod(fixture.managedTarget, 0o640); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := Run(
		[]string{"--config-dir", fixture.root, "apply", "--backup", "--only", "managed-home"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	); err != nil {
		t.Fatalf("backup apply error = %v\n%s", err, output.String())
	}
	match := regexp.MustCompile(`Backup ID: ([0-9a-f]{24})`).FindStringSubmatch(output.String())
	if len(match) != 2 {
		t.Fatalf("backup apply output = %q, want backup ID", output.String())
	}
	id := match[1]
	if destination, err := os.Readlink(fixture.managedTarget); err != nil || destination != fixture.managedSource {
		t.Fatalf("managed target = %q, %v", destination, err)
	}

	output.Reset()
	if err := Run(
		[]string{"--config-dir", fixture.root, "restore", id, "--dry"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	); err != nil {
		t.Fatalf("dry restore error = %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "Would restore 1 managed-home conflicts") {
		t.Errorf("dry restore output = %q", output.String())
	}
	if _, err := os.Readlink(fixture.managedTarget); err != nil {
		t.Fatalf("dry restore changed managed link: %v", err)
	}

	output.Reset()
	if err := Run(
		[]string{"--config-dir", fixture.root, "restore", id},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	); err != nil {
		t.Fatalf("restore error = %v\n%s", err, output.String())
	}
	data, err := os.ReadFile(fixture.managedTarget)
	if err != nil || string(data) != "local content\n" {
		t.Errorf("restored target = %q, %v", data, err)
	}
	if info, err := os.Lstat(fixture.managedTarget); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("restored mode = %v, %v, want 0640", info, err)
	}
}

func TestProfileBackupOnlyMovesSelectedConflicts(t *testing.T) {
	fixture := newApplyFixture(t)
	writeTestFile(t, filepath.Join(fixture.paths.Profiles, "work.toml"), []byte("managed_home = [\".managed\"]\n"))
	writeTestFile(t, fixture.managedTarget, []byte("selected local\n"))
	unselectedSource := filepath.Join(fixture.paths.ManagedHome, ".unselected")
	unselectedTarget := filepath.Join(filepath.Dir(fixture.managedTarget), ".unselected")
	writeTestFile(t, unselectedSource, []byte("unselected managed\n"))
	writeTestFile(t, unselectedTarget, []byte("unselected local\n"))

	var output bytes.Buffer
	if err := Run(
		[]string{
			"--config-dir", fixture.root,
			"apply", "--backup", "--only", "managed-home", "--profile", "work",
		},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	); err != nil {
		t.Fatalf("profile backup apply error = %v\n%s", err, output.String())
	}
	if destination, err := os.Readlink(fixture.managedTarget); err != nil || destination != fixture.managedSource {
		t.Errorf("selected target = %q, %v, want managed link", destination, err)
	}
	if data, err := os.ReadFile(unselectedTarget); err != nil || string(data) != "unselected local\n" {
		t.Errorf("unselected target = %q, %v, want local content", data, err)
	}
	if !regexp.MustCompile(`Backup ID: [0-9a-f]{24}`).MatchString(output.String()) {
		t.Errorf("profile backup output = %q, want backup ID", output.String())
	}
}

func TestDryBackupPreviewsEligibleAndUnsupportedConflicts(t *testing.T) {
	fixture := newApplyFixture(t)
	writeTestFile(t, fixture.managedTarget, []byte("local content\n"))
	unsupportedSource := filepath.Join(fixture.paths.ManagedHome, ".directory")
	unsupportedTarget := filepath.Join(filepath.Dir(fixture.managedTarget), ".directory")
	writeTestFile(t, unsupportedSource, []byte("managed content\n"))
	if err := os.Mkdir(unsupportedTarget, 0o755); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err := Run(
		[]string{"--config-dir", fixture.root, "apply", "--backup", "--dry", "--only", "managed-home"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	)
	var refusal *managedhome.PreparationRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("dry backup error = %T %v, want *PreparationRefusal", err, err)
	}
	for _, want := range []string{
		"Would back up " + fixture.managedTarget + " before linking",
		"Would link " + fixture.managedTarget + " -> " + fixture.managedSource,
		"Conflict at " + unsupportedTarget,
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("dry backup output does not contain %q:\n%s", want, output.String())
		}
	}
	if data, readErr := os.ReadFile(fixture.managedTarget); readErr != nil || string(data) != "local content\n" {
		t.Errorf("dry backup changed eligible target = %q, %v", data, readErr)
	}
	if info, statErr := os.Stat(unsupportedTarget); statErr != nil || !info.IsDir() {
		t.Errorf("dry backup changed unsupported target = %v, %v", info, statErr)
	}
}

func TestStatusReportsEligibleConflictsAndBackupManifestProblems(t *testing.T) {
	root, home := inspectionFixture(t)
	paths := config.NewPaths(root)
	source := filepath.Join(paths.ManagedHome, ".vimrc")
	if err := os.WriteFile(source, []byte("managed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".vimrc")
	if err := os.WriteFile(target, []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	backups := filepath.Join(home, ".local", "state", "xoldot", "backups")
	incompleteID := "0123456789abcdef01234567"
	invalidID := "89abcdef0123456789abcdef"
	if err := os.MkdirAll(filepath.Join(backups, incompleteID), 0o700); err != nil {
		t.Fatal(err)
	}
	invalidManifest := filepath.Join(backups, invalidID, "manifest.json")
	writeTestFile(t, invalidManifest, []byte("not json\n"))

	var output bytes.Buffer
	if err := Run(
		[]string{"--config-dir", root, "status"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"eligible backup conflict " + target,
		"incomplete " + incompleteID,
		"invalid " + invalidID,
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("status output does not contain %q:\n%s", want, output.String())
		}
	}
}

func TestApplyAndRestoreHelpDocumentBackupFlags(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		want      []string
	}{
		{arguments: []string{"apply", "--help"}, want: []string{"--backup", "managed-home conflicts"}},
		{arguments: []string{"restore", "--help"}, want: []string{"restore <backup-id>", "--dry"}},
	} {
		var output bytes.Buffer
		if err := Run(test.arguments, bytes.NewReader(nil), &output, &output, "test"); err != nil {
			t.Fatal(err)
		}
		for _, want := range test.want {
			if !strings.Contains(output.String(), want) {
				t.Errorf("help %v does not contain %q:\n%s", test.arguments, want, output.String())
			}
		}
	}
}
