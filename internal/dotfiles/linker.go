package dotfiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/pathutil"
)

type Result struct {
	Created int
	Current int
	Removed int
}

type linkLedger struct {
	Version int          `json:"version"`
	Links   []linkRecord `json:"links"`
}

type linkRecord struct {
	Target      string `json:"target"`
	Destination string `json:"destination"`
}

func Link(managedRoot, home, configRoot string, logOutput io.Writer, dry bool) (Result, error) {
	managedRoot, err := filepath.Abs(managedRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve managed home: %w", err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return Result{}, fmt.Errorf("resolve target home: %w", err)
	}
	configRoot, err = filepath.Abs(configRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve config root: %w", err)
	}

	// ponytail: these MkdirAll calls run even in dry mode; creating empty
	// roots is setup the walk below needs, not the mutation being previewed.
	if err := os.MkdirAll(managedRoot, 0o755); err != nil {
		return Result{}, fmt.Errorf("create managed home: %w", err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return Result{}, fmt.Errorf("create target home: %w", err)
	}
	managedRoot, err = filepath.EvalSymlinks(managedRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve managed home symlinks: %w", err)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return Result{}, fmt.Errorf("resolve target home symlinks: %w", err)
	}
	configRoot, err = filepath.EvalSymlinks(configRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve config root symlinks: %w", err)
	}

	ledgerPath := filepath.Join(home, ".local", "state", "xoldot", "links.json")
	var plans []linkRecord
	var records []linkRecord
	var current int
	err = filepath.WalkDir(managedRoot, func(source string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		isSymlink := entry.Type()&os.ModeSymlink != 0
		if isSymlink {
			destination, err := os.Stat(source)
			if err != nil {
				return fmt.Errorf("inspect managed symlink %s: %w", source, err)
			}
			if destination.IsDir() {
				return fmt.Errorf("managed directory symlink %s is not supported; track its files individually", source)
			}
		}

		relative, err := filepath.Rel(managedRoot, source)
		if err != nil {
			return fmt.Errorf("find path for %s: %w", source, err)
		}
		target := filepath.Join(home, relative)
		if target == ledgerPath {
			return fmt.Errorf("managed path %s is reserved for xoldot link state", source)
		}
		destination := source
		if isSymlink {
			destination, err = mappedSymlinkDestination(source, target, managedRoot, home, configRoot)
			if err != nil {
				return err
			}
		}
		resolvedParent, err := pathutil.ResolveExistingPrefix(filepath.Dir(target))
		if err != nil {
			return fmt.Errorf("resolve target directory for %s: %w", target, err)
		}
		resolvedTarget := filepath.Join(resolvedParent, filepath.Base(target))
		if pathutil.Contains(configRoot, resolvedTarget) || pathutil.Contains(resolvedTarget, managedRoot) {
			return fmt.Errorf("refusing recursive link %s -> %s", target, source)
		}
		state, err := linkState(target, destination)
		if err != nil {
			return err
		}
		switch state {
		case linkConflict:
			return fmt.Errorf("target %s already exists and is not managed by xoldot", target)
		case linkCurrent:
			current++
		default:
			plans = append(plans, linkRecord{Target: target, Destination: destination})
		}
		records = append(records, linkRecord{Target: target, Destination: destination})
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("plan managed home links: %w", err)
	}

	previous, err := loadLedger(ledgerPath)
	if err != nil {
		return Result{}, err
	}
	if err := validateLedger(previous, home, managedRoot); err != nil {
		return Result{}, fmt.Errorf("validate managed link state %s: %w", ledgerPath, err)
	}
	currentTargets := make(map[string]struct{}, len(records))
	for _, record := range records {
		currentTargets[record.Target] = struct{}{}
	}
	var stale []linkRecord
	for _, record := range previous.Links {
		if _, exists := currentTargets[record.Target]; exists {
			continue
		}
		owned, err := exactSymlink(record.Target, record.Destination)
		if err != nil {
			return Result{}, err
		}
		if owned {
			stale = append(stale, record)
		}
	}

	result := Result{Current: current}
	for _, plan := range plans {
		if dry {
			if err := logf(logOutput, "dotfiles: would link %s -> %s\n", plan.Target, plan.Destination); err != nil {
				return result, err
			}
			result.Created++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(plan.Target), 0o755); err != nil {
			return result, fmt.Errorf("create target directory for %s: %w", plan.Target, err)
		}
		if err := os.Symlink(plan.Destination, plan.Target); err != nil {
			return result, fmt.Errorf("link %s to %s: %w", plan.Target, plan.Destination, err)
		}
		if err := logf(logOutput, "dotfiles: linked %s -> %s\n", plan.Target, plan.Destination); err != nil {
			return result, err
		}
		result.Created++
	}
	for _, record := range stale {
		owned, err := exactSymlink(record.Target, record.Destination)
		if err != nil {
			return result, err
		}
		if !owned {
			continue
		}
		if dry {
			if err := logf(logOutput, "dotfiles: would remove stale link %s\n", record.Target); err != nil {
				return result, err
			}
			result.Removed++
			continue
		}
		if err := os.Remove(record.Target); err != nil {
			return result, fmt.Errorf("remove stale managed link %s: %w", record.Target, err)
		}
		if err := logf(logOutput, "dotfiles: removed stale link %s\n", record.Target); err != nil {
			return result, err
		}
		result.Removed++
	}
	if !dry && !slices.Equal(previous.Links, records) {
		if err := saveLedger(ledgerPath, records); err != nil {
			return result, err
		}
	}
	return result, nil
}

func logf(output io.Writer, format string, arguments ...any) error {
	if _, err := fmt.Fprintf(output, format, arguments...); err != nil {
		return fmt.Errorf("write link status: %w", err)
	}
	return nil
}

type linkStatus uint8

const (
	linkMissing linkStatus = iota
	linkCurrent
	linkConflict
)

func linkState(target, source string) (linkStatus, error) {
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return linkMissing, nil
		}
		return linkConflict, fmt.Errorf("inspect target %s: %w", target, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return linkConflict, nil
	}

	destination, err := os.Readlink(target)
	if err != nil {
		return linkConflict, fmt.Errorf("read target link %s: %w", target, err)
	}
	if destination == source {
		return linkCurrent, nil
	}
	return linkConflict, nil
}

func exactSymlink(path, destination string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect previous managed target %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}
	actual, err := os.Readlink(path)
	if err != nil {
		return false, fmt.Errorf("read previous managed link %s: %w", path, err)
	}
	return actual == destination, nil
}

