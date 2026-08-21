package managedhome

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type journalEntry struct {
	undo   func() error
	commit func() error
}

type linkTransaction struct {
	entries []journalEntry
	hook    func(transactionStep) error
}

type transactionStep string

const (
	transactionStepDestinationStaged transactionStep = "destination staged"
	transactionStepSourceBackedUp    transactionStep = "source backed up"
	transactionStepLinkCreated       transactionStep = "link created"
	transactionStepLedgerSaved       transactionStep = "ledger saved"
)

type ledgerSnapshot struct {
	existed  bool
	data     []byte
	mode     os.FileMode
	identity os.FileInfo
}

type closer interface {
	Close() error
}

func closeAfter(action func() error, resource closer) error {
	return errors.Join(action(), resource.Close())
}

func (transaction *linkTransaction) append(undo, commit func() error) {
	transaction.entries = append(transaction.entries, journalEntry{undo: undo, commit: commit})
}

func (transaction *linkTransaction) after(step transactionStep) error {
	if transaction.hook == nil {
		return nil
	}
	if err := transaction.hook(step); err != nil {
		return fmt.Errorf("after %s: %w", step, err)
	}
	return nil
}

func (transaction *linkTransaction) rollback() error {
	var rollbackErrors []error
	for index := len(transaction.entries) - 1; index >= 0; index-- {
		if undo := transaction.entries[index].undo; undo != nil {
			rollbackErrors = append(rollbackErrors, undo())
		}
	}
	return errors.Join(rollbackErrors...)
}

func (transaction *linkTransaction) commit() error {
	var cleanupErrors []error
	for index := len(transaction.entries) - 1; index >= 0; index-- {
		if commit := transaction.entries[index].commit; commit != nil {
			cleanupErrors = append(cleanupErrors, commit())
		}
	}
	return errors.Join(cleanupErrors...)
}

func (transaction *linkTransaction) makeDirectories(root *os.Root, relative string) error {
	relative = filepath.Clean(relative)
	var missing []string
	for current := relative; current != "."; current = filepath.Dir(current) {
		if _, err := root.Lstat(current); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missing = append(missing, current)
	}
	for index := len(missing) - 1; index >= 0; index-- {
		path := missing[index]
		if err := root.Mkdir(path, 0o755); err != nil {
			return err
		}
		identity, err := root.Lstat(path)
		if err != nil {
			_ = root.Remove(path)
			return err
		}
		handle, err := root.OpenRoot(path)
		if err != nil {
			_ = root.Remove(path)
			return err
		}
		displayPath := filepath.Join(root.Name(), path)
		transaction.append(func() error {
			return closeAfter(func() error {
				return removeMatchingFile(root, path, identity, "roll back directory "+displayPath)
			}, handle)
		}, handle.Close)
	}
	return nil
}

func (transaction *linkTransaction) makeDirectoriesAbsolute(path string) error {
	var missing []string
	for current := path; ; current = filepath.Dir(current) {
		if _, err := os.Lstat(current); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missing = append(missing, current)
		next := filepath.Dir(current)
		if next == current {
			break
		}
	}
	for index := len(missing) - 1; index >= 0; index-- {
		created := missing[index]
		if err := os.Mkdir(created, 0o755); err != nil {
			return err
		}
		identity, err := os.Lstat(created)
		if err != nil {
			_ = os.Remove(created)
			return err
		}
		handle, err := os.Open(created)
		if err != nil {
			_ = os.Remove(created)
			return err
		}
		transaction.append(func() error {
			return closeAfter(func() error {
				return removeMatchingFileAbsolute(created, identity, "roll back directory "+created)
			}, handle)
		}, handle.Close)
	}
	return nil
}

