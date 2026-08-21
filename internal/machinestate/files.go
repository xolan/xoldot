package machinestate

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const privateFileMode = 0o600

func AcquireRootedLock(root *os.Root, relative string) (*os.File, error) {
	relative = filepath.FromSlash(relative)
	if err := root.MkdirAll(filepath.Dir(relative), 0o755); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	if info, err := root.Lstat(relative); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("lock is not an ordinary file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect lock: %w", err)
	}

	lock, err := root.OpenFile(relative, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, privateFileMode)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	closeWith := func(err error) (*os.File, error) {
		return nil, errors.Join(err, lock.Close())
	}
	info, err := lock.Stat()
	if err != nil {
		return closeWith(fmt.Errorf("inspect open lock: %w", err))
	}
	if !info.Mode().IsRegular() {
		return closeWith(fmt.Errorf("lock is not an ordinary file"))
	}
	if err := lock.Chmod(privateFileMode); err != nil {
		return closeWith(fmt.Errorf("set lock permissions: %w", err))
	}
	if err := validateRootedLock(root, relative, info); err != nil {
		return closeWith(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return closeWith(fmt.Errorf("acquire lock: %w", err))
	}
	if err := validateRootedLock(root, relative, info); err != nil {
		return nil, errors.Join(err, ReleaseRootedLock(lock))
	}
	return lock, nil
}

func validateRootedLock(root *os.Root, relative string, opened os.FileInfo) error {
	info, err := root.Lstat(relative)
	if err != nil {
		return fmt.Errorf("inspect lock path: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("lock is not an ordinary file")
	}
	if !os.SameFile(opened, info) {
		return fmt.Errorf("lock changed while it was being acquired")
	}
	if info.Mode().Perm() != privateFileMode {
		return fmt.Errorf("lock permissions are %04o, want %04o", info.Mode().Perm(), privateFileMode)
	}
	return nil
}

func ReleaseRootedLock(lock *os.File) error {
	return errors.Join(
		syscall.Flock(int(lock.Fd()), syscall.LOCK_UN),
		lock.Close(),
	)
}

func RandomName(prefix string) (string, error) {
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate random name: %w", err)
	}
	return prefix + hex.EncodeToString(suffix[:]), nil
}