func loadLedger(path string) (linkLedger, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return linkLedger{Version: 1}, nil
	}
	if err != nil {
		return linkLedger{}, fmt.Errorf("read managed link state: %w", err)
	}
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

func saveLedger(path string, records []linkRecord) error {
	data, err := json.MarshalIndent(linkLedger{Version: 1, Links: records}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode managed link state: %w", err)
	}
	data = append(data, '\n')
	if err := config.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("save managed link state: %w", err)
	}
	return nil
}

func mappedSymlinkDestination(source, target, managedRoot, home, configRoot string) (string, error) {
	destination, err := os.Readlink(source)
	if err != nil {
		return "", fmt.Errorf("read managed symlink %s: %w", source, err)
	}
	if !filepath.IsAbs(destination) {
		destination = filepath.Join(filepath.Dir(source), destination)
	}
	destination = filepath.Clean(destination)
	if !pathutil.Contains(managedRoot, destination) {
		return "", fmt.Errorf("managed symlink %s points outside the managed home", source)
	}
	resolved, err := filepath.EvalSymlinks(destination)
	if err != nil {
		return "", fmt.Errorf("resolve managed symlink %s: %w", source, err)
	}
	if !pathutil.Contains(managedRoot, resolved) {
		return "", fmt.Errorf("managed symlink %s resolves outside the managed home", source)
	}
	relative, err := filepath.Rel(managedRoot, destination)
	if err != nil {
		return "", fmt.Errorf("map managed symlink %s: %w", source, err)
	}
	targetDestination := filepath.Join(home, relative)
	resolvedParent, err := pathutil.ResolveExistingPrefix(filepath.Dir(targetDestination))
	if err != nil {
		return "", fmt.Errorf("resolve target for managed symlink %s: %w", source, err)
	}
	resolvedTargetDestination := filepath.Join(resolvedParent, filepath.Base(targetDestination))
	if pathutil.Contains(configRoot, resolvedTargetDestination) || pathutil.Contains(resolvedTargetDestination, managedRoot) {
		return "", fmt.Errorf("refusing recursive managed symlink %s -> %s", target, targetDestination)
	}
	resolvedLinkParent, err := pathutil.ResolveExistingPrefix(filepath.Dir(target))
	if err != nil {
		return "", fmt.Errorf("resolve target directory for managed symlink %s: %w", source, err)
	}
	linkDestination, err := filepath.Rel(resolvedLinkParent, resolvedTargetDestination)
	if err != nil {
		return "", fmt.Errorf("make target symlink for %s: %w", target, err)
	}
	return linkDestination, nil
}