func (transaction *linkTransaction) createDirectory(root *os.Root, relative, display string, mode os.FileMode) error {
	if err := root.Mkdir(relative, mode); err != nil {
		return err
	}
	identity, err := root.Lstat(relative)
	if err != nil {
		_ = root.Remove(relative)
		return err
	}
	handle, err := root.OpenRoot(relative)
	if err != nil {
		_ = root.Remove(relative)
		return err
	}
	remove := func(action string) error {
		return removeMatchingFile(root, relative, identity, action+" "+display)
	}
	transaction.append(
		func() error { return closeAfter(func() error { return remove("roll back directory") }, handle) },
		func() error {
			return closeAfter(func() error { return remove("remove transaction directory") }, handle)
		},
	)
	return nil
}

func (transaction *linkTransaction) createFile(
	root *os.Root,
	relative string,
	display string,
	mode os.FileMode,
) (*os.File, os.FileInfo, error) {
	file, err := root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return nil, nil, err
	}
	identity, err := file.Stat()
	if err != nil {
		_ = file.Close()
		_ = root.Remove(relative)
		return nil, nil, err
	}
	handle, err := root.Open(relative)
	if err != nil {
		_ = file.Close()
		_ = root.Remove(relative)
		return nil, nil, err
	}
	transaction.append(func() error {
		return closeAfter(func() error {
			return removeMatchingFile(root, relative, identity, "roll back file "+display)
		}, handle)
	}, handle.Close)
	return file, identity, nil
}

func (transaction *linkTransaction) createSymlink(
	root *os.Root,
	destination string,
	relative string,
	display string,
) error {
	if err := root.Symlink(destination, relative); err != nil {
		return err
	}
	transaction.append(func() error {
		owned, err := exactRootSymlink(root, relative, destination)
		if err != nil {
			return fmt.Errorf("inspect link before rolling back %s: %w", display, err)
		}
		if !owned {
			return fmt.Errorf("refusing to roll back link %s because it was replaced", display)
		}
		if err := root.Remove(relative); err != nil {
			return fmt.Errorf("roll back link %s: %w", display, err)
		}
		return nil
	}, nil)
	return nil
}

func (transaction *linkTransaction) createSymlinkAbsolute(destination, target string) error {
	if err := os.Symlink(destination, target); err != nil {
		return err
	}
	transaction.append(func() error {
		owned, err := exactSymlink(target, destination)
		if err != nil {
			return fmt.Errorf("inspect link before rolling back %s: %w", target, err)
		}
		if !owned {
			return fmt.Errorf("refusing to roll back link %s because it was replaced", target)
		}
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("roll back link %s: %w", target, err)
		}
		return nil
	}, nil)
	return nil
}

func (transaction *linkTransaction) removeSymlink(
	root *os.Root,
	relative string,
	display string,
	destination string,
) error {
	if err := root.Remove(relative); err != nil {
		return err
	}
	transaction.append(func() error {
		if _, err := root.Lstat(relative); err == nil {
			return fmt.Errorf("refusing to restore stale link %s because the target was replaced", display)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect stale link target %s: %w", display, err)
		}
		if err := root.Symlink(destination, relative); err != nil {
			return fmt.Errorf("restore stale link %s: %w", display, err)
		}
		return nil
	}, nil)
	return nil
}

func (transaction *linkTransaction) removeSymlinkAbsolute(target, destination string) error {
	if err := os.Remove(target); err != nil {
		return err
	}
	transaction.append(func() error {
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("refusing to restore stale link %s because the target was replaced", target)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect stale link target %s: %w", target, err)
		}
		if err := os.Symlink(destination, target); err != nil {
			return fmt.Errorf("restore stale link %s: %w", target, err)
		}
		return nil
	}, nil)
	return nil
}

