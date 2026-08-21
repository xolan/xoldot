package managedhome

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	reportstatus "github.com/xolan/xoldot/internal/status"
)

type RestorePlan struct {
	layout   managedHomeLayout
	previous linkLedger
	manifest backupManifest
	records  []linkRecord
}

type RestoreResult struct {
	Restored int
}

func PrepareRestore(id, managedRoot, home, configRoot string) (RestorePlan, error) {
	if err := validateBackupID(id); err != nil {
		return RestorePlan{}, err
	}
	layout, err := newManagedHomeLayout(managedRoot, home, configRoot)
	if err != nil {
		return RestorePlan{}, err
	}
	if layout.homeIdentity == nil {
		return RestorePlan{}, fmt.Errorf("unknown backup ID %q", id)
	}
	previous, err := layout.loadLedger()
	if err != nil {
		return RestorePlan{}, err
	}
	if err := layout.validateLedger(previous); err != nil {
		return RestorePlan{}, err
	}
	root, err := layout.openHomeRoot()
	if err != nil {
		return RestorePlan{}, err
	}
	if _, err := root.Lstat(backupDirectoryRelative(id)); errors.Is(err, os.ErrNotExist) {
		return RestorePlan{}, errors.Join(fmt.Errorf("unknown backup ID %q", id), root.Close())
	} else if err != nil {
		return RestorePlan{}, errors.Join(fmt.Errorf("inspect backup %q: %w", id, err), root.Close())
	}
	manifest, _, err := loadBackupManifest(root, layout, id)
	if err != nil {
		return RestorePlan{}, errors.Join(fmt.Errorf("load backup %q: %w", id, err), root.Close())
	}
	if err := validateRestoreTargets(root, layout, previous, manifest); err != nil {
		return RestorePlan{}, errors.Join(err, root.Close())
	}
	restored := make(map[string]struct{}, len(manifest.Entries))
	for _, record := range manifest.Entries {
		restored[record.Original] = struct{}{}
	}
	records := make([]linkRecord, 0, len(previous.Links)-len(restored))
	for _, record := range previous.Links {
		if _, exists := restored[record.Target]; !exists {
			records = append(records, record)
		}
	}
	plan := RestorePlan{
		layout:   layout,
		previous: previous,
		manifest: manifest,
		records:  records,
	}
	return plan, root.Close()
}

func (plan RestorePlan) Apply(reporter reportstatus.Reporter, dry bool) (RestoreResult, error) {
	return plan.apply(reporter, dry, nil)
}

func (plan RestorePlan) apply(
	reporter reportstatus.Reporter,
	dry bool,
	hook func(transactionStep) error,
) (RestoreResult, error) {
	result := RestoreResult{Restored: len(plan.manifest.Entries)}
	if dry {
		for _, record := range plan.manifest.Entries {
			if err := reportf(reporter, "Would restore %s from backup %s", record.Original, plan.manifest.ID); err != nil {
				return RestoreResult{}, err
			}
		}
		return result, nil
	}
	home, err := plan.layout.openLockedHome(plan.previous)
	if err != nil {
		return RestoreResult{}, err
	}
	manifest, identities, err := loadBackupManifest(home.root, plan.layout, plan.manifest.ID)
	if err != nil {
		return RestoreResult{}, errors.Join(
			fmt.Errorf("revalidate backup %q: %w", plan.manifest.ID, err),
			home.close(),
		)
	}
	if !slices.Equal(manifest.Entries, plan.manifest.Entries) {
		return RestoreResult{}, errors.Join(
			fmt.Errorf("backup %q changed after preparation; retry the command", plan.manifest.ID),
			home.close(),
		)
	}
	if err := validateRestoreTargets(home.root, plan.layout, plan.previous, manifest); err != nil {
		return RestoreResult{}, errors.Join(err, home.close())
	}

	transaction := linkTransaction{hook: hook}
	for index, record := range manifest.Entries {
		original, err := plan.layout.homeRelative(record.Original)
		if err != nil {
			return RestoreResult{}, plan.restoreFailure(err, &transaction, home)
		}
		stored, err := plan.layout.homeRelative(record.Stored)
		if err != nil {
			return RestoreResult{}, plan.restoreFailure(err, &transaction, home)
		}
		if err := transaction.removeSymlink(home.root, original, record.Original, record.Destination); err != nil {
			return RestoreResult{}, plan.restoreFailure(fmt.Errorf("remove managed link %s: %w", record.Original, err), &transaction, home)
		}
		if err := transaction.restoreBackupPath(home.root, stored, original, record, identities[index]); err != nil {
			return RestoreResult{}, plan.restoreFailure(err, &transaction, home)
		}
		if err := transaction.after(transactionStepBackupRestored); err != nil {
			return RestoreResult{}, plan.restoreFailure(err, &transaction, home)
		}
		if err := reportf(reporter, "Restored %s", record.Original); err != nil {
			return RestoreResult{}, plan.restoreFailure(err, &transaction, home)
		}
	}
	if err := transaction.saveLedger(home.root, plan.layout, plan.records); err != nil {
		return RestoreResult{}, plan.restoreFailure(err, &transaction, home)
	}
	if err := transaction.after(transactionStepLedgerSaved); err != nil {
		return RestoreResult{}, plan.restoreFailure(err, &transaction, home)
	}
	if err := transaction.stageBackupRemoval(home.root, plan.layout, plan.manifest.ID); err != nil {
		return RestoreResult{}, plan.restoreFailure(err, &transaction, home)
	}
	if err := transaction.after(transactionStepBackupRemovalStaged); err != nil {
		return RestoreResult{}, plan.restoreFailure(err, &transaction, home)
	}
	return result, errors.Join(transaction.commit(), home.close())
}

