package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletionGeneratesSupportedShellScripts(t *testing.T) {
	tests := map[string][]string{
		"bash": {"__start_xoldot()", "complete -o default -F __start_xoldot xoldot"},
		"zsh":  {"#compdef xoldot", "_xoldot()"},
		"fish": {"complete -c xoldot", "__xoldot_perform_completion"},
	}

	for _, shell := range completionShellNames() {
		t.Run(shell, func(t *testing.T) {
			configRoot := filepath.Join(t.TempDir(), "must-not-exist")
			var output bytes.Buffer
			if err := Run(
				[]string{"--config-dir", configRoot, "completion", shell},
				bytes.NewReader(nil),
				&output,
				&output,
				"test",
			); err != nil {
				t.Fatalf("completion %s error = %v", shell, err)
			}
			if output.Len() == 0 {
				t.Fatalf("completion %s output is empty", shell)
			}
			for _, content := range tests[shell] {
				if !strings.Contains(output.String(), content) {
					t.Errorf("completion %s output does not contain %q", shell, content)
				}
			}
			if _, err := os.Stat(configRoot); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("completion created configuration directory: %v", err)
			}
		})
	}
}

func TestCompletionRejectsUnsupportedShell(t *testing.T) {
	var output bytes.Buffer
	err := Run([]string{"completion", "powershell"}, bytes.NewReader(nil), &output, &output, "test")
	if err == nil || !strings.Contains(err.Error(), "supported shells are bash, zsh, and fish") {
		t.Fatalf("completion error = %v, want supported shell list", err)
	}
	if output.Len() != 0 {
		t.Errorf("completion output = %q, want empty output", output.String())
	}
}

func TestCompletionOffersSupportedShellValues(t *testing.T) {
	var output bytes.Buffer
	if err := Run([]string{"__complete", "completion", ""}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatalf("completion values error = %v", err)
	}
	for _, shell := range completionShellNames() {
		if !strings.Contains(output.String(), shell) {
			t.Errorf("completion values = %q, want %q", output.String(), shell)
		}
	}
}

func TestCompletionOffersCommandsFlagsAndApplyParts(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		want      []string
	}{
		{name: "commands", arguments: []string{"__complete", ""}, want: []string{"apply", "completion", "restore"}},
		{name: "flags", arguments: []string{"__complete", "apply", "--"}, want: []string{"--backup", "--dry", "--only"}},
		{name: "apply parts", arguments: []string{"__complete", "apply", "--only", ""}, want: applyPartValues()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := Run(test.arguments, bytes.NewReader(nil), &output, &output, "test"); err != nil {
				t.Fatalf("completion error = %v", err)
			}
			for _, value := range test.want {
				if !strings.Contains(output.String(), value+"\n") && !strings.Contains(output.String(), value+"\t") {
					t.Errorf("completion output = %q, want %q", output.String(), value)
				}
			}
		})
	}
}
