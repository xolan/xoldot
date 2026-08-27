package validation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/xolan/xoldot/internal/config"
)

func TestCheckNeedsOnlyConfigurationLocalState(t *testing.T) {
	paths := initializeConfiguration(t)
	targetHome := filepath.Join(t.TempDir(), "missing", "home")
	t.Setenv("HOME", "")
	t.Setenv(config.TargetHomeEnv, targetHome)
	t.Setenv("SHELL", "")
	t.Setenv("XOLDOT_SHELL", "")
	t.Setenv("PATH", "")

	report := Check(paths)

	if err := report.Err(); err != nil {
		t.Fatalf("Check() error = %v; findings = %+v", err, report.Findings())
	}
	if _, err := os.Stat(targetHome); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("target home was created or inspected unexpectedly: %v", err)
	}
}

func TestCheckReportsIndependentConfigurationFindings(t *testing.T) {
	paths := initializeConfiguration(t)
	writeValidationFile(t, paths.Config, []byte("unknown = true\n"), 0o644)
	writeValidationFile(t, paths.Tools, []byte("[[tool]]\nname = \"broken\"\n"), 0o644)
	writeValidationFile(t, paths.Aliases, []byte("[[alias]]\nname = \"bad name\"\ncommand = \"true\"\n"), 0o644)
	writeValidationFile(t, paths.Skills, []byte("[[skill]]\nname = \"broken\"\n"), 0o644)
	writeValidationFile(t, filepath.Join(paths.Scripts, "before-apply", "bad-name"), []byte("#!/bin/sh\n"), 0o755)
	outside := filepath.Join(t.TempDir(), "outside")
	writeValidationFile(t, outside, []byte("outside"), 0o644)
	if err := os.Symlink(outside, filepath.Join(paths.ManagedHome, "escape")); err != nil {
		t.Fatal(err)
	}

	report := Check(paths)

	for _, kind := range []Kind{Configuration, Tools, Aliases, Skills, ManagedHome, LifecycleScripts} {
		if !reportHasKind(report, kind) {
			t.Errorf("findings do not contain kind %v: %+v", kind, report.Findings())
		}
	}
	if report.ErrorCount() != 6 {
		t.Errorf("ErrorCount() = %d, want 6; findings = %+v", report.ErrorCount(), report.Findings())
	}
}

func TestCheckReportsProfileAndAliasShellFindings(t *testing.T) {
	paths := initializeConfiguration(t)
	writeValidationFile(t, paths.Config, []byte(`[aliases]
shells = ["bash", "nushell"]
`), 0o644)
	writeValidationFile(t, filepath.Join(paths.Profiles, "broken.toml"), []byte(`aliases = ["missing"]
`), 0o644)

	report := Check(paths)

	for _, kind := range []Kind{Profiles, AliasShells} {
		if !reportHasKind(report, kind) {
			t.Errorf("findings do not contain kind %v: %+v", kind, report.Findings())
		}
	}
}

func initializeConfiguration(t *testing.T) config.Paths {
	t.Helper()
	paths := config.NewPaths(t.TempDir())
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	return paths
}

func writeValidationFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func reportHasKind(report Report, kind Kind) bool {
	for _, finding := range report.Findings() {
		if finding.Kind == kind {
			return true
		}
	}
	return false
}