func (plan RestorePlan) restoreFailure(err error, transaction *linkTransaction, home *lockedHome) error {
	return errors.Join(err, transaction.rollback(), home.close())
}

func validateRestoreTargets(
	root *os.Root,
	layout managedHomeLayout,
	ledger linkLedger,
	manifest backupManifest,
) error {
	owned := make(map[linkRecord]struct{}, len(ledger.Links))
	for _, record := range ledger.Links {
		owned[record] = struct{}{}
	}
	for _, record := range manifest.Entries {
		link := linkRecord{Target: record.Original, Destination: record.Destination}
		if _, exists := owned[link]; !exists {
			return fmt.Errorf("refusing to restore backup %q because %s is not an xoldot-owned link from that run", manifest.ID, record.Original)
		}
		original, err := layout.homeRelative(record.Original)
		if err != nil {
			return err
		}
		exact, err := exactRootSymlink(root, original, record.Destination)
		if err != nil {
			return fmt.Errorf("inspect restore target %s: %w", record.Original, err)
		}
		if !exact {
			return fmt.Errorf("refusing to restore backup %q because target %s changed", manifest.ID, record.Original)
		}
	}
	return nil
}

func (transaction *linkTransaction) restoreBackupPath(
	root *os.Root,
	stored string,
	original string,
	record backupRecord,
	want os.FileInfo,
) error {
	if err := root.Rename(stored, original); err != nil {
		return fmt.Errorf("restore %s: %w", record.Original, err)
	}
	transaction.append(func() error {
		current, err := root.Lstat(original)
		if err != nil {
			return fmt.Errorf("inspect restored path %s: %w", record.Original, err)
		}
		if !os.SameFile(want, current) {
			return fmt.Errorf("refusing to roll back restore of %s because it was replaced", record.Original)
		}
		if _, err := root.Lstat(stored); err == nil {
			return fmt.Errorf("refusing to roll back restore of %s because its backup path was replaced", record.Original)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect backup rollback target %s: %w", record.Stored, err)
		}
		if err := root.Rename(original, stored); err != nil {
			return fmt.Errorf("roll back restore of %s: %w", record.Original, err)
		}
		return nil
	}, nil)
	current, err := root.Lstat(original)
	if err != nil {
		return fmt.Errorf("inspect restored path %s: %w", record.Original, err)
	}
	if !os.SameFile(want, current) {
		return fmt.Errorf("stored backup %s changed while it was restored", record.Stored)
	}
	return nil
}

func (transaction *linkTransaction) stageBackupRemoval(root *os.Root, layout managedHomeLayout, id string) error {
	directory := backupDirectoryRelative(id)
	identity, err := root.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect restored backup directory %s: %w", backupDirectory(layout, id), err)
	}
	_, err = transaction.backupPath(
		root,
		filepath.Dir(directory),
		filepath.Base(directory),
		backupDirectory(layout, id),
		identity,
		true,
	)
	return err
}
