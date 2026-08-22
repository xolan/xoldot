package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/config"
)

func TestApplyOnlyRunsSelectedParts(t *testing.T) {
	tests := []struct {
		name    string
		parts   []string
		tools   bool
		managed bool
		aliases bool
	}{
		{name: "tools", parts: []string{"tools"}, tools: true},
		{name: "managed home", parts: []string{"managed-home"}, managed: true},
		{name: "aliases", parts: []string{"aliases"}, aliases: true},
		{name: "tools and managed home", parts: []string{"managed-home", "tools"}, tools: true, managed: true},
		{name: "tools and aliases", parts: []string{"aliases", "tools"}, tools: true, aliases: true},
		{name: "managed home and aliases", parts: []string{"aliases", "managed-home"}, managed: true, aliases: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newApplyFixture(t)
			if !test.tools {
				writeTestFile(t, fixture.paths.Tools, []byte("not valid TOML = ["))
			}
			if !test.aliases {
				writeTestFile(t, fixture.paths.Aliases, []byte("not valid TOML = ["))
				t.Setenv("XOLDOT_SHELL", "unsupported")
			}
			if !test.managed {
				writeTestFile(t, fixture.managedTarget, []byte("user content\n"))
			}

			arguments := []string{"--config-dir", fixture.root, "apply"}
			for _, part := range test.parts {
				arguments = append(arguments, "--only", part)
			}
			var output bytes.Buffer
			if err := Run(arguments, bytes.NewReader(nil), &output, &output, "test"); err != nil {
				t.Fatalf("apply error = %v\noutput:\n%s", err, output.String())
			}

			assertApplyPartOutput(t, output.String(), test.tools, test.managed, test.aliases)
			assertApplyPartChanges(t, fixture, test.managed, test.aliases)
		})
	}
}

