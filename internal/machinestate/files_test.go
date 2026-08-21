package machinestate

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireRootedLockCreatesPrivateOrdinaryFile(t *testing.T) {
	home := t.TempDir()
	root, err := os.OpenRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	lock, err := AcquireRootedLock(root, LinkLockRelativePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReleaseRootedLock(lock); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(Path(home, LinkLockRelativePath))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("lock mode = %v, want ordinary file", info.Mode())
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("lock permissions = %04o, want 0600", info.Mode().Perm())
	}
}

func TestAcquireRootedLockRepairsPermissionsAndRejectsNonFiles(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, string)
		wantErr bool
	}{
		{
			name: "permissions",
			prepare: func(t *testing.T, path string) {
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "directory",
			prepare: func(t *testing.T, path string) {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: true,
		},
		{
			name: "symlink",
			prepare: func(t *testing.T, path string) {
				target := filepath.Join(filepath.Dir(path), "target")
				if err := os.WriteFile(target, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Base(target), path); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			path := Path(home, LinkLockRelativePath)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			test.prepare(t, path)
			root, err := os.OpenRoot(home)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()

			lock, err := AcquireRootedLock(root, LinkLockRelativePath)
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "not an ordinary file") {
					t.Fatalf("AcquireRootedLock() error = %v, want ordinary-file error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := ReleaseRootedLock(lock); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("lock permissions = %04o, want 0600", info.Mode().Perm())
			}
		})
	}
}

func TestRandomNameAddsTwelveByteHexSuffix(t *testing.T) {
	const prefix = ".xoldot-test-"
	name, err := RandomName(prefix)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.TrimPrefix(name, prefix)
	if suffix == name {
		t.Fatalf("RandomName() = %q, want prefix %q", name, prefix)
	}
	decoded, err := hex.DecodeString(suffix)
	if err != nil {
		t.Fatalf("decode suffix %q: %v", suffix, err)
	}
	if len(decoded) != 12 {
		t.Errorf("suffix length = %d bytes, want 12", len(decoded))
	}
}
