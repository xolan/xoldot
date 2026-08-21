package managedhome

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/xolan/xoldot/internal/machinestate"
	"github.com/xolan/xoldot/internal/pathutil"
)

const (
	ledgerRelativePath  = machinestate.LinkLedgerRelativePath
	lockRelativePath    = machinestate.LinkLockRelativePath
	backupsRelativePath = machinestate.BackupsRelativePath
)

type managedHomeLayout struct {
	ManagedRoot string
	Home        string
	ConfigRoot  string
	LedgerPath  string
	LockPath    string

	homeIdentity    os.FileInfo
	managedIdentity os.FileInfo
}

type lockedHome struct {
	root *os.Root
	lock *os.File
}

func newManagedHomeLayout(managedRoot, home, configRoot string) (managedHomeLayout, error) {
	managedRoot, err := resolveRoot(managedRoot, "managed home")
	if err != nil {
		return managedHomeLayout{}, err
	}
	home, err = resolveRoot(home, "target home")
	if err != nil {
		return managedHomeLayout{}, err
	}
	configRoot, err = resolveRoot(configRoot, "config root")
	if err != nil {
		return managedHomeLayout{}, err
	}

	layout := managedHomeLayout{
		ManagedRoot: managedRoot,
		Home:        home,
		ConfigRoot:  configRoot,
		LedgerPath:  filepath.Join(home, filepath.FromSlash(ledgerRelativePath)),
		LockPath:    filepath.Join(home, filepath.FromSlash(lockRelativePath)),
	}
	if layout.homeIdentity, err = directoryIdentity(home, "target home"); err != nil {
		return managedHomeLayout{}, err
	}
	if layout.managedIdentity, err = directoryIdentity(managedRoot, "managed home"); err != nil {
		return managedHomeLayout{}, err
	}
	for _, statePath := range []string{layout.LedgerPath, layout.LockPath} {
		resolved, resolveErr := pathutil.ResolveExistingPrefix(statePath)
		if resolveErr != nil {
			return managedHomeLayout{}, fmt.Errorf("resolve managed link state path %s: %w", statePath, resolveErr)
		}
		if !pathutil.Contains(home, resolved) {
			return managedHomeLayout{}, fmt.Errorf("managed link state path %s resolves outside the target home %s", statePath, home)
		}
	}
	return layout, nil
}

func directoryIdentity(path, description string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect %s %s: %w", description, path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s %s is not a directory", description, path)
	}
	return info, nil
}

func (layout managedHomeLayout) openHomeRoot() (*os.Root, error) {
	if layout.homeIdentity == nil {
		if err := os.MkdirAll(layout.Home, 0o755); err != nil {
			return nil, fmt.Errorf("create target home %s: %w", layout.Home, err)
		}
		root, err := os.OpenRoot(layout.Home)
		if err != nil {
			return nil, fmt.Errorf("open target home %s: %w", layout.Home, err)
		}
		return root, nil
	}
	return openPreparedRoot(layout.Home, "target home", layout.homeIdentity)
}

func (layout managedHomeLayout) openManagedRoot() (*os.Root, error) {
	if layout.managedIdentity == nil {
		return nil, fmt.Errorf("managed home %s does not exist; run 'xoldot setup' before adopting files", layout.ManagedRoot)
	}
	return openPreparedRoot(layout.ManagedRoot, "managed home", layout.managedIdentity)
}

func (layout managedHomeLayout) openLockedHome(prepared linkLedger) (*lockedHome, error) {
	root, err := layout.openHomeRoot()
	if err != nil {
		return nil, err
	}
	lock, err := layout.acquireLedgerLock(root)
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	locked := &lockedHome{root: root, lock: lock}
	if err := layout.revalidatePreparedLedger(root, prepared); err != nil {
		return nil, errors.Join(err, locked.close())
	}
	return locked, nil
}

func (home *lockedHome) close() error {
	return errors.Join(releaseLedgerLock(home.lock), home.root.Close())
}

func openPreparedRoot(path, description string, prepared os.FileInfo) (*os.Root, error) {
	if prepared == nil {
		return nil, fmt.Errorf("%s %s does not exist", description, path)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open %s %s: %w", description, path, err)
	}
	current, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("inspect open %s %s: %w", description, path, err)
	}
	if !os.SameFile(prepared, current) {
		_ = root.Close()
		return nil, fmt.Errorf("%s %s changed after preparation", description, path)
	}
	return root, nil
}

func (layout managedHomeLayout) homeRelative(path string) (string, error) {
	return relativeWithin(layout.Home, path, "target home")
}

func (layout managedHomeLayout) managedRelative(path string) (string, error) {
	return relativeWithin(layout.ManagedRoot, path, "managed home")
}