func (transaction *linkTransaction) backupPath(
	root *os.Root,
	parent string,
	name string,
	display string,
	want os.FileInfo,
	directory bool,
) (string, error) {
	backupDirectory, err := transaction.makeTempDirectory(root, parent, ".xoldot-backup-", filepath.Dir(display))
	if err != nil {
		return "", err
	}
	backup := filepath.Join(backupDirectory, "original")
	backupDisplay := filepath.Join(root.Name(), backup)
	original := filepath.Join(parent, name)
	originalHandle, err := root.Open(original)
	if err != nil {
		return "", err
	}
	if err := root.Rename(original, backup); err != nil {
		_ = originalHandle.Close()
		return "", err
	}
	transaction.append(func() error {
		return closeAfter(func() error {
			if _, err := root.Lstat(original); err == nil {
				return fmt.Errorf(
					"refusing to restore %s because its path was replaced; original remains at %s",
					display,
					backupDisplay,
				)
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect restore target %s: %w", display, err)
			}
			current, err := root.Lstat(backup)
			if err != nil {
				return fmt.Errorf("inspect backup for %s: %w", display, err)
			}
			if !os.SameFile(want, current) {
				return fmt.Errorf("refusing to restore %s because its backup was replaced", display)
			}
			if err := root.Rename(backup, original); err != nil {
				return fmt.Errorf("restore %s: %w", display, err)
			}
			return nil
		}, originalHandle)
	}, func() error {
		return closeAfter(func() error {
			current, err := root.Lstat(backup)
			if err != nil {
				return fmt.Errorf("inspect committed backup for %s: %w", display, err)
			}
			if !os.SameFile(want, current) {
				return fmt.Errorf("refusing to remove backup for %s because it was replaced", display)
			}
			if directory {
				err = root.RemoveAll(backup)
			} else {
				err = root.Remove(backup)
			}
			if err != nil {
				return fmt.Errorf("remove backup for %s: %w", display, err)
			}
			return nil
		}, originalHandle)
	})
	backupIdentity, err := root.Lstat(backup)
	if err != nil {
		return "", err
	}
	if !os.SameFile(want, backupIdentity) {
		return "", fmt.Errorf("%s changed while it was being backed up", display)
	}
	return backup, nil
}

func (transaction *linkTransaction) makeTempDirectory(
	root *os.Root,
	parent string,
	prefix string,
	displayParent string,
) (string, error) {
	for range 100 {
		name, err := randomTransactionName(prefix)
		if err != nil {
			return "", err
		}
		relative := filepath.Join(parent, name)
		err = transaction.createDirectory(root, relative, filepath.Join(displayParent, name), 0o700)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		return relative, nil
	}
	return "", fmt.Errorf("create unique transaction directory in %s", displayParent)
}

func (transaction *linkTransaction) backupLegacySkillDirectory(
	root *os.Root,
	relative string,
	display string,
	previous linkLedger,
) error {
	_, replace, err := legacySkillDirectory(display, previous)
	if err != nil {
		return err
	}
	if !replace {
		return fmt.Errorf("legacy skill directory %s changed while applying links", display)
	}
	identity, err := root.Lstat(relative)
	if err != nil {
		return fmt.Errorf("inspect legacy skill directory %s: %w", display, err)
	}
	_, err = transaction.backupPath(
		root,
		filepath.Dir(relative),
		filepath.Base(relative),
		display,
		identity,
		true,
	)
	return err
}

