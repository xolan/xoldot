package managedhome

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateChecksManagedHomeWithoutTargetHome(t *testing.T) {
	configRoot := t.TempDir()
	managedRoot := filepath.Join(configRoot, "files", "home")
	writeManagedValidationFile(t, filepath.Join(managedRoot, ".config", "tool", "config.toml"), []byte("valid"))
	if err := os.Symlink("config.toml", filepath.Join(managedRoot, ".config", "tool", "current.toml")); err != nil {
		t.Fatal(err)
	}

	if err := Validate(managedRoot, configRoot); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsManagedHomeEscapeAndReservedState(t *testing.T) {
	t.Run("symlink escape", func(t *testing.T) {
		configRoot := t.TempDir()
		managedRoot := filepath.Join(configRoot, "files", "home")
		if err := os.MkdirAll(managedRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "outside")
		writeManagedValidationFile(t, outside, []byte("outside"))
		if err := os.Symlink(outside, filepath.Join(managedRoot, "escape")); err != nil {
			t.Fatal(err)
		}

		if err := Validate(managedRoot, configRoot); err == nil || !strings.Contains(err.Error(), "outside the managed home") {
			t.Fatalf("Validate() error = %v, want managed home escape", err)
		}
	})

	t.Run("reserved state", func(t *testing.T) {
		configRoot := t.TempDir()
		managedRoot := filepath.Join(configRoot, "files", "home")
		writeManagedValidationFile(t, filepath.Join(managedRoot, ".local", "state", "xoldot", "links.json"), []byte("{}"))

		if err := Validate(managedRoot, configRoot); err == nil || !strings.Contains(err.Error(), "reserved for xoldot state") {
			t.Fatalf("Validate() error = %v, want reserved state error", err)
		}
	})
}

func writeManagedValidationFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
