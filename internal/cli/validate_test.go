package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/config"
)

func TestValidateCommandHonorsConfigDirWithoutMachineInspection(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	targetHome := filepath.Join(t.TempDir(), "missing", "home")
	t.Setenv("HOME", "")
	t.Setenv(config.TargetHomeEnv, targetHome)
	t.Setenv("SHELL", "")
	t.Setenv("XOLDOT_SHELL", "")
	t.Setenv("PATH", "")

	var output bytes.Buffer
	if err := Run([]string{"--config-dir", root, "validate"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatalf("validate error = %v\n%s", err, output.String())
	}
	if got := output.String(); got != "✓ Configuration is valid\n" {
		t.Errorf("validate output = %q", got)
	}
	if _, err := os.Stat(targetHome); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("target home was created: %v", err)
	}
}

func TestValidateCommandReportsActionableConfigurationErrors(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(paths.Profiles, "broken.toml"), []byte(`tools = ["missing"]
`))

	var output bytes.Buffer
	err := Run([]string{"--config-dir", root, "validate"}, bytes.NewReader(nil), &output, &output, "test")
	if err == nil || !strings.Contains(err.Error(), "configuration validation found 1 error") {
		t.Fatalf("validate error = %v\n%s", err, output.String())
	}
	for _, want := range []string{"✗ profile \"broken\" references unknown Tool \"missing\"", "remedy:", paths.Profiles} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("validate output does not contain %q:\n%s", want, output.String())
		}
	}
}