func (transaction *linkTransaction) backupLegacySkillDirectoryAbsolute(
	target string,
	previous linkLedger,
) error {
	_, replace, err := legacySkillDirectory(target, previous)
	if err != nil {
		return err
	}
	if !replace {
		return fmt.Errorf("legacy skill directory %s changed while applying links", target)
	}
	identity, err := os.Lstat(target)
	if err != nil {
		return fmt.Errorf("inspect legacy skill directory %s: %w", target, err)
	}
	backupDirectory, err := os.MkdirTemp(filepath.Dir(target), ".xoldot-link-backup-")
	if err != nil {
		return fmt.Errorf("create backup for legacy skill directory %s: %w", target, err)
	}
	directoryIdentity, err := os.Lstat(backupDirectory)
	if err != nil {
		_ = os.Remove(backupDirectory)
		return err
	}
	directoryHandle, err := os.Open(backupDirectory)
	if err != nil {
		_ = os.Remove(backupDirectory)
		return err
	}
	transaction.append(
		func() error {
			return closeAfter(func() error {
				return removeMatchingFileAbsolute(
					backupDirectory,
					directoryIdentity,
					"roll back legacy backup directory "+backupDirectory,
				)
			}, directoryHandle)
		},
		func() error {
			return closeAfter(func() error {
				return removeMatchingFileAbsolute(
					backupDirectory,
					directoryIdentity,
					"remove legacy backup directory "+backupDirectory,
				)
			}, directoryHandle)
		},
	)
	backup := filepath.Join(backupDirectory, "skill")
	originalHandle, err := os.Open(target)
	if err != nil {
		return err
	}
	if err := os.Rename(target, backup); err != nil {
		_ = originalHandle.Close()
		return fmt.Errorf("back up legacy skill directory %s: %w", target, err)
	}
	transaction.append(func() error {
		return closeAfter(func() error {
			if _, err := os.Lstat(target); err == nil {
				return fmt.Errorf(
					"refusing to restore %s because its path was replaced; original remains at %s",
					target,
					backup,
				)
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect restore target %s: %w", target, err)
			}
			current, err := os.Lstat(backup)
			if err != nil {
				return fmt.Errorf("inspect backup for %s: %w", target, err)
			}
			if !os.SameFile(identity, current) {
				return fmt.Errorf("refusing to restore %s because its backup was replaced", target)
			}
			if err := os.Rename(backup, target); err != nil {
				return fmt.Errorf("restore %s: %w", target, err)
			}
			return nil
		}, originalHandle)
	}, func() error {
		return closeAfter(func() error {
			current, err := os.Lstat(backup)
			if err != nil {
				return fmt.Errorf("inspect committed backup for %s: %w", target, err)
			}
			if !os.SameFile(identity, current) {
				return fmt.Errorf("refusing to remove backup for %s because it was replaced", target)
			}
			if err := os.RemoveAll(backup); err != nil {
				return fmt.Errorf("remove backup for %s: %w", target, err)
			}
			return nil
		}, originalHandle)
	})
	backupIdentity, err := os.Lstat(backup)
	if err != nil {
		return err
	}
	if !os.SameFile(identity, backupIdentity) {
		return fmt.Errorf("legacy skill directory %s changed while it was being backed up", target)
	}
	return nil
}

func (transaction *linkTransaction) saveLedger(
	root *os.Root,
	layout managedHomeLayout,
	records []linkRecord,
) error {
	snapshot, err := takeLedgerSnapshot(root, layout.LedgerPath)
	if err != nil {
		return err
	}
	data, err := encodeLedger(records)
	if err != nil {
		return err
	}
	ledgerRelative := filepath.FromSlash(ledgerRelativePath)
	parent := filepath.Dir(ledgerRelative)
	tempName, err := randomTransactionName(".xoldot-ledger-")
	if err != nil {
		return err
	}
	tempRelative := filepath.Join(parent, tempName)
	tempDisplay := filepath.Join(filepath.Dir(layout.LedgerPath), tempName)
	temp, tempIdentity, err := transaction.createFile(root, tempRelative, tempDisplay, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary managed link state: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary managed link state: %w", err)
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set permissions on temporary managed link state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync temporary managed link state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary managed link state: %w", err)
	}
	if err := snapshot.verify(root, ledgerRelative, "save managed link state"); err != nil {
		return err
	}

	if snapshot.existed {
		previousHandle, err := root.Open(ledgerRelative)
		if err != nil {
			return fmt.Errorf("open managed link state backup: %w", err)
		}
		backupName, err := randomTransactionName(".xoldot-ledger-backup-")
		if err != nil {
			_ = previousHandle.Close()
			return err
		}
		backupRelative := filepath.Join(parent, backupName)
		if err := root.Rename(ledgerRelative, backupRelative); err != nil {
			_ = previousHandle.Close()
			return fmt.Errorf("back up managed link state: %w", err)
		}
		transaction.append(func() error {
			return closeAfter(func() error {
				if _, err := root.Lstat(ledgerRelative); err == nil {
					return fmt.Errorf("refusing to restore managed link state because it was replaced")
				} else if !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("inspect managed link state restore target: %w", err)
				}
				if err := snapshot.verify(root, backupRelative, "restore managed link state"); err != nil {
					return err
				}
				if err := root.Rename(backupRelative, ledgerRelative); err != nil {
					return fmt.Errorf("restore managed link state: %w", err)
				}
				return nil
			}, previousHandle)
		}, func() error {
			return closeAfter(func() error {
				if err := snapshot.verify(root, backupRelative, "remove managed link state backup"); err != nil {
					return err
				}
				if err := root.Remove(backupRelative); err != nil {
					return fmt.Errorf("remove managed link state backup: %w", err)
				}
				return nil
			}, previousHandle)
		})
		backupIdentity, err := root.Lstat(backupRelative)
		if err != nil {
			return fmt.Errorf("inspect managed link state backup: %w", err)
		}
		if !os.SameFile(snapshot.identity, backupIdentity) {
			return fmt.Errorf("managed link state changed while it was being saved")
		}
	}

	if err := root.Rename(tempRelative, ledgerRelative); err != nil {
		return fmt.Errorf("save managed link state: %w", err)
	}
	writtenSnapshot := ledgerSnapshot{
		existed:  true,
		data:     data,
		mode:     0o600,
		identity: tempIdentity,
	}
	transaction.append(func() error {
		if err := writtenSnapshot.verify(root, ledgerRelative, "roll back managed link state"); err != nil {
			return err
		}
		if err := root.Remove(ledgerRelative); err != nil {
			return fmt.Errorf("roll back managed link state: %w", err)
		}
		return nil
	}, nil)
	if err := writtenSnapshot.verify(root, ledgerRelative, "verify saved managed link state"); err != nil {
		return err
	}
	return nil
}

