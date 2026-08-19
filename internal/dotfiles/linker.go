package dotfiles

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Result struct {
	Created int
	Updated int
	Current int
}

func Link(managedRoot, home, configRoot string) (Result, error) {
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

	var result Result
	err = filepath.WalkDir(managedRoot, func(source string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
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
		resolvedParent, err := resolveExistingPrefix(filepath.Dir(target))
		if err != nil {
			return fmt.Errorf("resolve target directory for %s: %w", target, err)
		}
		resolvedTarget := filepath.Join(resolvedParent, filepath.Base(target))
		if pathContains(configRoot, resolvedTarget) || pathContains(resolvedTarget, managedRoot) {
			return fmt.Errorf("refusing recursive link %s -> %s", target, source)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create target directory for %s: %w", target, err)
		}

		state, err := linkState(target, source, managedRoot)
		if err != nil {
			return err
		}
		switch state {
		case linkCurrent:
			result.Current++
			return nil
		case linkConflict:
			return fmt.Errorf("target %s already exists and is not managed by xoldot", target)
		case linkManaged:
			if err := replaceSymlink(target, source); err != nil {
				return err
			}
			result.Updated++
		case linkMissing:
			if err := os.Symlink(source, target); err != nil {
				return fmt.Errorf("link %s to %s: %w", target, source, err)
			}
			result.Created++
		}
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("link managed home: %w", err)
	}
	return result, nil
}

type linkStatus uint8

const (
	linkMissing linkStatus = iota
	linkCurrent
	linkManaged
	linkConflict
)

func linkState(target, source, managedRoot string) (linkStatus, error) {
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
	if !filepath.IsAbs(destination) {
		destination = filepath.Join(filepath.Dir(target), destination)
	}
	destination = filepath.Clean(destination)
	if destination == source {
		return linkCurrent, nil
	}
	if pathContains(managedRoot, destination) {
		return linkManaged, nil
	}
	return linkConflict, nil
}

func replaceSymlink(target, source string) error {
	temporary := target + ".xoldot-new"
	if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear temporary link %s: %w", temporary, err)
	}
	if err := os.Symlink(source, temporary); err != nil {
		return fmt.Errorf("create temporary link %s: %w", temporary, err)
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace link %s: %w", target, err)
	}
	return nil
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func resolveExistingPrefix(path string) (string, error) {
	var suffix []string
	current := filepath.Clean(path)
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}
