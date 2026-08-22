package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/machinestate"
)

func TestLifecycleScriptsRunAroundSelectedApplyParts(t *testing.T) {
	fixture := newApplyFixture(t)
	home := filepath.Dir(fixture.managedTarget)
	trace := filepath.Join(t.TempDir(), "trace")
	toolMarker := filepath.Join(t.TempDir(), "tool-installed")
	writeTestFile(t, fixture.paths.Tools, []byte(fmt.Sprintf(`[[tool]]
name = "probe"
check = "test -f %s"

[tool.install]
macos = "printf 'tool\\n' >> %s; touch %s"

[tool.install.linux]
default = "printf 'tool\\n' >> %s; touch %s"
`, shellQuote(toolMarker), shellQuote(trace), shellQuote(toolMarker), shellQuote(trace), shellQuote(toolMarker))))
	writeLifecycleScript(t, fixture.paths, "before-apply", "run_10_before", fmt.Sprintf(`
test ! -e %s
test ! -e %s
test ! -e %s
printf 'before\n' >> %s`, shellQuote(toolMarker), shellQuote(fixture.managedTarget), shellQuote(fixture.aliasPath), shellQuote(trace)))
	writeLifecycleScript(t, fixture.paths, "after-apply", "run_10_after", fmt.Sprintf(`
test -e %s
test -L %s
test -f %s
printf 'after\n' >> %s`, shellQuote(toolMarker), shellQuote(fixture.managedTarget), shellQuote(fixture.aliasPath), shellQuote(trace)))

	var output bytes.Buffer
	if err := Run([]string{"--config-dir", fixture.root, "apply"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatalf("apply error = %v\n%s", err, output.String())
	}
	if got, want := strings.Split(strings.TrimSpace(readTestFile(t, trace)), "\n"), []string{"before", "tool", "after"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("trace = %v, want %v", got, want)
	}
	for _, want := range []string{
		"› Running lifecycle script before-apply/run_10_before",
		"✓ Ran lifecycle script before-apply/run_10_before",
		"› Running lifecycle script after-apply/run_10_after",
		"✓ Ran lifecycle script after-apply/run_10_after",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("apply output does not contain %q:\n%s", want, output.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "xoldot", "scripts.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("always-run scripts wrote state: %v", err)
	}
}

func TestBeforeScriptFailurePreventsSelectedPartMutation(t *testing.T) {
	fixture := newApplyFixture(t)
	toolMarker := filepath.Join(t.TempDir(), "tool-installed")
	writeTestFile(t, fixture.paths.Tools, []byte(fmt.Sprintf(`[[tool]]
name = "probe"
check = "false"

[tool.install]
macos = "touch %s"

[tool.install.linux]
default = "touch %s"
`, shellQuote(toolMarker), shellQuote(toolMarker))))
	writeLifecycleScript(t, fixture.paths, "before-apply", "run_once_fail", "exit 7")

	var output bytes.Buffer
	err := Run([]string{"--config-dir", fixture.root, "apply"}, bytes.NewReader(nil), &output, &output, "test")
	if err == nil || !strings.Contains(err.Error(), "before-apply/run_once_fail") {
		t.Fatalf("apply error = %v, want before script failure", err)
	}
	for _, path := range []string{toolMarker, fixture.managedTarget, fixture.aliasPath, filepath.Join(filepath.Dir(fixture.managedTarget), ".local", "state", "xoldot", "scripts.json")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("failed before script allowed mutation at %s: %v", path, err)
		}
	}
}

func TestAfterScriptFailureKeepsCompletedApplyWork(t *testing.T) {
	fixture := newApplyFixture(t)
	writeLifecycleScript(t, fixture.paths, "after-apply", "run_fail", "exit 8")

	var output bytes.Buffer
	err := Run([]string{"--config-dir", fixture.root, "apply"}, bytes.NewReader(nil), &output, &output, "test")
	if err == nil || !strings.Contains(err.Error(), "after-apply/run_fail") {
		t.Fatalf("apply error = %v, want after script failure", err)
	}
	assertApplyPartChanges(t, fixture, true, true)
}

func TestLifecyclePreflightChecksBothPhasesBeforeMutation(t *testing.T) {
	fixture := newApplyFixture(t)
	beforeMarker := filepath.Join(t.TempDir(), "before-ran")
	writeLifecycleScript(t, fixture.paths, "before-apply", "run_probe", "touch "+shellQuote(beforeMarker))
	path := filepath.Join(fixture.paths.Scripts, "after-apply", "run_not_executable")
	writeTestFile(t, path, []byte("#!/bin/sh\nexit 0\n"))

	var output bytes.Buffer
	err := Run([]string{"--config-dir", fixture.root, "apply"}, bytes.NewReader(nil), &output, &output, "test")
	if err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("apply error = %v, want after-script preflight error", err)
	}
	for _, path := range []string{beforeMarker, fixture.managedTarget, fixture.aliasPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("preflight failure allowed mutation at %s: %v", path, err)
		}
	}
}