func relativeWithin(root, path, description string) (string, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || !pathutil.Contains(root, path) {
		if err != nil {
			return "", fmt.Errorf("map %s path %s: %w", description, path, err)
		}
		return "", fmt.Errorf("path %s is outside the %s %s", path, description, root)
	}
	return relative, nil
}

func (layout managedHomeLayout) reservedTarget(path string) bool {
	return machinestate.IsReserved(layout.Home, path)
}

func (layout managedHomeLayout) loadLedger() (linkLedger, error) {
	return loadLedger(layout.LedgerPath)
}

func (layout managedHomeLayout) loadLedgerFrom(root *os.Root) (linkLedger, error) {
	data, err := root.ReadFile(filepath.FromSlash(ledgerRelativePath))
	if errors.Is(err, os.ErrNotExist) {
		return linkLedger{Version: 1}, nil
	}
	if err != nil {
		return linkLedger{}, fmt.Errorf("read managed link state: %w", err)
	}
	return decodeLedger(layout.LedgerPath, data)
}

func (layout managedHomeLayout) validateLedger(ledger linkLedger) error {
	if err := validateLedger(ledger, layout.Home, layout.ManagedRoot); err != nil {
		return fmt.Errorf("validate managed link state %s: %w", layout.LedgerPath, err)
	}
	return nil
}

func (layout managedHomeLayout) revalidatePreparedLedger(root *os.Root, prepared linkLedger) error {
	current, err := layout.loadLedgerFrom(root)
	if err != nil {
		return err
	}
	if err := layout.validateLedger(current); err != nil {
		return err
	}
	if current.Version != prepared.Version || !slices.Equal(current.Links, prepared.Links) {
		return fmt.Errorf("managed link state %s changed after preparation; retry the command", layout.LedgerPath)
	}
	return nil
}

func (layout managedHomeLayout) recordsWith(record linkRecord, previous linkLedger) []linkRecord {
	records := make([]linkRecord, 0, len(previous.Links)+1)
	for _, existing := range previous.Links {
		if existing.Target != record.Target {
			records = append(records, existing)
		}
	}
	records = append(records, record)
	sort.Slice(records, func(left, right int) bool {
		return records[left].Target < records[right].Target
	})
	return records
}

func (layout managedHomeLayout) acquireLedgerLock(root *os.Root) (*os.File, error) {
	lock, err := machinestate.AcquireRootedLock(root, lockRelativePath)
	if err != nil {
		return nil, fmt.Errorf("acquire managed link state lock %s: %w", layout.LockPath, err)
	}
	return lock, nil
}

func releaseLedgerLock(lock *os.File) error {
	if err := machinestate.ReleaseRootedLock(lock); err != nil {
		return fmt.Errorf("release managed link state lock: %w", err)
	}
	return nil
}

func resolveRoot(root, description string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", description, err)
	}
	root, err = pathutil.ResolveExistingPrefix(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s symlinks: %w", description, err)
	}
	return root, nil
}

func loadLedger(path string) (linkLedger, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return linkLedger{Version: 1}, nil
	}
	if err != nil {
		return linkLedger{}, fmt.Errorf("read managed link state: %w", err)
	}
	return decodeLedger(path, data)
}

func decodeLedger(path string, data []byte) (linkLedger, error) {
	var ledger linkLedger
	if err := json.Unmarshal(data, &ledger); err != nil {
		return linkLedger{}, fmt.Errorf("parse managed link state %s: %w", path, err)
	}
	if ledger.Version != 1 {
		return linkLedger{}, fmt.Errorf("unsupported managed link state version %d", ledger.Version)
	}
	return ledger, nil
}

func validateLedger(ledger linkLedger, home, managedRoot string) error {
	seen := make(map[string]struct{}, len(ledger.Links))
	for _, record := range ledger.Links {
		if !filepath.IsAbs(record.Target) || record.Target == home || !pathutil.Contains(home, record.Target) {
			return fmt.Errorf("recorded target %q is outside the target home", record.Target)
		}
		if record.Destination == "" {
			return fmt.Errorf("recorded target %q has an empty destination", record.Target)
		}
		if filepath.IsAbs(record.Destination) && !pathutil.Contains(managedRoot, record.Destination) {
			return fmt.Errorf("recorded destination %q is outside the managed home", record.Destination)
		}
		if _, exists := seen[record.Target]; exists {
			return fmt.Errorf("recorded target %q is duplicated", record.Target)
		}
		seen[record.Target] = struct{}{}
	}
	return nil
}

func encodeLedger(records []linkRecord) ([]byte, error) {
	data, err := json.MarshalIndent(linkLedger{Version: 1, Links: records}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode managed link state: %w", err)
	}
	return append(data, '\n'), nil
}
