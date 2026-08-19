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
