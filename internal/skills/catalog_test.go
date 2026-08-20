package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAddArguments(t *testing.T) {
	tests := []struct {
		name       string
		arguments  []string
		wantName   string
		wantSource string
	}{
		{
			name:       "GitHub shorthand",
			arguments:  []string{"unslop@poteto/plugins"},
			wantName:   "unslop",
			wantSource: "https://github.com/poteto/plugins",
		},
		{
			name:       "explicit source",
			arguments:  []string{"unslop", "--from", "https://git.example.com/poteto/plugins.git"},
			wantName:   "unslop",
			wantSource: "https://git.example.com/poteto/plugins.git",
		},
		{
			name:       "GitHub source shorthand",
			arguments:  []string{"unslop", "--from", "poteto/plugins"},
			wantName:   "unslop",
			wantSource: "https://github.com/poteto/plugins",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name, source, err := ParseAddArguments(test.arguments)
			if err != nil {
				t.Fatalf("ParseAddArguments() error = %v", err)
			}
			if name != test.wantName || source != test.wantSource {
				t.Errorf("ParseAddArguments() = %q, %q; want %q, %q", name, source, test.wantName, test.wantSource)
			}
		})
	}
}

func TestParseAddArgumentsRejectsAmbiguousOrUnsafeInput(t *testing.T) {
	for _, arguments := range [][]string{
		{"unslop"},
		{"../unslop@poteto/plugins"},
		{"unslop@poteto/plugins/extra"},
		{"Unslop@poteto/plugins"},
	} {
		if _, _, err := ParseAddArguments(arguments); err == nil {
			t.Errorf("ParseAddArguments(%q) error = nil", arguments)
		}
	}
}

func TestNormalizeSourceResolvesRelativePaths(t *testing.T) {
	base := t.TempDir()
	got, err := NormalizeSource("./skills/example", base)
	if err != nil {
		t.Fatalf("NormalizeSource() error = %v", err)
	}
	want := filepath.Join(base, "skills", "example")
	if got != want {
		t.Errorf("NormalizeSource() = %q, want %q", got, want)
	}
}

func TestNormalizeSourceRejectsCredentialBearingURL(t *testing.T) {
	for _, source := range []string{
		"https://token@github.com/owner/repo",
		"https://github.com/owner/repo?token=secret",
		"git+https://token@github.com/owner/repo",
	} {
		if _, err := NormalizeSource(source, t.TempDir()); err == nil || !strings.Contains(err.Error(), "credentials") {
			t.Errorf("NormalizeSource(%q) error = %v", source, err)
		}
	}
}

func TestLoadRejectsDuplicateSkills(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills.toml")
	data := `[[skill]]
name = "example"
source = "https://github.com/owner/one"
digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

[[skill]]
name = "example"
source = "https://github.com/owner/two"
digest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("Load() error = %v, want duplicate error", err)
	}
}
