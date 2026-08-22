package managedhome

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xolan/xoldot/internal/status"
)

func TestAdoptPreservesBytesAndPermissionsAndRecordsOwnership(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	configRoot := filepath.Join(home, ".config", "xoldot")
	managed := filepath.Join(configRoot, "files", "home")
	source := filepath.Join(home, ".config", "example", "settings")
	for _, directory := range []string{filepath.Dir(source), managed} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	contents := []byte("first\x00second\n")
	if err := os.WriteFile(source, contents, 0o751); err != nil {
		t.Fatal(err)
	}

	var output strings.Builder
	var kinds []status.Kind
	reporter := recordingReporter(&output, &kinds)
	if err := Adopt(source, managed, home, configRoot, reporter, false); err != nil {
		t.Fatalf("Adopt() error = %v", err)
	}
	destination := filepath.Join(managed, ".config", "example", "settings")
	assertAdoptedFile(t, source, destination, contents, 0o751)
	if got, want := output.String(), fmt.Sprintf("Moving %s -> %s\nLinking %s -> %s\n", source, destination, source, destination); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	if len(kinds) != 2 || kinds[0] != status.Progress || kinds[1] != status.Progress {
		t.Errorf("status kinds = %v, want two progress reports before commit", kinds)
	}
	ledger, err := loadLedger(filepath.Join(home, ".local", "state", "xoldot", "links.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Links) != 1 || ledger.Links[0] != (linkRecord{Target: source, Destination: destination}) {
		t.Errorf("ledger links = %#v", ledger.Links)
	}
}

func TestAdoptDryReportsExactMoveAndLinkWithoutChangingTrees(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	managed := filepath.Join(root, "configuration", "files", "home")
	source := filepath.Join(home, ".vimrc")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("set number\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	var output strings.Builder
	var kinds []status.Kind
	reporter := recordingReporter(&output, &kinds)
	if err := Adopt(source, managed, home, filepath.Join(root, "configuration"), reporter, true); err != nil {
		t.Fatalf("Adopt() error = %v", err)
	}
	destination := filepath.Join(managed, ".vimrc")
	if got, want := output.String(), fmt.Sprintf("Would move %s -> %s\nWould link %s -> %s\n", source, destination, source, destination); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	if len(kinds) != 2 || kinds[0] != status.Progress || kinds[1] != status.Progress {
		t.Errorf("status kinds = %v, want two progress reports", kinds)
	}
	data, err := os.ReadFile(source)
	if err != nil || string(data) != "set number\n" {
		t.Errorf("source changed: %q, %v", data, err)
	}
	if _, err := os.Lstat(managed); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry adoption created managed content: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".local")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry adoption created link state: %v", err)
	}
}

func TestAdoptStagesBeforeChangingSource(t *testing.T) {
	plan, contents, mode := newAdoptionPlan(t)
	err := plan.apply(discardReporter, false, func(step transactionStep) error {
		if step != transactionStepDestinationStaged {
			return nil
		}
		data, readErr := os.ReadFile(plan.Source)
		if readErr != nil || string(data) != string(contents) {
			t.Errorf("source changed before staging completed: %q, %v", data, readErr)
		}
		staged, readErr := os.ReadFile(plan.Destination)
		if readErr != nil || string(staged) != string(contents) {
			t.Errorf("staged bytes = %q, %v", staged, readErr)
		}
		return fmt.Errorf("stop after cross-device-safe staging")
	})
	if err == nil || !strings.Contains(err.Error(), "cross-device-safe staging") {
		t.Fatalf("Apply() error = %v", err)
	}
	assertRestoredAdoption(t, plan, contents, mode)
}

func TestAdoptAcrossFilesystems(t *testing.T) {
	managedFilesystem, err := os.MkdirTemp("/dev/shm", "xoldot-adopt-test-*")
	if err != nil {
		t.Skipf("no separate temporary filesystem is available: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(managedFilesystem); err != nil {
			t.Errorf("remove cross-filesystem test directory: %v", err)
		}
	})
	home := t.TempDir()
	probe := filepath.Join(home, "rename-probe")
	writeTestFile(t, probe, "probe")
	probeDestination := filepath.Join(managedFilesystem, "rename-probe")
	if err := os.Rename(probe, probeDestination); err == nil {
		_ = os.Remove(probeDestination)
		t.Skip("temporary directories are on the same filesystem")
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}

	configRoot := filepath.Join(managedFilesystem, "configuration")
	managed := filepath.Join(configRoot, "files", "home")
	source := filepath.Join(home, ".vimrc")
	contents := []byte("set number\n")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, contents, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Adopt(source, managed, home, configRoot, discardReporter, false); err != nil {
		t.Fatalf("cross-filesystem Adopt() error = %v", err)
	}
	assertAdoptedFile(t, source, filepath.Join(managed, ".vimrc"), contents, 0o640)
}

