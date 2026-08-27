package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/lifecyclescripts"
	"github.com/xolan/xoldot/internal/managedhome"
	agentskills "github.com/xolan/xoldot/internal/skills"
)

func TestStatusAndDiffJSONDocuments(t *testing.T) {
	root, home := inspectionFixture(t)
	paths := config.NewPaths(root)
	for _, name := range []string{".zshrc", ".vimrc"} {
		if err := os.WriteFile(filepath.Join(paths.ManagedHome, name), []byte("managed"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, paths.Tools, []byte(`[[tool]]
name = "ripgrep"
check = "command -v rg"
`))
	writeTestFile(t, paths.Skills, []byte(`[[skill]]
name = "focus"
source = "owner/repo"
digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
agents = ["reviewer.md"]
`))
	writeTestFile(t, filepath.Join(paths.Scripts, "before-apply", "run_notice"), []byte("#!/bin/sh\ntrue\n"))
	if err := os.Chmod(filepath.Join(paths.Scripts, "before-apply", "run_notice"), 0o755); err != nil {
		t.Fatal(err)
	}

	statusOutput := runInspectionJSON(t, root, "status")
	var statusDocument statusJSONDocument
	if err := json.Unmarshal(statusOutput, &statusDocument); err != nil {
		t.Fatalf("decode status JSON: %v\n%s", err, statusOutput)
	}
	if statusDocument.SchemaVersion != inspectionSchemaVersion {
		t.Errorf("status schema version = %d", statusDocument.SchemaVersion)
	}
	if len(statusDocument.ManagedHome) != 2 ||
		statusDocument.ManagedHome[0].Target != filepath.Join(home, ".vimrc") ||
		statusDocument.ManagedHome[1].Target != filepath.Join(home, ".zshrc") {
		t.Errorf("status managed_home = %+v", statusDocument.ManagedHome)
	}
	if statusDocument.Aliases.State != string(aliases.StateMissing) {
		t.Errorf("status aliases = %+v", statusDocument.Aliases)
	}
	if statusDocument.Tools.Unchecked != 1 {
		t.Errorf("status tools = %+v", statusDocument.Tools)
	}
	if len(statusDocument.Skills) != 1 || statusDocument.Skills[0].State != string(agentskills.InspectionProblem) {
		t.Errorf("status skills = %+v", statusDocument.Skills)
	}
	if len(statusDocument.LifecycleScripts.BeforeApply) != 1 {
		t.Errorf("status lifecycle scripts = %+v", statusDocument.LifecycleScripts)
	}

	diffOutput := runInspectionJSON(t, root, "diff")
	var diffDocument diffJSONDocument
	if err := json.Unmarshal(diffOutput, &diffDocument); err != nil {
		t.Fatalf("decode diff JSON: %v\n%s", err, diffOutput)
	}
	if diffDocument.SchemaVersion != inspectionSchemaVersion {
		t.Errorf("diff schema version = %d", diffDocument.SchemaVersion)
	}
	if len(diffDocument.ManagedHome) != 2 ||
		diffDocument.ManagedHome[0].Action != "link" ||
		diffDocument.ManagedHome[1].Action != "link" {
		t.Errorf("diff managed_home = %+v", diffDocument.ManagedHome)
	}
	if diffDocument.Aliases.Action != "create" {
		t.Errorf("diff aliases = %+v", diffDocument.Aliases)
	}
	if len(diffDocument.LifecycleScripts.BeforeApply) != 1 {
		t.Errorf("diff lifecycle scripts = %+v", diffDocument.LifecycleScripts)
	}
}

func TestStatusJSONIncludesConflictBackupAndSkillStates(t *testing.T) {
	managedEntries := []managedhome.Entry{
		{
			State:             managedhome.StateConflict,
			Target:            "/home/user/.vimrc",
			Destination:       "/config/files/home/.vimrc",
			Problem:           "target exists",
			EligibleForBackup: true,
		},
	}
	document := newStatusJSONDocument(machineInspection{
		managedHome: managedEntries,
		backups: []managedhome.BackupInspection{
			{ID: "b", State: managedhome.BackupInvalid, Problem: "bad manifest"},
			{ID: "a", State: managedhome.BackupReady},
		},
		alias: aliases.Inspection{
			State:   aliases.StateConflict,
			Path:    "/home/user/.aliases/alias.bash",
			Problem: "not owned",
		},
		skills: []agentskills.Inspection{
			{Name: "zeta", State: agentskills.InspectionCurrent},
			{Name: "alpha", State: agentskills.InspectionProblem, Problem: "digest mismatch"},
		},
		beforeScripts: []lifecyclescripts.Entry{{Path: "before-apply/run_b"}, {Path: "before-apply/run_a"}},
		afterScripts:  []lifecyclescripts.Entry{{Path: "after-apply/run_z"}},
		tools:         2,
	})

	if !document.ManagedHome[0].EligibleForBackup || document.ManagedHome[0].Problem != "target exists" {
		t.Errorf("managed home conflict = %+v", document.ManagedHome[0])
	}
	if document.Backups[0].ID != "a" || document.Backups[1].Problem != "bad manifest" {
		t.Errorf("backups = %+v", document.Backups)
	}
	if document.Aliases.Problem != "not owned" {
		t.Errorf("aliases = %+v", document.Aliases)
	}
	if document.Skills[0].Name != "alpha" || document.Skills[0].Problem != "digest mismatch" {
		t.Errorf("skills = %+v", document.Skills)
	}
	if document.LifecycleScripts.BeforeApply[0].Path != "before-apply/run_a" {
		t.Errorf("lifecycle scripts = %+v", document.LifecycleScripts)
	}

	diff := newDiffJSONDocument(machineInspection{
		managedHome: managedEntries,
		alias: aliases.Inspection{
			State:   aliases.StateConflict,
			Path:    "/home/user/.aliases/alias.bash",
			Problem: "not owned",
		},
	})
	if len(diff.ManagedHome) != 1 ||
		diff.ManagedHome[0].Action != "conflict" ||
		!diff.ManagedHome[0].EligibleForBackup {
		t.Errorf("diff managed home conflict = %+v", diff.ManagedHome)
	}
	if diff.Aliases.Action != "conflict" || diff.Aliases.Problem != "not owned" {
		t.Errorf("diff alias conflict = %+v", diff.Aliases)
	}
}

func TestDiffJSONUsesTypedAliasReplacement(t *testing.T) {
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

	var document diffJSONDocument
	if err := json.Unmarshal(runInspectionJSON(t, root, "diff"), &document); err != nil {
		t.Fatal(err)
	}
	if document.Aliases.Action != "replace" ||
		document.Aliases.CurrentContent == nil ||
		document.Aliases.DesiredContent == nil ||
		!strings.Contains(*document.Aliases.CurrentContent, "alias ll='ls -l'") ||
		!strings.Contains(*document.Aliases.DesiredContent, "alias ll='ls -la'") {
		t.Errorf("diff aliases = %+v", document.Aliases)
	}
}

func TestInspectionJSONIgnoresTerminalStyling(t *testing.T) {
	root, _ := inspectionFixture(t)
	var styled bytes.Buffer
	application := app{configDir: root, output: &styled, style: styler{enabled: true}}
	if err := application.machineStatusJSON(""); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(styled.Bytes(), []byte("\x1b[")) || !json.Valid(styled.Bytes()) {
		t.Errorf("styled status JSON = %q", styled.String())
	}
	for _, want := range []string{`"managed_home":[]`, `"backups":[]`, `"skills":[]`} {
		if !strings.Contains(styled.String(), want) {
			t.Errorf("empty status collection is not an array: %s", styled.String())
		}
	}

	var plain bytes.Buffer
	application.output = &plain
	application.style = styler{}
	if err := application.machineStatusJSON(""); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(styled.Bytes(), plain.Bytes()) {
		t.Errorf("terminal styling changed JSON bytes\nstyled: %s\nplain: %s", styled.Bytes(), plain.Bytes())
	}
}

func TestInspectionTextFormatPreservesDefaultOutput(t *testing.T) {
	root, _ := inspectionFixture(t)
	for _, command := range []string{"status", "diff"} {
		var defaultOutput bytes.Buffer
		if err := Run(
			[]string{"--config-dir", root, command},
			bytes.NewReader(nil),
			&defaultOutput,
			&defaultOutput,
			"test",
		); err != nil {
			t.Fatal(err)
		}
		var explicitOutput bytes.Buffer
		if err := Run(
			[]string{"--config-dir", root, command, "--format", "text"},
			bytes.NewReader(nil),
			&explicitOutput,
			&explicitOutput,
			"test",
		); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(defaultOutput.Bytes(), explicitOutput.Bytes()) {
			t.Errorf("%s --format text changed output\ndefault: %q\nexplicit: %q", command, defaultOutput.String(), explicitOutput.String())
		}
	}
}

func TestInspectionFormatValidationAndCompletion(t *testing.T) {
	root, _ := inspectionFixture(t)
	for _, command := range []string{"status", "diff"} {
		var output bytes.Buffer
		err := Run(
			[]string{"--config-dir", root, command, "--format", "yaml"},
			bytes.NewReader(nil),
			&output,
			&output,
			"test",
		)
		if err == nil || !strings.Contains(err.Error(), "use text or json") {
			t.Errorf("%s invalid format error = %v", command, err)
		}
		if output.Len() != 0 {
			t.Errorf("%s invalid format wrote stdout: %q", command, output.String())
		}

		output.Reset()
		if err := Run(
			[]string{"--config-dir", root, "__complete", command, "--format", ""},
			bytes.NewReader(nil),
			&output,
			&output,
			"test",
		); err != nil {
			t.Fatalf("%s format completion error = %v", command, err)
		}
		if !strings.Contains(output.String(), "text") || !strings.Contains(output.String(), "json") {
			t.Errorf("%s format completion = %q", command, output.String())
		}
	}
}

func TestInspectionJSONErrorsDoNotWritePartialDocuments(t *testing.T) {
	root, _ := inspectionFixture(t)
	paths := config.NewPaths(root)
	writeTestFile(t, paths.Tools, []byte("[[tool]]\nname = [\n"))

	for _, command := range []string{"status", "diff"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		err := Run(
			[]string{"--config-dir", root, command, "--format", "json"},
			bytes.NewReader(nil),
			&stdout,
			&stderr,
			"test",
		)
		if err == nil {
			t.Fatalf("%s JSON succeeded with invalid configuration", command)
		}
		if stdout.Len() != 0 {
			t.Errorf("%s JSON wrote a partial document: %q", command, stdout.String())
		}
		if err := WriteError(&stderr, err); err != nil {
			t.Fatal(err)
		}
		if stderr.Len() == 0 || json.Valid(stderr.Bytes()) {
			t.Errorf("%s error output = %q", command, stderr.String())
		}
	}
}

func runInspectionJSON(t *testing.T, root, command string) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := Run(
		[]string{"--config-dir", root, command, "--format", "json"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	); err != nil {
		t.Fatalf("%s JSON error = %v\n%s", command, err, output.String())
	}
	if !json.Valid(output.Bytes()) {
		t.Fatalf("%s output is not JSON: %q", command, output.String())
	}
	return append([]byte(nil), output.Bytes()...)
}