func takeLedgerSnapshot(root *os.Root, display string) (ledgerSnapshot, error) {
	relative := filepath.FromSlash(ledgerRelativePath)
	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return ledgerSnapshot{}, nil
	}
	if err != nil {
		return ledgerSnapshot{}, fmt.Errorf("inspect managed link state: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ledgerSnapshot{}, fmt.Errorf("managed link state %s is not a regular file", display)
	}
	data, err := root.ReadFile(relative)
	if err != nil {
		return ledgerSnapshot{}, fmt.Errorf("back up managed link state: %w", err)
	}
	return ledgerSnapshot{
		existed:  true,
		data:     data,
		mode:     info.Mode().Perm(),
		identity: info,
	}, nil
}

func (snapshot ledgerSnapshot) verify(root *os.Root, relative, action string) error {
	info, err := root.Lstat(relative)
	if !snapshot.existed {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect managed link state before %s: %w", action, err)
		}
		return fmt.Errorf("refusing to %s because the managed link state was replaced", action)
	}
	if err != nil {
		return fmt.Errorf("refusing to %s because the managed link state cannot be verified: %w", action, err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(snapshot.identity, info) || info.Mode().Perm() != snapshot.mode {
		return fmt.Errorf("refusing to %s because the managed link state was replaced", action)
	}
	data, err := root.ReadFile(relative)
	if err != nil {
		return fmt.Errorf("verify managed link state before %s: %w", action, err)
	}
	if !bytes.Equal(data, snapshot.data) {
		return fmt.Errorf("refusing to %s because the managed link state changed", action)
	}
	return nil
}

func removeMatchingFile(root *os.Root, relative string, want os.FileInfo, action string) error {
	current, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("refusing to %s because the path cannot be verified: %w", action, err)
	}
	if !os.SameFile(want, current) {
		return fmt.Errorf("refusing to %s because the path was replaced", action)
	}
	if err := root.Remove(relative); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}

func removeMatchingFileAbsolute(path string, want os.FileInfo, action string) error {
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	if !os.SameFile(want, current) {
		return fmt.Errorf("refusing to %s because the path was replaced", action)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}

func exactRootSymlink(root *os.Root, relative, destination string) (bool, error) {
	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}
	actual, err := root.Readlink(relative)
	if err != nil {
		return false, err
	}
	return actual == destination, nil
}

func randomTransactionName(prefix string) (string, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate transaction path: %w", err)
	}
	return prefix + hex.EncodeToString(random[:]), nil
}
