package managedhome

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type backupFixture struct {
	configRoot string
	managed    string
	home       string
}

func newBackupFixture(t *testing.T) backupFixture {
	t.Helper()
	root := t.TempDir()
	fixture := backupFixture{
		configRoot: filepath.Join(root, "configuration"),
		home:       filepath.Join(root, "home"),
	}
	fixture.managed = filepath.Join(fixture.configRoot, "files", "home")
	for _, directory := range []string{fixture.managed, fixture.home} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (fixture backupFixture) conflict(t *testing.T, relative, managed, local string, mode os.FileMode) (string, string) {
	t.Helper()
	source := filepath.Join(fixture.managed, relative)
	target := filepath.Join(fixture.home, relative)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(managed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(local), mode); err != nil {
		t.Fatal(err)
	}
	return source, target
}

func TestApplyWithoutBackupKeepsExactConflictRefusal(t *testing.T) {
	fixture := newBackupFixture(t)
	_, target := fixture.conflict(t, ".config/app", "managed", "local", 0o644)

	_, err := Prepare(fixture.managed, fixture.home, fixture.configRoot)
	want := fmt.Sprintf("target %s already exists and is not managed by xoldot", target)
	if err == nil || err.Error() != want {
		t.Fatalf("Prepare() error = %v, want %q", err, want)
	}
}

func TestBackupDryRunReportsEligibleAndRefusesDirectoriesWithoutWriting(t *testing.T) {
	fixture := newBackupFixture(t)
	_, eligible := fixture.conflict(t, ".eligible", "managed", "local", 0o640)
	var directoryTargets []string
	for _, name := range []string{".first-directory", ".second-directory"} {
		directorySource := filepath.Join(fixture.managed, name)
		if err := os.WriteFile(directorySource, []byte("managed"), 0o644); err != nil {
			t.Fatal(err)
		}
		directoryTarget := filepath.Join(fixture.home, name)
		if err := os.Mkdir(directoryTarget, 0o755); err != nil {
			t.Fatal(err)
		}
		directoryTargets = append(directoryTargets, directoryTarget)
	}
	before := snapshotTree(t, fixture.home)

	_, err := PrepareBackup(fixture.managed, fixture.home, fixture.configRoot)
	var refusal *PreparationRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("PrepareBackup() error = %T %v, want *PreparationRefusal", err, err)
	}
	var output bytes.Buffer
	if err := refusal.Preview(writerReporter(&output)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Would back up "+eligible+" before linking") {
		t.Errorf("dry output = %q, want eligible backup", output.String())
	}
	for _, target := range directoryTargets {
		if !strings.Contains(output.String(), "Conflict at "+target) {
			t.Errorf("dry output = %q, want unsupported conflict %s", output.String(), target)
		}
	}
	if after := snapshotTree(t, fixture.home); after != before {
		t.Fatalf("preparation preview changed Target home\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestBackupApplyRollsBackEveryConflictOnFailures(t *testing.T) {
	steps := []transactionStep{
		transactionStepConflictBackedUp,
		transactionStepLinkCreated,
		transactionStepLedgerSaved,
		transactionStepBackupManifestSaved,
	}
	for _, step := range steps {
		t.Run(string(step), func(t *testing.T) {
			fixture := newBackupFixture(t)
			_, first := fixture.conflict(t, ".first", "managed first", "local first", 0o640)
			_, second := fixture.conflict(t, ".second", "managed second", "local second", 0o600)
			plan, err := PrepareBackup(fixture.managed, fixture.home, fixture.configRoot)
			if err != nil {
				t.Fatal(err)
			}
			err = applyBackupWithHook(plan, func(current transactionStep) error {
				if current == step {
					return fmt.Errorf("simulated %s failure", step)
				}
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "simulated") {
				t.Fatalf("Apply() error = %v, want injected failure", err)
			}
			for path, want := range map[string]string{first: "local first", second: "local second"} {
				data, readErr := os.ReadFile(path)
				if readErr != nil || string(data) != want {
					t.Errorf("restored %s = %q, %v, want %q", path, data, readErr, want)
				}
			}
			backups := filepath.Join(fixture.home, filepath.FromSlash(backupsRelativePath))
			entries, readErr := os.ReadDir(backups)
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Errorf("rollback left backup entries: %v", entries)
			}
			ledger, loadErr := loadLedger(filepath.Join(fixture.home, filepath.FromSlash(ledgerRelativePath)))
			if loadErr != nil || len(ledger.Links) != 0 {
				t.Errorf("rollback ledger = %+v, %v, want empty", ledger, loadErr)
			}
		})
	}
}

func applyBackupWithHook(plan Plan, hook func(transactionStep) error) error {
	home, err := plan.layout.openLockedHome(plan.previous)
	if err != nil {
		return err
	}
	transaction := linkTransaction{hook: hook}
	_, err = plan.apply(discardReporter, false, &transaction, home.root)
	if err != nil {
		return errors.Join(err, transaction.rollback(), home.close())
	}
	return errors.Join(transaction.commit(), home.close())
}

func TestBackupManifestAndRestorePreserveFilesAndSymlinks(t *testing.T) {
	fixture := newBackupFixture(t)
	fileSource, fileTarget := fixture.conflict(t, ".config/app", "managed", "local", 0o640)
	symlinkSource := filepath.Join(fixture.managed, ".link")
	if err := os.WriteFile(symlinkSource, []byte("managed link replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkTarget := filepath.Join(fixture.home, ".link")
	if err := os.Symlink("local-destination", symlinkTarget); err != nil {
		t.Fatal(err)
	}

	plan, err := PrepareBackup(fixture.managed, fixture.home, fixture.configRoot)
	if err != nil {
		t.Fatal(err)
	}
	result, err := plan.Apply(discardReporter, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBackupID(result.BackupID); err != nil {
		t.Fatalf("backup ID = %q: %v", result.BackupID, err)
	}
	for target, destination := range map[string]string{fileTarget: fileSource, symlinkTarget: symlinkSource} {
		if got, readErr := os.Readlink(target); readErr != nil || got != destination {
			t.Errorf("managed link %s = %q, %v, want %q", target, got, readErr, destination)
		}
	}

	manifestPath := filepath.Join(backupDirectory(plan.layout, result.BackupID), backupManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest backupManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.Complete || len(manifest.Entries) != 2 {
		t.Fatalf("manifest = %+v, want two complete entries", manifest)
	}
	for _, record := range manifest.Entries {
		if record.Original == "" || record.Stored == "" || record.Type == "" || record.Digest == "" || record.Destination == "" {
			t.Errorf("incomplete manifest record: %+v", record)
		}
	}
	inspections, err := InspectBackups(fixture.managed, fixture.home, fixture.configRoot)
	if err != nil || len(inspections) != 1 || inspections[0].State != BackupReady {
		t.Fatalf("backup inspections = %+v, %v, want one ready", inspections, err)
	}

	restore, err := PrepareRestore(result.BackupID, fixture.managed, fixture.home, fixture.configRoot)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, fixture.home)
	if _, err := restore.Apply(discardReporter, true); err != nil {
		t.Fatal(err)
	}
	if after := snapshotTree(t, fixture.home); after != before {
		t.Fatal("dry restore changed Target home")
	}
	if _, err := restore.Apply(discardReporter, false); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(fileTarget)
	if err != nil || string(data) != "local" {
		t.Errorf("restored file = %q, %v", data, err)
	}
	if info, statErr := os.Lstat(fileTarget); statErr != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("restored file mode = %v, %v, want 0640", info, statErr)
	}
	if destination, readErr := os.Readlink(symlinkTarget); readErr != nil || destination != "local-destination" {
		t.Errorf("restored symlink = %q, %v", destination, readErr)
	}
	if _, err := os.Lstat(backupDirectory(plan.layout, result.BackupID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("restored backup directory remains: %v", err)
	}
	backupEntries, err := os.ReadDir(filepath.Join(fixture.home, filepath.FromSlash(backupsRelativePath)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(backupEntries) != 0 {
		t.Errorf("restore left transaction directories: %v", backupEntries)
	}
	ledger, err := loadLedger(plan.layout.LedgerPath)
	if err != nil || len(ledger.Links) != 0 {
		t.Errorf("restored ledger = %+v, %v, want no owned links", ledger, err)
	}
}

func TestRestoreRollsBackEveryPathOnFailures(t *testing.T) {
	steps := []transactionStep{
		transactionStepBackupRestored,
		transactionStepLedgerSaved,
		transactionStepBackupRemovalStaged,
	}
	for _, step := range steps {
		t.Run(string(step), func(t *testing.T) {
			fixture := newBackupFixture(t)
			_, first := fixture.conflict(t, ".first", "managed first", "local first", 0o640)
			_, second := fixture.conflict(t, ".second", "managed second", "local second", 0o600)
			backup, err := PrepareBackup(fixture.managed, fixture.home, fixture.configRoot)
			if err != nil {
				t.Fatal(err)
			}
			result, err := backup.Apply(discardReporter, false)
			if err != nil {
				t.Fatal(err)
			}
			restore, err := PrepareRestore(result.BackupID, fixture.managed, fixture.home, fixture.configRoot)
			if err != nil {
				t.Fatal(err)
			}
			_, err = restore.apply(discardReporter, false, func(current transactionStep) error {
				if current == step {
					return fmt.Errorf("simulated %s failure", step)
				}
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "simulated") {
				t.Fatalf("restore error = %v, want injected failure", err)
			}
			for _, target := range []string{first, second} {
				if destination, readErr := os.Readlink(target); readErr != nil || destination == "" {
					t.Errorf("rollback did not restore managed link %s: %q, %v", target, destination, readErr)
				}
			}
			inspections, inspectErr := InspectBackups(fixture.managed, fixture.home, fixture.configRoot)
			if inspectErr != nil || len(inspections) != 1 || inspections[0].State != BackupReady {
				t.Errorf("rollback backup inspections = %+v, %v, want one ready", inspections, inspectErr)
			}
		})
	}
}

func TestRestoreRefusesChangedTargetAndStoredBackupWithoutPartialChanges(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(t *testing.T, manifest backupManifest)
		want   string
	}{
		{
			name: "target",
			change: func(t *testing.T, manifest backupManifest) {
				t.Helper()
				if err := os.Remove(manifest.Entries[0].Original); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(manifest.Entries[0].Original, []byte("replacement"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "target",
		},
		{
			name: "stored backup",
			change: func(t *testing.T, manifest backupManifest) {
				t.Helper()
				if err := os.WriteFile(manifest.Entries[0].Stored, []byte("changed backup"), 0o640); err != nil {
					t.Fatal(err)
				}
			},
			want: "stored backup",
		},
		{
			name: "directory target",
			change: func(t *testing.T, manifest backupManifest) {
				t.Helper()
				if err := os.Remove(manifest.Entries[0].Original); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(manifest.Entries[0].Original, 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: "target",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBackupFixture(t)
			_, first := fixture.conflict(t, ".first", "managed first", "local first", 0o640)
			_, second := fixture.conflict(t, ".second", "managed second", "local second", 0o600)
			plan, err := PrepareBackup(fixture.managed, fixture.home, fixture.configRoot)
			if err != nil {
				t.Fatal(err)
			}
			result, err := plan.Apply(discardReporter, false)
			if err != nil {
				t.Fatal(err)
			}
			root, err := plan.layout.openHomeRoot()
			if err != nil {
				t.Fatal(err)
			}
			manifest, _, err := loadBackupManifest(root, plan.layout, result.BackupID)
			_ = root.Close()
			if err != nil {
				t.Fatal(err)
			}
			test.change(t, manifest)
			beforeSecond, err := os.Readlink(second)
			if err != nil {
				t.Fatal(err)
			}
			_, err = PrepareRestore(result.BackupID, fixture.managed, fixture.home, fixture.configRoot)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("PrepareRestore() error = %v, want %q refusal", err, test.want)
			}
			if afterSecond, readErr := os.Readlink(second); readErr != nil || afterSecond != beforeSecond {
				t.Errorf("refusal changed second target = %q, %v", afterSecond, readErr)
			}
			if test.name == "stored backup" {
				if destination, readErr := os.Readlink(first); readErr != nil || destination == "" {
					t.Errorf("refusal changed first target = %q, %v", destination, readErr)
				}
			}
		})
	}
}

func TestRestoreRefusesUnknownIDsAndManifestEscapes(t *testing.T) {
	fixture := newBackupFixture(t)
	if _, err := PrepareRestore("0123456789abcdef01234567", fixture.managed, fixture.home, fixture.configRoot); err == nil || !strings.Contains(err.Error(), "unknown backup ID") {
		t.Fatalf("unknown PrepareRestore() error = %v", err)
	}
	_, _ = fixture.conflict(t, ".file", "managed", "local", 0o644)
	plan, err := PrepareBackup(fixture.managed, fixture.home, fixture.configRoot)
	if err != nil {
		t.Fatal(err)
	}
	result, err := plan.Apply(discardReporter, false)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(backupDirectory(plan.layout, result.BackupID), backupManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest backupManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(filepath.Dir(fixture.home), "escape")
	manifest.Entries[0].Original = escape
	data, err = encodeBackupManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareRestore(result.BackupID, fixture.managed, fixture.home, fixture.configRoot); err == nil || !strings.Contains(err.Error(), "outside the Target home") {
		t.Fatalf("escape PrepareRestore() error = %v", err)
	}
	if _, err := os.Lstat(escape); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("escape path changed: %v", err)
	}
}

func TestRestoreRefusesTargetParentSymlinkEscape(t *testing.T) {
	fixture := newBackupFixture(t)
	source, target := fixture.conflict(t, filepath.Join(".config", "app"), "managed", "local", 0o644)
	backup, err := PrepareBackup(fixture.managed, fixture.home, fixture.configRoot)
	if err != nil {
		t.Fatal(err)
	}
	result, err := backup.Apply(discardReporter, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(target)
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(escape, 0o755); err != nil {
		t.Fatal(err)
	}
	escapedTarget := filepath.Join(escape, filepath.Base(target))
	if err := os.Symlink(source, escapedTarget); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escape, parent); err != nil {
		t.Fatal(err)
	}

	_, err = PrepareRestore(result.BackupID, fixture.managed, fixture.home, fixture.configRoot)
	if err == nil || !strings.Contains(err.Error(), "inspect restore target") {
		t.Fatalf("PrepareRestore() error = %v, want parent escape refusal", err)
	}
	if destination, err := os.Readlink(escapedTarget); err != nil || destination != source {
		t.Errorf("escape target changed = %q, %v", destination, err)
	}
}

func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var snapshot strings.Builder
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
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
		if info.Mode()&os.ModeSymlink != 0 {
			destination, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&snapshot, "-> %s\n", destination)
		} else if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&snapshot, "%q\n", data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.String()
}
