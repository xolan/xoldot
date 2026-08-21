package lifecyclescripts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/machinestate"
	"github.com/xolan/xoldot/internal/status"
)

func TestModesAdvanceOnlySuccessfulState(t *testing.T) {
	root, home := testLayout(t)
	trace := filepath.Join(t.TempDir(), "trace")
	writeScript(t, filepath.Join(root, "scripts", "before-apply", "run_10_always"), appendCommand(trace, "always"), 0o755)
	writeScript(t, filepath.Join(root, "scripts", "before-apply", "run_once_20_once"), appendCommand(trace, "once"), 0o755)
	onChange := filepath.Join(root, "scripts", "before-apply", "run_onchange_30_changed")
	writeScript(t, onChange, appendCommand(trace, "change-v1"), 0o755)

	runPhase(t, prepareTestPlan(t, root, home), BeforeApply)
	assertLines(t, trace, []string{"always", "once", "change-v1"})
	statePath := filepath.Join(home, filepath.FromSlash(stateRelativePath))
	state := readFile(t, statePath)
	if info, err := os.Stat(statePath); err != nil {
		t.Errorf("inspect state mode: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Errorf("state mode = %v, want 0600", info.Mode().Perm())
	}
	if strings.Contains(state, "run_10_always") {
		t.Errorf("always-run script was persisted in state:\n%s", state)
	}
	for _, path := range []string{"before-apply/run_once_20_once", "before-apply/run_onchange_30_changed"} {
		if !strings.Contains(state, path) {
			t.Errorf("state does not contain %q:\n%s", path, state)
		}
	}

	runPhase(t, prepareTestPlan(t, root, home), BeforeApply)
	assertLines(t, trace, []string{"always", "once", "change-v1", "always"})

	writeScript(t, onChange, appendCommand(trace, "change-v2"), 0o755)
	runPhase(t, prepareTestPlan(t, root, home), BeforeApply)
	assertLines(t, trace, []string{"always", "once", "change-v1", "always", "always", "change-v2"})
}

func TestOnChangeFailureKeepsLastSuccessfulDigest(t *testing.T) {
	root, home := testLayout(t)
	trace := filepath.Join(t.TempDir(), "trace")
	path := filepath.Join(root, "scripts", "after-apply", "run_onchange_probe")
	writeScript(t, path, appendCommand(trace, "v1"), 0o755)
	runPhase(t, prepareTestPlan(t, root, home), AfterApply)
	statePath := filepath.Join(home, filepath.FromSlash(stateRelativePath))
	stateV1 := readFile(t, statePath)

	writeScript(t, path, appendCommand(trace, "failed")+"\nexit 6", 0o755)
	plan := prepareTestPlan(t, root, home)
	var output bytes.Buffer
	if err := plan.Run(AfterApply, strings.NewReader(""), &output, &output, testReporter(&output)); err == nil {
		t.Fatal("changed script failure returned no error")
	}
	if got := readFile(t, statePath); got != stateV1 {
		t.Errorf("failed on-change script advanced state:\n%s", got)
	}

	writeScript(t, path, appendCommand(trace, "v2"), 0o755)
	runPhase(t, prepareTestPlan(t, root, home), AfterApply)
	assertLines(t, trace, []string{"v1", "failed", "v2"})
}

func TestOnceStateUsesPhaseRelativePathAndSurvivesRemoval(t *testing.T) {
	root, home := testLayout(t)
	trace := filepath.Join(t.TempDir(), "trace")
	before := filepath.Join(root, "scripts", "before-apply", "run_once_same")
	after := filepath.Join(root, "scripts", "after-apply", "run_once_same")
	writeScript(t, before, appendCommand(trace, "before"), 0o755)
	writeScript(t, after, appendCommand(trace, "after"), 0o755)
	plan := prepareTestPlan(t, root, home)
	runPhase(t, plan, BeforeApply)
	runPhase(t, plan, AfterApply)
	assertLines(t, trace, []string{"before", "after"})

	if err := os.Remove(before); err != nil {
		t.Fatal(err)
	}
	writeScript(t, filepath.Join(root, "scripts", "before-apply", "run_once_renamed"), appendCommand(trace, "renamed"), 0o755)
	runPhase(t, prepareTestPlan(t, root, home), BeforeApply)
	writeScript(t, before, appendCommand(trace, "old-path"), 0o755)
	runPhase(t, prepareTestPlan(t, root, home), BeforeApply)
	assertLines(t, trace, []string{"before", "after", "renamed"})
}

func TestScriptsRunInBytewiseFilenameAndPhaseOrder(t *testing.T) {
	root, home := testLayout(t)
	trace := filepath.Join(t.TempDir(), "trace")
	for _, script := range []struct {
		phase Phase
		name  string
		line  string
	}{
		{phase: BeforeApply, name: "run_z", line: "before-z"},
		{phase: BeforeApply, name: "run_A", line: "before-A"},
		{phase: BeforeApply, name: "run_a", line: "before-a"},
		{phase: AfterApply, name: "run_b", line: "after-b"},
		{phase: AfterApply, name: "run_0", line: "after-0"},
	} {
		writeScript(t, filepath.Join(root, "scripts", string(script.phase), script.name), appendCommand(trace, script.line), 0o755)
	}
	plan := prepareTestPlan(t, root, home)
	runPhase(t, plan, BeforeApply)
	runPhase(t, plan, AfterApply)
	assertLines(t, trace, []string{"before-A", "before-a", "before-z", "after-0", "after-b"})
}

func TestFailedScriptDoesNotAdvanceItsState(t *testing.T) {
	root, home := testLayout(t)
	trace := filepath.Join(t.TempDir(), "trace")
	writeScript(t, filepath.Join(root, "scripts", "before-apply", "run_once_10_success"), appendCommand(trace, "success"), 0o755)
	failed := filepath.Join(root, "scripts", "before-apply", "run_once_20_failure")
	writeScript(t, failed, "exit 9", 0o755)

	plan := prepareTestPlan(t, root, home)
	var output bytes.Buffer
	err := plan.Run(BeforeApply, strings.NewReader(""), &output, &output, testReporter(&output))
	if err == nil || !strings.Contains(err.Error(), "run_once_20_failure") {
		t.Fatalf("Run() error = %v, want failed script", err)
	}
	state := readFile(t, filepath.Join(home, filepath.FromSlash(stateRelativePath)))
	if !strings.Contains(state, "run_once_10_success") || strings.Contains(state, "run_once_20_failure") {
		t.Fatalf("state advanced the wrong scripts:\n%s", state)
	}

	writeScript(t, failed, appendCommand(trace, "recovered"), 0o755)
	runPhase(t, prepareTestPlan(t, root, home), BeforeApply)
	assertLines(t, trace, []string{"success", "recovered"})
}

func TestRunExecutesDirectlyAndInheritsStreamsAndEnvironment(t *testing.T) {
	root, home := testLayout(t)
	path := filepath.Join(root, "scripts", "before-apply", "run_contract")
	writeScript(t, path, `read -r input
printf '%s|%s|%s|%s|%s|%s\n' "$input" "$XOLDOT" "$XOLDOT_CONFIG_DIR" "$XOLDOT_TARGET_HOME" "$XOLDOT_APPLY_COMPONENTS" "$XOLDOT_PROFILE"
printf 'child error\n' >&2`, 0o755)
	catalog, err := Load(root, filepath.Join(root, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := catalog.Prepare(Environment{
		ConfigDir:  root,
		TargetHome: home,
		Components: "tools,aliases",
		Profile:    "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr, reports bytes.Buffer
	if err := plan.Run(BeforeApply, strings.NewReader("from stdin\n"), &stdout, &stderr, testReporter(&reports)); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("from stdin|1|%s|%s|tools,aliases|work\n", root, home)
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.String() != "child error\n" {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunRemovesInheritedProfileWhenNoneIsSelected(t *testing.T) {
	root, home := testLayout(t)
	t.Setenv("XOLDOT_PROFILE", "stale")
	path := filepath.Join(root, "scripts", "before-apply", "run_profile")
	writeScript(t, path, `if [ "${XOLDOT_PROFILE+x}" = x ]; then exit 4; fi`, 0o755)
	catalog, err := Load(root, filepath.Join(root, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := catalog.Prepare(Environment{ConfigDir: root, TargetHome: home, Components: "tools"})
	if err != nil {
		t.Fatal(err)
	}
	runPhase(t, plan, BeforeApply)
}

func TestLoadRejectsInvalidEntriesAndEscapes(t *testing.T) {
	for _, test := range []struct {
		name      string
		prepare   func(*testing.T, string)
		wantError string
	}{
		{
			name: "unknown prefix",
			prepare: func(t *testing.T, root string) {
				writeScript(t, filepath.Join(root, "scripts", "before-apply", "bootstrap"), "true", 0o755)
			},
			wantError: "filename must start",
		},
		{
			name: "non executable",
			prepare: func(t *testing.T, root string) {
				writeScript(t, filepath.Join(root, "scripts", "before-apply", "run_probe"), "true", 0o644)
			},
			wantError: "not executable",
		},
		{
			name: "directory",
			prepare: func(t *testing.T, root string) {
				if err := os.Mkdir(filepath.Join(root, "scripts", "before-apply", "run_directory"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "not an ordinary file",
		},
		{
			name: "script symlink escape",
			prepare: func(t *testing.T, root string) {
				outside := filepath.Join(t.TempDir(), "outside")
				writeScript(t, outside, "true", 0o755)
				if err := os.Symlink(outside, filepath.Join(root, "scripts", "before-apply", "run_escape")); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "outside the scripts directory",
		},
		{
			name: "phase symlink escape",
			prepare: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "scripts", "before-apply")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "scripts", "before-apply")); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "resolves outside",
		},
		{
			name: "scripts root symlink escape",
			prepare: func(t *testing.T, root string) {
				if err := os.RemoveAll(filepath.Join(root, "scripts")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "scripts")); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "outside the Configuration",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := testLayout(t)
			test.prepare(t, root)
			_, err := Load(root, filepath.Join(root, "scripts"))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Load() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestLoadAcceptsSymlinkWithinScriptsDirectory(t *testing.T) {
	root, home := testLayout(t)
	target := filepath.Join(root, "scripts", "shared")
	writeScript(t, target, "true", 0o755)
	if err := os.Symlink(target, filepath.Join(root, "scripts", "before-apply", "run_linked")); err != nil {
		t.Fatal(err)
	}
	runPhase(t, prepareTestPlan(t, root, home), BeforeApply)
}

func TestInspectIsReadOnlyAndRejectsEscapingState(t *testing.T) {
	root, _ := testLayout(t)
	writeScript(t, filepath.Join(root, "scripts", "before-apply", "run_once_probe"), "true", 0o755)
	catalog, err := Load(root, filepath.Join(root, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	missingHome := filepath.Join(t.TempDir(), "missing-home")
	if _, err := catalog.Inspect(missingHome); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missingHome); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Inspect created Target home: %v", err)
	}

	home := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, ".local")); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Inspect(home); err == nil {
		t.Fatal("Inspect accepted lifecycle state escaping Target home")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside state directory changed: entries = %v, error = %v", entries, err)
	}
}

func TestInspectRejectsMalformedAndSymlinkedState(t *testing.T) {
	for _, test := range []struct {
		name      string
		prepare   func(*testing.T, string)
		wantError string
	}{
		{
			name: "malformed",
			prepare: func(t *testing.T, path string) {
				writeFileMode(t, path, []byte(`{"version":99,"scripts":[]}`), 0o600)
			},
			wantError: "unsupported lifecycle script state version",
		},
		{
			name: "symlink",
			prepare: func(t *testing.T, path string) {
				target := filepath.Join(t.TempDir(), "state.json")
				writeFileMode(t, target, []byte(`{"version":1,"scripts":[]}`), 0o600)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "not an ordinary file",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, home := testLayout(t)
			writeScript(t, filepath.Join(root, "scripts", "before-apply", "run_once_probe"), "true", 0o755)
			catalog, err := Load(root, filepath.Join(root, "scripts"))
			if err != nil {
				t.Fatal(err)
			}
			test.prepare(t, filepath.Join(home, filepath.FromSlash(stateRelativePath)))
			_, err = catalog.Inspect(home)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Inspect() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestPrepareRequiresExecutionEnvironment(t *testing.T) {
	root, home := testLayout(t)
	writeScript(t, filepath.Join(root, "scripts", "before-apply", "run_probe"), "true", 0o755)
	catalog, err := Load(root, filepath.Join(root, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	complete := Environment{ConfigDir: root, TargetHome: home, Components: "tools"}
	for _, test := range []struct {
		name      string
		configure func(*Environment)
		want      string
	}{
		{name: "Configuration directory", configure: func(environment *Environment) { environment.ConfigDir = "" }, want: "XOLDOT_CONFIG_DIR"},
		{name: "Target home", configure: func(environment *Environment) { environment.TargetHome = "" }, want: "XOLDOT_TARGET_HOME"},
		{name: "Apply components", configure: func(environment *Environment) { environment.Components = "" }, want: "XOLDOT_APPLY_COMPONENTS"},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := complete
			test.configure(&environment)
			_, err := catalog.Prepare(environment)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Prepare() error = %v, want %s", err, test.want)
			}
		})
	}
}

func TestRunRejectsScriptChangedAfterPreparation(t *testing.T) {
	root, home := testLayout(t)
	marker := filepath.Join(t.TempDir(), "marker")
	path := filepath.Join(root, "scripts", "before-apply", "run_probe")
	writeScript(t, path, "true", 0o755)
	plan := prepareTestPlan(t, root, home)
	writeScript(t, path, appendCommand(marker, "changed"), 0o755)

	var output bytes.Buffer
	err := plan.Run(BeforeApply, strings.NewReader(""), &output, &output, testReporter(&output))
	if err == nil || !strings.Contains(err.Error(), "changed after preparation") {
		t.Fatalf("Run() error = %v, want changed-after-preparation error", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("changed script ran: %v", err)
	}
}

func TestRunRejectsSymlinkedStateLock(t *testing.T) {
	root, home := testLayout(t)
	marker := filepath.Join(t.TempDir(), "ran")
	writeScript(t, filepath.Join(root, "scripts", "before-apply", "run_once_probe"), "touch "+shellWord(marker), 0o755)
	plan := prepareTestPlan(t, root, home)
	outside := filepath.Join(t.TempDir(), "outside-lock")
	writeFileMode(t, outside, nil, 0o644)
	lock := machinestate.Path(home, machinestate.ScriptsLockRelativePath)
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, lock); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err := plan.Run(BeforeApply, strings.NewReader(""), &output, &output, testReporter(&output))
	if err == nil || !strings.Contains(err.Error(), "state lock") || !strings.Contains(err.Error(), "not an ordinary file") {
		t.Fatalf("Run() error = %v, want state-lock type error", err)
	}
	assertPathMissing(t, marker)
}

func TestStateWriteFailureDoesNotAdvanceReturnedState(t *testing.T) {
	_, home := testLayout(t)
	store, _, err := inspectStateStore(home)
	if err != nil {
		t.Fatal(err)
	}
	const path = "before-apply/run_once_probe"
	digest := "sha256:" + strings.Repeat("0", sha256HexLength)
	statePath := filepath.Join(home, filepath.FromSlash(stateRelativePath))

	state, err := store.withLockedState(func(transaction *stateTransaction) error {
		transaction.recordSuccess(path, digest)
		return os.Mkdir(statePath, 0o700)
	})
	if err == nil || !strings.Contains(err.Error(), "not an ordinary file") {
		t.Fatalf("withLockedState() error = %v, want ordinary-file error", err)
	}
	if _, exists := state[path]; exists {
		t.Errorf("failed state write returned a recorded success: %#v", state)
	}
}

func TestStalePlanReloadsAndMergesStateBeforeRunning(t *testing.T) {
	root, home := testLayout(t)
	trace := filepath.Join(t.TempDir(), "trace")
	writeScript(t, filepath.Join(root, "scripts", "before-apply", "run_once_before"), appendCommand(trace, "before"), 0o755)
	writeScript(t, filepath.Join(root, "scripts", "after-apply", "run_once_after"), appendCommand(trace, "after"), 0o755)
	first := prepareTestPlan(t, root, home)
	stale := prepareTestPlan(t, root, home)

	runPhase(t, first, AfterApply)
	runPhase(t, stale, BeforeApply)
	runPhase(t, stale, AfterApply)
	assertLines(t, trace, []string{"after", "before"})

	inspection, err := first.store.load()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"before-apply/run_once_before", "after-apply/run_once_after"} {
		if _, exists := inspection[path]; !exists {
			t.Errorf("state does not contain %s", path)
		}
	}
}

func TestConcurrentStalePlansRunOnceScriptOnlyOnce(t *testing.T) {
	root, home := testLayout(t)
	trace := filepath.Join(t.TempDir(), "trace")
	writeScript(t, filepath.Join(root, "scripts", "before-apply", "run_once_probe"), appendCommand(trace, "ran"), 0o755)
	first := prepareTestPlan(t, root, home)
	second := prepareTestPlan(t, root, home)
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	results := make(chan error, 2)
	firstReporter := status.ReporterFunc(func(_ status.Kind, text string) error {
		if strings.HasPrefix(text, "Running lifecycle script ") {
			close(firstEntered)
			<-releaseFirst
		}
		return nil
	})
	go func() {
		results <- first.Run(BeforeApply, strings.NewReader(""), io.Discard, io.Discard, firstReporter)
	}()
	<-firstEntered
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		results <- second.Run(BeforeApply, strings.NewReader(""), io.Discard, io.Discard, status.ReporterFunc(func(status.Kind, string) error { return nil }))
	}()
	<-secondStarted
	close(releaseFirst)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	assertLines(t, trace, []string{"ran"})
}

func TestRunExecutesVerifiedFileAfterPathReplacement(t *testing.T) {
	root, home := testLayout(t)
	originalMarker := filepath.Join(t.TempDir(), "original")
	replacementMarker := filepath.Join(t.TempDir(), "replacement")
	path := filepath.Join(root, "scripts", "before-apply", "run_probe")
	writeScript(t, path, "touch "+shellWord(originalMarker), 0o755)
	plan := prepareTestPlan(t, root, home)
	replacement := filepath.Join(root, "scripts", "replacement")
	writeScript(t, replacement, "touch "+shellWord(replacementMarker), 0o755)
	reporter := replacingReporter(t, func() error { return os.Rename(replacement, path) })

	if err := plan.Run(BeforeApply, strings.NewReader(""), io.Discard, io.Discard, reporter); err != nil {
		t.Fatal(err)
	}
	assertPathExists(t, originalMarker)
	assertPathMissing(t, replacementMarker)
}

func TestRunExecutesVerifiedFileAfterSymlinkRetarget(t *testing.T) {
	root, home := testLayout(t)
	originalMarker := filepath.Join(t.TempDir(), "original")
	replacementMarker := filepath.Join(t.TempDir(), "replacement")
	original := filepath.Join(root, "scripts", "original")
	replacement := filepath.Join(root, "scripts", "replacement")
	writeScript(t, original, "touch "+shellWord(originalMarker), 0o755)
	writeScript(t, replacement, "touch "+shellWord(replacementMarker), 0o755)
	path := filepath.Join(root, "scripts", "before-apply", "run_probe")
	if err := os.Symlink(original, path); err != nil {
		t.Fatal(err)
	}
	plan := prepareTestPlan(t, root, home)
	reporter := replacingReporter(t, func() error {
		if err := os.Remove(path); err != nil {
			return err
		}
		return os.Symlink(replacement, path)
	})

	if err := plan.Run(BeforeApply, strings.NewReader(""), io.Discard, io.Discard, reporter); err != nil {
		t.Fatal(err)
	}
	assertPathExists(t, originalMarker)
	assertPathMissing(t, replacementMarker)
}

func TestPreviewReportsEligibleScriptsWithoutExecutingOrWritingState(t *testing.T) {
	root, home := testLayout(t)
	marker := filepath.Join(t.TempDir(), "marker")
	writeScript(t, filepath.Join(root, "scripts", "after-apply", "run_once_probe"), appendCommand(marker, "ran"), 0o755)
	plan := prepareTestPlan(t, root, home)
	var output bytes.Buffer
	if err := plan.Preview(AfterApply, testReporter(&output)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Would run lifecycle script after-apply/run_once_probe") {
		t.Errorf("preview = %q", output.String())
	}
	for _, path := range []string{
		marker,
		filepath.Join(home, filepath.FromSlash(stateRelativePath)),
		filepath.Join(home, filepath.FromSlash(machinestate.ScriptsLockRelativePath)),
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("preview created %s: %v", path, err)
		}
	}
}

func replacingReporter(t *testing.T, replace func() error) status.Reporter {
	t.Helper()
	replaced := false
	return status.ReporterFunc(func(_ status.Kind, text string) error {
		if !replaced && strings.HasPrefix(text, "Running lifecycle script ") {
			replaced = true
			return replace()
		}
		return nil
	})
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s: %v", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be missing: %v", path, err)
	}
}

func testLayout(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	for _, phase := range phases {
		if err := os.MkdirAll(filepath.Join(root, "scripts", string(phase)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, home
}

func prepareTestPlan(t *testing.T, root, home string) Plan {
	t.Helper()
	catalog, err := Load(root, filepath.Join(root, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := catalog.Prepare(Environment{ConfigDir: root, TargetHome: home, Components: "tools,managed-home,aliases"})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func runPhase(t *testing.T, plan Plan, phase Phase) {
	t.Helper()
	var output bytes.Buffer
	if err := plan.Run(phase, strings.NewReader(""), &output, &output, testReporter(&output)); err != nil {
		t.Fatalf("Run(%s) error = %v\n%s", phase, err, output.String())
	}
}

func writeScript(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	writeFileMode(t, path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), mode)
}

func writeFileMode(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func appendCommand(path, line string) string {
	return fmt.Sprintf("printf '%%s\\n' %s >> %s", shellWord(line), shellWord(path))
}

func shellWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func testReporter(output *bytes.Buffer) status.Reporter {
	return status.ReporterFunc(func(_ status.Kind, text string) error {
		_, err := output.WriteString(text + "\n")
		return err
	})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertLines(t *testing.T, path string, want []string) {
	t.Helper()
	got := strings.Split(strings.TrimSpace(readFile(t, path)), "\n")
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("lines = %v, want %v", got, want)
	}
}
