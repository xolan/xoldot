package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/config"
)

func TestDoctorPrintsStableSeveritiesRemediesAndFailsOnErrors(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	home := filepath.Join(t.TempDir(), "home")
	paths := config.NewPaths(root)
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Tools, []byte("[[tool]]\nname = [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.ManagedHome, ".vimrc"), []byte("managed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".vimrc"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.TargetHomeEnv, home)
	t.Setenv("XOLDOT_SHELL", "bash")

	var output bytes.Buffer
	err := Run([]string{"--config-dir", root, "doctor"}, bytes.NewReader(nil), &output, &output, "test")
	if err == nil || !strings.Contains(err.Error(), "doctor found 1 error") {
		t.Fatalf("doctor error = %v", err)
	}
	text := output.String()
	errorAt := strings.Index(text, "✗ error:")
	warningAt := strings.Index(text, "! warning:")
	informationAt := strings.Index(text, "› information:")
	if errorAt < 0 || warningAt <= errorAt || informationAt <= warningAt {
		t.Fatalf("output severity order is unstable:\n%s", text)
	}
	if !strings.Contains(text, "  remedy:") {
		t.Errorf("output has no remedy:\n%s", text)
	}
}

func TestDoctorWarningsDoNotFail(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	home := filepath.Join(t.TempDir(), "home")
	paths := config.NewPaths(root)
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.ManagedHome, ".vimrc"), []byte("managed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".vimrc"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.TargetHomeEnv, home)
	t.Setenv("XOLDOT_SHELL", "bash")

	var output bytes.Buffer
	if err := Run([]string{"--config-dir", root, "doctor"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatalf("doctor error = %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "! warning: Managed home conflict") {
		t.Errorf("output = %q", output.String())
	}
}

func TestDoctorStylesSeverityAndRemedyPrefixesForTerminals(t *testing.T) {
	root := filepath.Join(t.TempDir(), "config")
	home := filepath.Join(t.TempDir(), "home")
	paths := config.NewPaths(root)
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Tools, []byte("[[tool]]\nname = [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.TargetHomeEnv, home)
	t.Setenv("XOLDOT_SHELL", "bash")

	var output bytes.Buffer
	application := app{configDir: root, output: &output, style: styler{enabled: true}}
	if err := application.doctor(); err == nil {
		t.Fatal("doctor error = nil, want invalid Tool catalog error")
	}
	for _, want := range []string{
		"\x1b[31m✗\x1b[0m \x1b[31merror:\x1b[0m",
		"\x1b[1mremedy:\x1b[0m",
		"\x1b[36m›\x1b[0m \x1b[36minformation:\x1b[0m",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("styled doctor output does not contain %q:\n%s", want, output.String())
		}
	}
}