func TestToolChecksRunAfterBeforeScriptsAndValidateBeforeInstalling(t *testing.T) {
	fixture := newApplyFixture(t)
	beforeMarker := filepath.Join(t.TempDir(), "before-ran")
	installMarker := filepath.Join(t.TempDir(), "installed")
	writeLifecycleScript(t, fixture.paths, "before-apply", "run_probe", "touch "+shellQuote(beforeMarker))
	writeTestFile(t, fixture.paths.Tools, []byte(fmt.Sprintf(`[[tool]]
name = "first"
check = "false"

[tool.install]
macos = "touch %s"

[tool.install.linux]
default = "touch %s"

[[tool]]
name = "second"
check = "false"
`, shellQuote(installMarker), shellQuote(installMarker))))

	var output bytes.Buffer
	err := Run([]string{"--config-dir", fixture.root, "apply", "--only", "tools"}, bytes.NewReader(nil), &output, &output, "test")
	if err == nil || !strings.Contains(err.Error(), "second") {
		t.Fatalf("apply error = %v, want second Tool validation error", err)
	}
	if _, err := os.Stat(beforeMarker); err != nil {
		t.Errorf("before script did not run before Tool checks: %v", err)
	}
	if _, err := os.Stat(installMarker); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Tool validation failure allowed an install: %v", err)
	}
}

