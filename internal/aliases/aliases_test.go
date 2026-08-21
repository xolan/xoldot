package aliases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddUpdatesExistingAlias(t *testing.T) {
	file := File{Aliases: []Alias{{Name: "ll", Command: "ls -l"}}}
	updated, err := Add(&file, "ll", "eza -l")
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if !updated {
		t.Error("Add() updated = false, want true")
	}
	if got := file.Aliases[0].Command; got != "eza -l" {
		t.Errorf("command = %q, want eza -l", got)
	}
}

func TestRenderShellSyntax(t *testing.T) {
	tests := []struct {
		shell string
		want  string
	}{
		{shell: "bash", want: `alias quote='printf '\''hello'\'''`},
		{shell: "zsh", want: `alias quote='printf '\''hello'\'''`},
		{shell: "fish", want: `alias quote 'printf \'hello\''`},
	}
	for _, test := range tests {
		t.Run(test.shell, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "alias."+test.shell)
			err := Render(path, test.shell, []Alias{{Name: "quote", Command: "printf 'hello'"}})
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), test.want) {
				t.Errorf("rendered alias = %q, want it to contain %q", data, test.want)
			}
		})
	}
}

func TestAddRejectsUnsafeName(t *testing.T) {
	var file File
	if _, err := Add(&file, "bad name", "true"); err == nil {
		t.Fatal("Add() error = nil, want invalid name error")
	}
}

func TestPrepareRefusesUnownedOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alias.bash")
	if err := os.WriteFile(path, []byte("alias precious='keep-me'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(path, "bash", nil); err == nil || !strings.Contains(err.Error(), "not managed") {
		t.Fatalf("Prepare() error = %v, want ownership error", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alias precious='keep-me'\n" {
		t.Errorf("unowned output changed: %q", data)
	}
}

func TestPrepareRefusesEditedGeneratedOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alias.bash")
	plan, err := Prepare(path, "bash", []Alias{{Name: "ll", Command: "ls -l"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("alias local='keep-me'\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(path, "bash", []Alias{{Name: "ll", Command: "ls -la"}}); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("Prepare() error = %v, want local changes error", err)
	}
}

func TestPlanApplyRechecksOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alias.bash")
	plan, err := Prepare(path, "bash", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("user data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err == nil || !strings.Contains(err.Error(), "not managed") {
		t.Fatalf("Apply() error = %v, want ownership error", err)
	}
}

func TestLoadRejectsDuplicateAliases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aliases.toml")
	data := `[[alias]]
name = "ll"
command = "ls -l"

[[alias]]
name = "ll"
command = "ls -la"
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("Load() error = %v, want duplicate error", err)
	}
}

func TestPlanCurrentDoesNotRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alias.bash")
	plan, err := Prepare(path, "bash", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = Prepare(path, "bash", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("Apply() replaced an already-current output")
	}
}

func TestInspectReportsMissingOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alias.bash")
	inspection, err := Inspect(path, "bash", nil)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != StateMissing {
		t.Errorf("state = %q, want %q", inspection.State, StateMissing)
	}
}

func TestInspectReportsOwnedReplacementWithUnifiedDiff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alias.bash")
	if err := Render(path, "bash", []Alias{{Name: "ll", Command: "ls -l"}}); err != nil {
		t.Fatal(err)
	}

	inspection, err := Inspect(path, "bash", []Alias{{Name: "ll", Command: "ls -la"}})
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != StateReplaceable {
		t.Fatalf("state = %q, want %q", inspection.State, StateReplaceable)
	}
	diff := inspection.UnifiedDiff()
	for _, want := range []string{
		"--- " + path,
		"+++ " + path + " (planned)",
		"-alias ll='ls -l'",
		"+alias ll='ls -la'",
	} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff does not contain %q:\n%s", want, diff)
		}
	}
}

func TestInspectReportsLocallyModifiedOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alias.bash")
	if err := Render(path, "bash", []Alias{{Name: "ll", Command: "ls -l"}}); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("alias local='keep-me'\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	inspection, err := Inspect(path, "bash", nil)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != StateConflict || !strings.Contains(inspection.Problem, "local changes") {
		t.Errorf("inspection = %+v, want local-change conflict", inspection)
	}
}

func TestInspectReportsNonOwnedOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alias.bash")
	if err := os.WriteFile(path, []byte("alias precious='keep-me'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inspection, err := Inspect(path, "bash", nil)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != StateConflict || !strings.Contains(inspection.Problem, "not managed") {
		t.Errorf("inspection = %+v, want non-owned conflict", inspection)
	}
}