func TestApplyWithoutOnlyRunsAllPartsInOrder(t *testing.T) {
	fixture := newApplyFixture(t)
	var output bytes.Buffer
	if err := Run(
		[]string{"--config-dir", fixture.root, "apply"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	); err != nil {
		t.Fatalf("apply error = %v\noutput:\n%s", err, output.String())
	}

	want := fmt.Sprintf(
		"✓ Tool probe is already installed\n"+
			"› Linking %s -> %s\n"+
			"✓ Managed home links: 1 created, 0 removed, 0 already current\n"+
			"✓ Rendered aliases to %s\n",
		fixture.managedTarget,
		fixture.managedSource,
		fixture.aliasPath,
	)
	if got := output.String(); got != want {
		t.Errorf("apply output = %q, want %q", got, want)
	}
	assertApplyPartChanges(t, fixture, true, true)
}

func TestApplyOnlyDryRunsSelectedPart(t *testing.T) {
	tests := []struct {
		part       string
		wantOutput string
	}{
		{part: "tools", wantOutput: "Would check tool probe"},
		{part: "managed-home", wantOutput: "Would link "},
		{part: "aliases", wantOutput: "Would render aliases"},
	}

	for _, test := range tests {
		t.Run(test.part, func(t *testing.T) {
			fixture := newApplyFixture(t)
			var output bytes.Buffer
			if err := Run(
				[]string{"--config-dir", fixture.root, "apply", "--dry", "--only", test.part},
				bytes.NewReader(nil),
				&output,
				&output,
				"test",
			); err != nil {
				t.Fatalf("dry apply error = %v\noutput:\n%s", err, output.String())
			}
			if !strings.Contains(output.String(), test.wantOutput) {
				t.Errorf("dry apply output = %q, want %q", output.String(), test.wantOutput)
			}
			assertApplyPartOutput(
				t,
				output.String(),
				test.part == "tools",
				test.part == "managed-home",
				test.part == "aliases",
			)
			assertPathMissing(t, fixture.managedTarget)
			assertPathMissing(t, fixture.aliasPath)
		})
	}
}

func TestApplyOnlyCollapsesDuplicates(t *testing.T) {
	fixture := newApplyFixture(t)
	writeTestFile(t, fixture.paths.Tools, []byte("not valid TOML = ["))
	writeTestFile(t, fixture.paths.Aliases, []byte("not valid TOML = ["))
	t.Setenv("XOLDOT_SHELL", "unsupported")

	var output bytes.Buffer
	if err := Run(
		[]string{"--config-dir", fixture.root, "apply", "--only", "managed-home", "--only", "managed-home"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	); err != nil {
		t.Fatalf("apply error = %v\noutput:\n%s", err, output.String())
	}
	if count := strings.Count(output.String(), "Managed home links:"); count != 1 {
		t.Errorf("managed home summary count = %d, want 1; output = %q", count, output.String())
	}
	assertApplyPartChanges(t, fixture, true, false)
}

func TestApplyOnlyRejectsUnknownPartBeforeMutation(t *testing.T) {
	fixture := newApplyFixture(t)
	var output bytes.Buffer
	err := Run(
		[]string{"--config-dir", fixture.root, "apply", "--only", "managed-home", "--only", "unknown"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	)
	if err == nil || !strings.Contains(err.Error(), `unknown apply part "unknown"`) {
		t.Fatalf("apply error = %v, want unknown part error", err)
	}
	if output.Len() != 0 {
		t.Errorf("apply reported work before rejecting selection: %q", output.String())
	}
	assertPathMissing(t, fixture.managedTarget)
	assertPathMissing(t, fixture.aliasPath)
}

func TestApplyOnlyStillRequiresTopLevelConfiguration(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	err := Run(
		[]string{"--config-dir", root, "apply", "--only", "tools"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	)
	if err == nil || !strings.Contains(err.Error(), "configuration not found") {
		t.Fatalf("apply error = %v, want missing Configuration error", err)
	}
}

func TestApplyHelpDocumentsOnlyValuesAndDefault(t *testing.T) {
	var output bytes.Buffer
	if err := Run([]string{"apply", "--help"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"--only", "tools", "managed-home", "aliases", "default: all"} {
		if !strings.Contains(output.String(), text) {
			t.Errorf("apply help does not include %q:\n%s", text, output.String())
		}
	}
}

func TestApplyOnlyCompletesEveryPart(t *testing.T) {
	root := (&app{}).rootCommand("test")
	applyCommand, _, err := root.Find([]string{"apply"})
	if err != nil {
		t.Fatal(err)
	}
	completion, exists := applyCommand.GetFlagCompletionFunc("only")
	if !exists {
		t.Fatal("--only completion is not registered")
	}
	values, directive := completion(applyCommand, nil, "")
	if want := []string{"tools", "managed-home", "aliases"}; !slices.Equal(values, want) {
		t.Errorf("--only completions = %v, want %v", values, want)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("--only completion directive = %v, want no file completion", directive)
	}
}

type applyFixture struct {
	root          string
	paths         config.Paths
	managedSource string
	managedTarget string
	aliasPath     string
}

func newApplyFixture(t *testing.T) applyFixture {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	paths := config.NewPaths(root)
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, paths.Tools, []byte(`[[tool]]
name = "probe"
check = "exit 0"

[tool.install]
macos = "exit 1"

[tool.install.linux]
default = "exit 1"
`))
	writeTestFile(t, paths.Aliases, []byte(`[[alias]]
name = "ll"
command = "ls -la"
`))
	managedSource := filepath.Join(paths.ManagedHome, ".managed")
	writeTestFile(t, managedSource, []byte("managed content\n"))
	t.Setenv(config.TargetHomeEnv, home)
	t.Setenv("XOLDOT_SHELL", "bash")

	return applyFixture{
		root:          root,
		paths:         paths,
		managedSource: managedSource,
		managedTarget: filepath.Join(home, ".managed"),
		aliasPath:     filepath.Join(home, ".aliases", "alias.bash"),
	}
}

func assertApplyPartOutput(t *testing.T, output string, tools, managed, aliases bool) {
	t.Helper()
	lowerOutput := strings.ToLower(output)
	markers := []struct {
		text string
		want bool
	}{
		{text: "tool probe", want: tools},
		{text: "Managed home links:", want: managed},
		{text: "aliases to ", want: aliases},
	}
	last := -1
	for _, marker := range markers {
		index := strings.Index(lowerOutput, strings.ToLower(marker.text))
		if marker.want && index < 0 {
			t.Errorf("apply output = %q, want %q", output, marker.text)
		}
		if !marker.want && index >= 0 {
			t.Errorf("apply output = %q, do not want %q", output, marker.text)
		}
		if marker.want && index < last {
			t.Errorf("apply output is out of Apply part order: %q", output)
		}
		if marker.want {
			last = index
		}
	}
}

func assertApplyPartChanges(t *testing.T, fixture applyFixture, managed, aliases bool) {
	t.Helper()
	if managed {
		destination, err := os.Readlink(fixture.managedTarget)
		if err != nil {
			t.Fatalf("read managed link: %v", err)
		}
		if destination != fixture.managedSource {
			t.Errorf("managed link = %q, want %q", destination, fixture.managedSource)
		}
	} else {
		data, err := os.ReadFile(fixture.managedTarget)
		if err != nil {
			t.Fatalf("read unselected managed target: %v", err)
		}
		if got, want := string(data), "user content\n"; got != want {
			t.Errorf("unselected managed target = %q, want %q", got, want)
		}
	}

	if aliases {
		data, err := os.ReadFile(fixture.aliasPath)
		if err != nil {
			t.Fatalf("read rendered aliases: %v", err)
		}
		if !bytes.Contains(data, []byte("alias ll='ls -la'")) {
			t.Errorf("rendered aliases = %q", data)
		}
	} else {
		assertPathMissing(t, fixture.aliasPath)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("path %s exists or cannot be inspected, error = %v", path, err)
	}
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