func TestToolApplyRechecksAfterBeforeScripts(t *testing.T) {
	fixture := newApplyFixture(t)
	trace := filepath.Join(t.TempDir(), "trace")
	installed := filepath.Join(t.TempDir(), "installed")
	writeTestFile(t, fixture.paths.Tools, []byte(fmt.Sprintf(`[[tool]]
name = "probe"
check = "printf 'check\\n' >> %s; test -f %s"

[tool.install]
macos = "printf 'install\\n' >> %s"

[tool.install.linux]
default = "printf 'install\\n' >> %s"
`, shellQuote(trace), shellQuote(installed), shellQuote(trace), shellQuote(trace))))
	writeLifecycleScript(t, fixture.paths, "before-apply", "run_install", fmt.Sprintf(`
printf 'before\n' >> %s
touch %s`, shellQuote(trace), shellQuote(installed)))

	var output bytes.Buffer
	if err := Run([]string{"--config-dir", fixture.root, "apply", "--only", "tools"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatalf("apply error = %v\n%s", err, output.String())
	}
	if got, want := strings.Split(strings.TrimSpace(readTestFile(t, trace)), "\n"), []string{"before", "check"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("trace = %v, want %v", got, want)
	}
}

func TestSelectedPartPreflightCompletesBeforeBeforeScripts(t *testing.T) {
	fixture := newApplyFixture(t)
	marker := filepath.Join(t.TempDir(), "before-ran")
	writeLifecycleScript(t, fixture.paths, "before-apply", "run_probe", "touch "+shellQuote(marker))
	if err := os.MkdirAll(filepath.Dir(fixture.aliasPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.aliasPath, []byte("user aliases\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err := Run([]string{"--config-dir", fixture.root, "apply"}, bytes.NewReader(nil), &output, &output, "test")
	if err == nil || !strings.Contains(err.Error(), "not managed") {
		t.Fatalf("apply error = %v, want Alias preflight failure", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("before script ran before Alias preflight: %v", err)
	}
}

func TestSelectedPartFailureSkipsAfterScripts(t *testing.T) {
	fixture := newApplyFixture(t)
	marker := filepath.Join(t.TempDir(), "after-ran")
	writeLifecycleScript(t, fixture.paths, "after-apply", "run_probe", "touch "+shellQuote(marker))
	writeTestFile(t, fixture.paths.Tools, []byte(`[[tool]]
name = "probe"
check = "false"

[tool.install]
macos = "exit 5"

[tool.install.linux]
default = "exit 5"
`))

	var output bytes.Buffer
	if err := Run([]string{"--config-dir", fixture.root, "apply", "--only", "tools"}, bytes.NewReader(nil), &output, &output, "test"); err == nil {
		t.Fatal("apply returned no Tool failure")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("after script ran after selected part failed: %v", err)
	}
}

func TestDryApplyReportsScriptsInApplyOrderWithoutExecuting(t *testing.T) {
	fixture := newApplyFixture(t)
	marker := filepath.Join(t.TempDir(), "script-ran")
	writeLifecycleScript(t, fixture.paths, "before-apply", "run_once_before", "touch "+shellQuote(marker))
	writeLifecycleScript(t, fixture.paths, "after-apply", "run_onchange_after", "touch "+shellQuote(marker))

	var output bytes.Buffer
	if err := Run([]string{"--config-dir", fixture.root, "apply", "--dry"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatal(err)
	}
	ordered := []string{
		"Would run lifecycle script before-apply/run_once_before",
		"Would check tool probe",
		"Would link ",
		"Would render aliases",
		"Would run lifecycle script after-apply/run_onchange_after",
	}
	last := -1
	for _, want := range ordered {
		index := strings.Index(output.String(), want)
		if index < 0 || index < last {
			t.Errorf("dry output missing or misordered %q:\n%s", want, output.String())
		}
		last = index
	}
	for _, path := range []string{
		marker,
		fixture.managedTarget,
		fixture.aliasPath,
		machinestate.Path(filepath.Dir(fixture.managedTarget), machinestate.ScriptsStateRelativePath),
		machinestate.Path(filepath.Dir(fixture.managedTarget), machinestate.ScriptsLockRelativePath),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("dry Apply changed %s: %v", path, err)
		}
	}
}

func TestLifecycleEnvironmentMatchesComponentsAndProfile(t *testing.T) {
	fixture := newApplyFixture(t)
	home := filepath.Dir(fixture.managedTarget)
	marker := filepath.Join(t.TempDir(), "environment")
	writeTestFile(t, filepath.Join(fixture.paths.Profiles, "Work.toml"), []byte(`aliases = ["ll"]
managed_home = [".managed"]
`))
	writeLifecycleScript(t, fixture.paths, "before-apply", "run_environment", fmt.Sprintf(`printf '%%s|%%s|%%s|%%s|%%s\n' "$XOLDOT" "$XOLDOT_CONFIG_DIR" "$XOLDOT_TARGET_HOME" "$XOLDOT_APPLY_COMPONENTS" "$XOLDOT_PROFILE" > %s`, shellQuote(marker)))

	var output bytes.Buffer
	if err := Run(
		[]string{"--config-dir", fixture.root, "apply", "--only", "aliases", "--only", "managed-home", "--profile", "WoRk"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	); err != nil {
		t.Fatalf("apply error = %v\n%s", err, output.String())
	}
	want := fmt.Sprintf("1|%s|%s|managed-home,aliases|work\n", fixture.root, home)
	if got := readTestFile(t, marker); got != want {
		t.Errorf("environment = %q, want %q", got, want)
	}
}

func TestLifecycleScriptsComposeWithManagedHomeBackup(t *testing.T) {
	fixture := newApplyFixture(t)
	home := filepath.Dir(fixture.managedTarget)
	trace := filepath.Join(t.TempDir(), "trace")
	writeTestFile(t, fixture.managedTarget, []byte("local conflict\n"))
	writeLifecycleScript(t, fixture.paths, "before-apply", "run_before_backup", fmt.Sprintf(`
test -f %s
test ! -L %s
printf 'before\n' >> %s`, shellQuote(fixture.managedTarget), shellQuote(fixture.managedTarget), shellQuote(trace)))
	backupManifests := shellQuote(machinestate.Path(home, machinestate.BackupsRelativePath)) + "/*/manifest.json"
	writeLifecycleScript(t, fixture.paths, "after-apply", "run_after_backup", fmt.Sprintf(`
test -L %s
set -- %s
test -f "$1"
printf 'after\n' >> %s`, shellQuote(fixture.managedTarget), backupManifests, shellQuote(trace)))

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
	if got, want := strings.Split(strings.TrimSpace(readTestFile(t, trace)), "\n"), []string{"before", "after"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("trace = %v, want %v", got, want)
	}
	if !strings.Contains(output.String(), "Backup ID:") {
		t.Errorf("backup apply output = %q", output.String())
	}
}

func TestStatusAndDiffReportScriptsWithoutExecutingOrWritingState(t *testing.T) {
	root, home := inspectionFixture(t)
	paths := config.NewPaths(root)
	marker := filepath.Join(t.TempDir(), "script-ran")
	writeLifecycleScript(t, paths, "before-apply", "run_once_before", "touch "+shellQuote(marker))
	writeLifecycleScript(t, paths, "after-apply", "run_onchange_after", "touch "+shellQuote(marker))

	for _, command := range []string{"status", "diff"} {
		var output bytes.Buffer
		if err := Run([]string{"--config-dir", root, command}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
			t.Fatalf("%s error = %v", command, err)
		}
		for _, path := range []string{"before-apply/run_once_before", "after-apply/run_onchange_after"} {
			if !strings.Contains(output.String(), path) {
				t.Errorf("%s output does not report %s:\n%s", command, path, output.String())
			}
		}
		for _, path := range []string{
			marker,
			machinestate.Path(home, machinestate.ScriptsStateRelativePath),
			machinestate.Path(home, machinestate.ScriptsLockRelativePath),
		} {
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s changed %s: %v", command, path, err)
			}
		}
	}
}

func TestDiffHelpMentionsLifecycleScripts(t *testing.T) {
	var output bytes.Buffer
	if err := Run([]string{"diff", "--help"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "lifecycle script") {
		t.Errorf("diff help does not mention lifecycle scripts:\n%s", output.String())
	}
}

func TestStatusAndDiffReportOnlyEligibleScripts(t *testing.T) {
	root, _ := inspectionFixture(t)
	paths := config.NewPaths(root)
	writeLifecycleScript(t, paths, "before-apply", "run_once_once", "true")
	writeLifecycleScript(t, paths, "before-apply", "run_always", "true")
	runCLI(t, root, "apply", "--only", "tools")

	for _, command := range []string{"status", "diff"} {
		output := runCLI(t, root, command)
		if strings.Contains(output, "run_once_once") || !strings.Contains(output, "run_always") {
			t.Errorf("%s output reports wrong eligibility:\n%s", command, output)
		}
	}
}

func writeLifecycleScript(t *testing.T, paths config.Paths, phase, name, body string) {
	t.Helper()
	path := filepath.Join(paths.Scripts, phase, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