func TestAdoptRefusesAStagedDestinationChangedToASymlink(t *testing.T) {
	plan, contents, mode := newAdoptionPlan(t)
	err := plan.apply(discardReporter, false, func(step transactionStep) error {
		if step != transactionStepDestinationStaged {
			return nil
		}
		if err := os.Remove(plan.Destination); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(plan.Source, plan.Destination); err != nil {
			t.Fatal(err)
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to roll back file") {
		t.Fatalf("Apply() error = %v", err)
	}
	assertRestoredSource(t, plan.Source, contents, mode)
	if destination, readErr := os.Readlink(plan.Destination); readErr != nil {
		t.Fatalf("replacement symlink was removed: %v", readErr)
	} else if destination != plan.Source {
		t.Errorf("replacement destination = %q, want %q", destination, plan.Source)
	}
}

func TestAdoptRollbackPreservesAReplacementManagedDirectory(t *testing.T) {
	plan, contents, mode := newAdoptionPlan(t)
	createdDirectory := filepath.Join(plan.linkPlan.layout.ManagedRoot, ".config")
	replacement := filepath.Join(createdDirectory, "replacement")
	err := plan.apply(discardReporter, false, func(step transactionStep) error {
		if step != transactionStepDestinationStaged {
			return nil
		}
		if err := os.RemoveAll(createdDirectory); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, replacement, "replacement")
		return fmt.Errorf("stop after directory replacement")
	})
	if err == nil || !strings.Contains(err.Error(), "roll back directory") {
		t.Fatalf("Apply() error = %v", err)
	}
	assertRestoredSource(t, plan.Source, contents, mode)
	if data, readErr := os.ReadFile(replacement); readErr != nil || string(data) != "replacement" {
		t.Errorf("replacement directory changed: %q, %v", data, readErr)
	}
}

func TestAdoptRefusesSourceParentSwapAfterPreparation(t *testing.T) {
	plan, contents, mode := newAdoptionPlan(t)
	sourceParent := filepath.Dir(plan.Source)
	originalParent := sourceParent + "-original"
	outsideParent := t.TempDir()
	outsideSource := filepath.Join(outsideParent, filepath.Base(plan.Source))
	writeTestFile(t, outsideSource, "outside")
	if err := os.Rename(sourceParent, originalParent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideParent, sourceParent); err != nil {
		t.Fatal(err)
	}

	err := plan.Apply(discardReporter, false)
	if err == nil || !strings.Contains(err.Error(), "path escapes") {
		t.Fatalf("Apply() error = %v, want rooted source refusal", err)
	}
	assertRestoredSource(t, filepath.Join(originalParent, filepath.Base(plan.Source)), contents, mode)
	if data, readErr := os.ReadFile(outsideSource); readErr != nil || string(data) != "outside" {
		t.Errorf("outside source changed: %q, %v", data, readErr)
	}
	if _, statErr := os.Lstat(plan.Destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("managed destination was created: %v", statErr)
	}
}

func TestAdoptRefusesDestinationParentSwapAfterPreparation(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	configRoot := filepath.Join(root, "configuration")
	managed := filepath.Join(configRoot, "files", "home")
	source := filepath.Join(home, ".config", "app", "settings")
	destinationParent := filepath.Join(managed, ".config", "app")
	writeTestFile(t, source, "source")
	if err := os.MkdirAll(destinationParent, 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareAdoption(source, managed, home, configRoot)
	if err != nil {
		t.Fatal(err)
	}
	originalParent := destinationParent + "-original"
	outsideParent := t.TempDir()
	outsideDestination := filepath.Join(outsideParent, filepath.Base(plan.Destination))
	writeTestFile(t, outsideDestination, "outside")
	if err := os.Rename(destinationParent, originalParent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideParent, destinationParent); err != nil {
		t.Fatal(err)
	}

	err = plan.Apply(discardReporter, false)
	if err == nil || !strings.Contains(err.Error(), "path escapes") {
		t.Fatalf("Apply() error = %v, want rooted destination refusal", err)
	}
	if data, readErr := os.ReadFile(source); readErr != nil || string(data) != "source" {
		t.Errorf("source changed: %q, %v", data, readErr)
	}
	if data, readErr := os.ReadFile(outsideDestination); readErr != nil || string(data) != "outside" {
		t.Errorf("outside destination changed: %q, %v", data, readErr)
	}
}

func TestAdoptRollbackPreservesAReplacementAtTheSourcePath(t *testing.T) {
	plan, contents, _ := newAdoptionPlan(t)
	err := plan.apply(discardReporter, false, func(step transactionStep) error {
		if step != transactionStepLinkCreated {
			return nil
		}
		if err := os.Remove(plan.Source); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(plan.Source, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
		return fmt.Errorf("stop after source replacement")
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to restore") {
		t.Fatalf("Apply() error = %v", err)
	}
	if data, readErr := os.ReadFile(plan.Source); readErr != nil || string(data) != "replacement" {
		t.Errorf("replacement source changed: %q, %v", data, readErr)
	}
	backups, globErr := filepath.Glob(filepath.Join(filepath.Dir(plan.Source), ".xoldot-backup-*", "original"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(backups) != 1 {
		t.Fatalf("original backups = %v, want one", backups)
	}
	if !strings.Contains(err.Error(), backups[0]) {
		t.Errorf("Apply() error = %v, want backup path %s", err, backups[0])
	}
	if data, readErr := os.ReadFile(backups[0]); readErr != nil || string(data) != string(contents) {
		t.Errorf("preserved original = %q, %v", data, readErr)
	}
}

func TestAdoptRollsBackEveryTransactionStep(t *testing.T) {
	steps := []transactionStep{
		transactionStepDestinationStaged,
		transactionStepSourceBackedUp,
		transactionStepLinkCreated,
		transactionStepLedgerSaved,
	}
	for _, step := range steps {
		t.Run(string(step), func(t *testing.T) {
			plan, contents, mode := newAdoptionPlan(t)
			var kinds []status.Kind
			reporter := recordingReporter(io.Discard, &kinds)
			err := plan.apply(reporter, false, func(current transactionStep) error {
				if current == step {
					return fmt.Errorf("simulated %s failure", step)
				}
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "simulated") {
				t.Fatalf("Apply() error = %v", err)
			}
			for _, kind := range kinds {
				if kind == status.Success {
					t.Errorf("rollback path reported success: %v", kinds)
				}
			}
			assertRestoredAdoption(t, plan, contents, mode)
		})
	}
}

func recordingReporter(output io.Writer, kinds *[]status.Kind) status.Reporter {
	reporter := writerReporter(output)
	return status.ReporterFunc(func(kind status.Kind, text string) error {
		*kinds = append(*kinds, kind)
		return reporter.Report(kind, text)
	})
}

func TestAdoptRollsBackStatusWriteFailures(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("write_%d", failAt), func(t *testing.T) {
			plan, contents, mode := newAdoptionPlan(t)
			writes := 0
			reporter := status.ReporterFunc(func(status.Kind, string) error {
				writes++
				if writes == failAt {
					return fmt.Errorf("simulated output failure")
				}
				return nil
			})
			if err := plan.Apply(reporter, false); err == nil || !strings.Contains(err.Error(), "simulated output failure") {
				t.Fatalf("Apply() error = %v", err)
			}
			assertRestoredAdoption(t, plan, contents, mode)
		})
	}
}

func TestAdoptRestoresAnExistingOwnershipLedger(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	configRoot := filepath.Join(root, "configuration")
	managed := filepath.Join(configRoot, "files", "home")
	source := filepath.Join(home, "new")
	writeTestFile(t, source, "new")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(home, ".local", "state", "xoldot", "links.json")
	previous := []linkRecord{{
		Target:      filepath.Join(home, "old"),
		Destination: filepath.Join(managed, "old"),
	}}
	if err := saveLedger(ledgerPath, previous); err != nil {
		t.Fatal(err)
	}
	wantLedger, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareAdoption(source, managed, home, configRoot)
	if err != nil {
		t.Fatal(err)
	}
	err = plan.apply(discardReporter, false, func(step transactionStep) error {
		if step == transactionStepLedgerSaved {
			return fmt.Errorf("simulated post-ledger failure")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "post-ledger failure") {
		t.Fatalf("Apply() error = %v", err)
	}
	gotLedger, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotLedger) != string(wantLedger) {
		t.Errorf("restored ledger = %q, want %q", gotLedger, wantLedger)
	}
	data, err := os.ReadFile(source)
	if err != nil || string(data) != "new" {
		t.Errorf("source was not restored: %q, %v", data, err)
	}
	if _, err := os.Lstat(plan.Destination); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("managed destination remains after rollback: %v", err)
	}
}

func TestAdoptRollbackPreservesAReplacementOwnershipLedger(t *testing.T) {
	plan, contents, mode := newAdoptionPlan(t)
	ledgerPath := plan.linkPlan.layout.LedgerPath
	err := plan.apply(discardReporter, false, func(step transactionStep) error {
		if step != transactionStepLedgerSaved {
			return nil
		}
		if err := os.WriteFile(ledgerPath, []byte("replacement ledger\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return fmt.Errorf("stop after ledger replacement")
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to roll back managed link state") {
		t.Fatalf("Apply() error = %v", err)
	}
	if data, readErr := os.ReadFile(ledgerPath); readErr != nil || string(data) != "replacement ledger\n" {
		t.Errorf("replacement ledger changed: %q, %v", data, readErr)
	}
	assertRestoredSource(t, plan.Source, contents, mode)
}

func TestAdoptRejectsAStalePreparedLedgerBeforeMovingTheSource(t *testing.T) {
	plan, contents, mode := newAdoptionPlan(t)
	concurrentRecord := linkRecord{
		Target:      filepath.Join(plan.linkPlan.layout.Home, "concurrent"),
		Destination: filepath.Join(plan.linkPlan.layout.ManagedRoot, "concurrent"),
	}
	if err := saveLedger(plan.linkPlan.layout.LedgerPath, []linkRecord{concurrentRecord}); err != nil {
		t.Fatal(err)
	}

	err := plan.Apply(discardReporter, false)
	if err == nil || !strings.Contains(err.Error(), "changed after preparation") {
		t.Fatalf("Apply() error = %v", err)
	}
	assertRestoredSource(t, plan.Source, contents, mode)
	if _, statErr := os.Lstat(plan.Destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("managed destination was created: %v", statErr)
	}
	ledger, loadErr := loadLedger(plan.linkPlan.layout.LedgerPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(ledger.Links) != 1 || ledger.Links[0] != concurrentRecord {
		t.Errorf("concurrent ledger = %#v", ledger.Links)
	}
}

func TestConcurrentAdoptionsDoNotLoseLedgerOwnership(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	configRoot := filepath.Join(root, "configuration")
	managed := filepath.Join(configRoot, "files", "home")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	sources := []string{filepath.Join(home, "one"), filepath.Join(home, "two")}
	plans := make([]AdoptionPlan, len(sources))
	for index, source := range sources {
		writeTestFile(t, source, filepath.Base(source))
		plan, err := PrepareAdoption(source, managed, home, configRoot)
		if err != nil {
			t.Fatal(err)
		}
		plans[index] = plan
	}

	errorsByPlan := make([]error, len(plans))
	var wait sync.WaitGroup
	for index := range plans {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsByPlan[index] = plans[index].Apply(discardReporter, false)
		}()
	}
	wait.Wait()

	succeeded := -1
	for index, err := range errorsByPlan {
		if err == nil {
			if succeeded != -1 {
				t.Fatalf("both concurrent adoptions succeeded: %v", errorsByPlan)
			}
			succeeded = index
			continue
		}
		if !strings.Contains(err.Error(), "changed after preparation") {
			t.Errorf("plan %d error = %v", index, err)
		}
	}
	if succeeded == -1 {
		t.Fatalf("no concurrent adoption succeeded: %v", errorsByPlan)
	}
	ledger, err := loadLedger(filepath.Join(home, filepath.FromSlash(ledgerRelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Links) != 1 || ledger.Links[0].Target != sources[succeeded] {
		t.Errorf("ledger links = %#v, want ownership for %s", ledger.Links, sources[succeeded])
	}
	other := 1 - succeeded
	if data, readErr := os.ReadFile(sources[other]); readErr != nil || string(data) != filepath.Base(sources[other]) {
		t.Errorf("stale plan changed its source: %q, %v", data, readErr)
	}
}

func TestPrepareAdoptionRefusesUnsupportedAndUnsafePaths(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(t *testing.T, root, home, managed, configRoot string) string
		wantError string
	}{
		{
			name: "source symlink",
			prepare: func(t *testing.T, root, home, _, _ string) string {
				t.Helper()
				real := filepath.Join(home, "real")
				writeTestFile(t, real, "real")
				link := filepath.Join(home, "link")
				if err := os.Symlink(real, link); err != nil {
					t.Fatal(err)
				}
				return link
			},
			wantError: "is a symlink",
		},
		{
			name: "directory",
			prepare: func(t *testing.T, _, home, _, _ string) string {
				t.Helper()
				path := filepath.Join(home, "directory")
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantError: "not an ordinary file",
		},
		{
			name: "outside target home",
			prepare: func(t *testing.T, root, _, _, _ string) string {
				t.Helper()
				path := filepath.Join(root, "outside")
				writeTestFile(t, path, "outside")
				return path
			},
			wantError: "outside the target home",
		},
		{
			name: "recursive configuration path",
			prepare: func(t *testing.T, _, _, _, configRoot string) string {
				t.Helper()
				path := filepath.Join(configRoot, "local")
				writeTestFile(t, path, "recursive")
				return path
			},
			wantError: "recursive adoption",
		},
		{
			name: "existing destination",
			prepare: func(t *testing.T, _, home, managed, _ string) string {
				t.Helper()
				path := filepath.Join(home, ".vimrc")
				writeTestFile(t, path, "local")
				writeTestFile(t, filepath.Join(managed, ".vimrc"), "managed")
				return path
			},
			wantError: "already exists",
		},
		{
			name: "existing destination symlink",
			prepare: func(t *testing.T, root, home, managed, _ string) string {
				t.Helper()
				path := filepath.Join(home, ".vimrc")
				writeTestFile(t, path, "local")
				if err := os.MkdirAll(managed, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(managed, ".vimrc")); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantError: "already exists",
		},
		{
			name: "managed parent symlink",
			prepare: func(t *testing.T, _, home, managed, _ string) string {
				t.Helper()
				path := filepath.Join(home, ".config", "app", "settings")
				writeTestFile(t, path, "local")
				actual := filepath.Join(managed, "actual")
				if err := os.MkdirAll(actual, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(actual, filepath.Join(managed, ".config")); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantError: "contains a symlink",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			configRoot := filepath.Join(home, ".config", "xoldot")
			managed := filepath.Join(configRoot, "files", "home")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			source := test.prepare(t, root, home, managed, configRoot)
			if _, err := PrepareAdoption(source, managed, home, configRoot); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("PrepareAdoption() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestAdoptDoesNotLinkManagedSiblings(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	configRoot := filepath.Join(root, "configuration")
	managed := filepath.Join(configRoot, "files", "home")
	source := filepath.Join(home, "selected")
	sibling := filepath.Join(managed, "sibling")
	writeTestFile(t, source, "selected")
	writeTestFile(t, sibling, "sibling")

	if err := Adopt(source, managed, home, configRoot, discardReporter, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, "sibling")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("adoption linked sibling managed content: %v", err)
	}
}

func newAdoptionPlan(t *testing.T) (AdoptionPlan, []byte, os.FileMode) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	configRoot := filepath.Join(root, "configuration")
	managed := filepath.Join(configRoot, "files", "home")
	source := filepath.Join(home, ".config", "app", "settings")
	contents := []byte("keep these bytes\n")
	mode := os.FileMode(0o640)
	for _, directory := range []string{filepath.Dir(source), managed} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(source, contents, mode); err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareAdoption(source, managed, home, configRoot)
	if err != nil {
		t.Fatal(err)
	}
	return plan, contents, mode
}

func assertAdoptedFile(t *testing.T, source, destination string, contents []byte, mode os.FileMode) {
	t.Helper()
	link, err := os.Readlink(source)
	if err != nil {
		t.Fatalf("Readlink(%s) error = %v", source, err)
	}
	if link != destination {
		t.Errorf("link destination = %q, want %q", link, destination)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(contents) {
		t.Errorf("managed contents = %q, want %q", data, contents)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode.Perm() {
		t.Errorf("managed permissions = %04o, want %04o", info.Mode().Perm(), mode.Perm())
	}
}

func assertRestoredAdoption(t *testing.T, plan AdoptionPlan, contents []byte, mode os.FileMode) {
	t.Helper()
	assertRestoredSource(t, plan.Source, contents, mode)
	if _, err := os.Lstat(plan.Destination); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("managed destination remains after rollback: %v", err)
	}
	if _, err := os.Lstat(plan.linkPlan.layout.LedgerPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("link ledger remains after rollback: %v", err)
	}
	if _, err := os.Lstat(filepath.Dir(plan.Destination)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("managed destination directory remains after rollback: %v", err)
	}
}

func assertRestoredSource(t *testing.T, source string, contents []byte, mode os.FileMode) {
	t.Helper()
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatalf("source was not restored: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("restored source mode = %v, want an ordinary file", info.Mode())
	}
	if info.Mode().Perm() != mode.Perm() {
		t.Errorf("restored permissions = %04o, want %04o", info.Mode().Perm(), mode.Perm())
	}
	data, err := os.ReadFile(source)
	if err != nil || string(data) != string(contents) {
		t.Errorf("restored source = %q, %v", data, err)
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
