package lifecyclescripts

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xolan/xoldot/internal/machinestate"
	"github.com/xolan/xoldot/internal/pathutil"
)

const stateRelativePath = machinestate.ScriptsStateRelativePath

type scriptState map[string]string

type stateStore struct {
	home string
}

type stateTransaction struct {
	state   scriptState
	pending *stateEntry
}

type stateFile struct {
	Version int          `json:"version"`
	Scripts []stateEntry `json:"scripts"`
}

type stateEntry struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

func inspectStateStore(targetHome string) (stateStore, scriptState, error) {
	home, err := filepath.Abs(targetHome)
	if err != nil {
		return stateStore{}, nil, fmt.Errorf("resolve Target home: %w", err)
	}
	home, err = pathutil.ResolveExistingPrefix(home)
	if err != nil {
		return stateStore{}, nil, fmt.Errorf("resolve Target home: %w", err)
	}
	store := stateStore{home: home}
	state, err := store.load()
	return store, state, err
}

func (store stateStore) load() (scriptState, error) {
	info, err := os.Stat(store.home)
	if errors.Is(err, os.ErrNotExist) {
		return make(scriptState), nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect Target home %s: %w", store.home, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("target home %s is not a directory", store.home)
	}

	root, err := os.OpenRoot(store.home)
	if err != nil {
		return nil, fmt.Errorf("open Target home %s: %w", store.home, err)
	}
	defer func() { _ = root.Close() }()
	return loadStateFromRoot(root, store.home)
}

func loadStateFromRoot(root *os.Root, home string) (scriptState, error) {
	relative := filepath.FromSlash(stateRelativePath)
	info, err := root.Lstat(relative)
	if errors.Is(err, os.ErrNotExist) {
		return make(scriptState), nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect lifecycle script state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("lifecycle script state %s is not an ordinary file", filepath.Join(home, relative))
	}
	data, err := root.ReadFile(relative)
	if err != nil {
		return nil, fmt.Errorf("read lifecycle script state: %w", err)
	}
	state, err := decodeState(data)
	if err != nil {
		return nil, fmt.Errorf("parse lifecycle script state %s: %w", filepath.Join(home, relative), err)
	}
	return state, nil
}

func decodeState(data []byte) (scriptState, error) {
	var file stateFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("state contains more than one JSON value")
		}
		return nil, err
	}
	if file.Version != 1 {
		return nil, fmt.Errorf("unsupported lifecycle script state version %d", file.Version)
	}
	state := make(scriptState, len(file.Scripts))
	for _, entry := range file.Scripts {
		if !validStatePath(entry.Path) {
			return nil, fmt.Errorf("invalid recorded lifecycle script path %q", entry.Path)
		}
		if !validDigest(entry.Digest) {
			return nil, fmt.Errorf("invalid recorded lifecycle script digest for %q", entry.Path)
		}
		if _, exists := state[entry.Path]; exists {
			return nil, fmt.Errorf("recorded lifecycle script path %q is duplicated", entry.Path)
		}
		state[entry.Path] = entry.Digest
	}
	return state, nil
}

func validStatePath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if clean != path {
		return false
	}
	return strings.HasPrefix(path, string(BeforeApply)+"/") || strings.HasPrefix(path, string(AfterApply)+"/")
}

func validDigest(digest string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(digest, prefix) || len(digest) != len(prefix)+sha256HexLength {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(digest, prefix))
	return err == nil
}

const sha256HexLength = 64

func (store stateStore) withLockedState(action func(*stateTransaction) error) (scriptState, error) {
	if err := os.MkdirAll(store.home, 0o755); err != nil {
		return nil, fmt.Errorf("create Target home %s: %w", store.home, err)
	}
	root, err := os.OpenRoot(store.home)
	if err != nil {
		return nil, fmt.Errorf("open Target home %s: %w", store.home, err)
	}
	lock, err := acquireStateLock(root, store.home)
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	closeLockedState := func() error {
		return errors.Join(releaseStateLock(lock), root.Close())
	}

	state, err := loadStateFromRoot(root, store.home)
	if err != nil {
		return nil, errors.Join(err, closeLockedState())
	}
	transaction := stateTransaction{state: state}
	if err := action(&transaction); err != nil {
		return state, errors.Join(err, closeLockedState())
	}
	if transaction.pending != nil {
		entry := *transaction.pending
		previous, existed := state[entry.Path]
		state[entry.Path] = entry.Digest
		if err := writeState(root, store.home, state); err != nil {
			if existed {
				state[entry.Path] = previous
			} else {
				delete(state, entry.Path)
			}
			return state, errors.Join(err, closeLockedState())
		}
	}
	return state, closeLockedState()
}

func (transaction *stateTransaction) recordSuccess(path, digest string) {
	transaction.pending = &stateEntry{Path: path, Digest: digest}
}

func acquireStateLock(root *os.Root, home string) (*os.File, error) {
	lock, err := machinestate.AcquireRootedLock(root, machinestate.ScriptsLockRelativePath)
	if err != nil {
		return nil, fmt.Errorf("acquire lifecycle script state lock %s: %w", machinestate.Path(home, machinestate.ScriptsLockRelativePath), err)
	}
	return lock, nil
}

func releaseStateLock(lock *os.File) error {
	if err := machinestate.ReleaseRootedLock(lock); err != nil {
		return fmt.Errorf("release lifecycle script state lock: %w", err)
	}
	return nil
}

func writeState(root *os.Root, home string, state scriptState) error {
	relative := filepath.FromSlash(stateRelativePath)
	if info, err := root.Lstat(relative); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("lifecycle script state %s is not an ordinary file", filepath.Join(home, relative))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect lifecycle script state: %w", err)
	}
	data, err := encodeState(state)
	if err != nil {
		return err
	}
	temporary, err := temporaryStateName(filepath.Dir(relative))
	if err != nil {
		return err
	}
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary lifecycle script state: %w", err)
	}
	defer func() { _ = root.Remove(temporary) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set temporary lifecycle script state permissions: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temporary lifecycle script state: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync temporary lifecycle script state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary lifecycle script state: %w", err)
	}
	if err := root.Rename(temporary, relative); err != nil {
		return fmt.Errorf("replace lifecycle script state: %w", err)
	}
	return nil
}

func encodeState(state scriptState) ([]byte, error) {
	paths := make([]string, 0, len(state))
	for path := range state {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	file := stateFile{Version: 1, Scripts: make([]stateEntry, 0, len(paths))}
	for _, path := range paths {
		file.Scripts = append(file.Scripts, stateEntry{Path: path, Digest: state[path]})
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode lifecycle script state: %w", err)
	}
	return append(data, '\n'), nil
}

func temporaryStateName(directory string) (string, error) {
	name, err := machinestate.RandomName(".xoldot-scripts-")
	if err != nil {
		return "", fmt.Errorf("choose temporary lifecycle script state name: %w", err)
	}
	return filepath.Join(directory, name), nil
}
