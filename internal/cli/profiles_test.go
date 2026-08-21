package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/config"
)

func TestProfileFiltersApplyStatusAndDiffAndSwitchesSafely(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	paths := config.NewPaths(root)
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, paths.Tools, []byte(`[[tool]]
name = "work-tool"
check = "exit 0"

[tool.install]
macos = "true"

[tool.install.linux]
default = "true"

[[tool]]
name = "personal-tool"
check = "exit 0"

[tool.install]
macos = "true"

[tool.install.linux]
default = "true"
`))
	writeTestFile(t, paths.Aliases, []byte(`[[alias]]
name = "ll"
command = "ls -la"

[[alias]]
name = "gs"
command = "git status"
`))
	workSource := filepath.Join(paths.ManagedHome, ".config", "work.toml")
	personalSource := filepath.Join(paths.ManagedHome, ".config", "personal.toml")
	writeTestFile(t, workSource, []byte("work"))
	writeTestFile(t, personalSource, []byte("personal"))
	writeTestFile(t, filepath.Join(paths.Profiles, "base.toml"), []byte(`aliases = ["ll"]
`))
	writeTestFile(t, filepath.Join(paths.Profiles, "work.toml"), []byte(`extends = ["base"]
tools = ["work-tool"]
managed_home = [".config/work.toml"]
`))
	writeTestFile(t, filepath.Join(paths.Profiles, "personal.toml"), []byte(`tools = ["personal-tool"]
aliases = ["gs"]
managed_home = [".config/personal.toml"]
`))
	t.Setenv(config.TargetHomeEnv, home)
	t.Setenv("XOLDOT_SHELL", "bash")

	workTarget := filepath.Join(home, ".config", "work.toml")
	personalTarget := filepath.Join(home, ".config", "personal.toml")
	aliasPath := filepath.Join(home, ".aliases", "alias.bash")
	runCLI(t, root, "apply", "--profile", "work")
	assertLinkDestination(t, workTarget, workSource)
	assertPathMissing(t, personalTarget)
	assertFileContains(t, aliasPath, "alias ll='ls -la'")
	assertFileExcludes(t, aliasPath, "alias gs=")

	statusOutput := runCLI(t, root, "status", "--profile", "personal")
	for _, want := range []string{
		"stale " + workTarget,
		"missing " + personalTarget,
		"replaceable " + aliasPath,
		"unchecked 1 declared tool",
	} {
		if !strings.Contains(statusOutput, want) {
			t.Errorf("status output does not contain %q:\n%s", want, statusOutput)
		}
	}

	diffOutput := runCLI(t, root, "diff", "--profile", "personal")
	for _, want := range []string{
		"Would remove stale link " + workTarget,
		"Would link " + personalTarget + " -> " + personalSource,
		"-alias ll='ls -la'",
		"+alias gs='git status'",
	} {
		if !strings.Contains(diffOutput, want) {
			t.Errorf("diff output does not contain %q:\n%s", want, diffOutput)
		}
	}

	runCLI(t, root, "apply", "--profile", "personal")
	assertPathMissing(t, workTarget)
	assertLinkDestination(t, personalTarget, personalSource)
	assertFileContains(t, aliasPath, "alias gs='git status'")
	assertFileExcludes(t, aliasPath, "alias ll=")

	dryOutput := runCLI(t, root, "apply", "--dry", "--profile", "work")
	for _, want := range []string{
		"Would check tool work-tool",
		"Would link " + workTarget + " -> " + workSource,
		"Would remove stale link " + personalTarget,
		"Would render aliases to " + aliasPath,
	} {
		if !strings.Contains(dryOutput, want) {
			t.Errorf("dry Apply output does not contain %q:\n%s", want, dryOutput)
		}
	}

	runCLI(t, root, "apply", "--profile", "work")
	if err := os.Remove(workTarget); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, workTarget, []byte("local replacement"))
	runCLI(t, root, "apply", "--profile", "personal")
	data, err := os.ReadFile(workTarget)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "local replacement" {
		t.Errorf("unowned stale target changed to %q", got)
	}
}

func TestProfileSkillIncludesCanonicalCompatibilityAndCompanionPaths(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	paths := config.NewPaths(root)
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	installProfileSkill(t, paths, "selected", "reviewer.md")
	installProfileSkill(t, paths, "other", "other.md")
	writeTestFile(t, paths.Skills, []byte(`[[skill]]
name = "selected"
source = "owner/repo"
digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
agents = ["reviewer.md"]

[[skill]]
name = "other"
source = "owner/repo"
digest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
agents = ["other.md"]
`))
	writeTestFile(t, filepath.Join(paths.Profiles, "selected.toml"), []byte(`skills = ["selected"]
`))
	t.Setenv(config.TargetHomeEnv, home)
	t.Setenv("XOLDOT_SHELL", "bash")

	runCLI(t, root, "apply", "--only", "managed-home", "--profile", "selected")
	for _, relative := range []string{
		filepath.Join(".agents", "skills", "selected"),
		filepath.Join(".claude", "skills", "selected"),
		filepath.Join(".agents", "agents", "reviewer.md"),
		filepath.Join(".claude", "agents", "reviewer.md"),
	} {
		if _, err := os.Lstat(filepath.Join(home, relative)); err != nil {
			t.Errorf("selected Skill path %s is missing: %v", relative, err)
		}
	}
	for _, relative := range []string{
		filepath.Join(".agents", "skills", "other"),
		filepath.Join(".claude", "skills", "other"),
		filepath.Join(".agents", "agents", "other.md"),
		filepath.Join(".claude", "agents", "other.md"),
	} {
		if _, err := os.Lstat(filepath.Join(home, relative)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("unselected Skill path %s exists: %v", relative, err)
		}
	}
}

func TestProfileSkillPreservesLegacyCompanionAgent(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	paths := config.NewPaths(root)
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	installLegacyProfileSkill(t, paths, "selected", "reviewer.md")
	writeTestFile(t, paths.Skills, []byte(`[[skill]]
name = "selected"
source = "owner/repo"
digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`))
	writeTestFile(t, filepath.Join(paths.Profiles, "selected.toml"), []byte(`skills = ["selected"]
`))
	t.Setenv(config.TargetHomeEnv, home)
	t.Setenv("XOLDOT_SHELL", "bash")

	legacyAgent := filepath.Join(home, ".claude", "agents", "reviewer.md")
	runCLI(t, root, "apply", "--only", "managed-home")
	if _, err := os.Lstat(legacyAgent); err != nil {
		t.Fatalf("legacy Companion agent was not linked: %v", err)
	}
	runCLI(t, root, "apply", "--only", "managed-home", "--profile", "selected")
	if _, err := os.Lstat(legacyAgent); err != nil {
		t.Fatalf("Profile Apply removed legacy Companion agent: %v", err)
	}
}

func TestProfileValidationPrecedesToolMutation(t *testing.T) {
	root := t.TempDir()
	paths := config.NewPaths(root)
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "installed")
	writeTestFile(t, paths.Tools, []byte(fmt.Sprintf(`[[tool]]
name = "mutating"
check = "false"

[tool.install]
macos = "touch %s"

[tool.install.linux]
default = "touch %s"
`, marker, marker)))
	writeTestFile(t, filepath.Join(paths.Profiles, "selected.toml"), []byte(`tools = ["mutating"]
`))
	writeTestFile(t, filepath.Join(paths.Profiles, "broken.toml"), []byte(`aliases = ["missing"]
`))

	var output bytes.Buffer
	err := Run(
		[]string{"--config-dir", root, "apply", "--only", "tools", "--profile", "selected"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	)
	if err == nil || !strings.Contains(err.Error(), "unknown Alias") {
		t.Fatalf("Apply error = %v, want profile validation error", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Tool mutation happened before profile validation: %v", err)
	}
}

func TestProfileFlagIsDocumentedForSelectionCommands(t *testing.T) {
	for _, command := range []string{"apply", "status", "diff"} {
		var output bytes.Buffer
		if err := Run([]string{command, "--help"}, bytes.NewReader(nil), &output, &output, "test"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "--profile") {
			t.Errorf("%s help does not document --profile:\n%s", command, output.String())
		}
	}
}

func runCLI(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	arguments = append([]string{"--config-dir", root}, arguments...)
	var output bytes.Buffer
	if err := Run(arguments, bytes.NewReader(nil), &output, &output, "test"); err != nil {
		t.Fatalf("xoldot %s error = %v\noutput:\n%s", strings.Join(arguments, " "), err, output.String())
	}
	return output.String()
}

func assertLinkDestination(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("Readlink(%s) error = %v", path, err)
	}
	if got != want {
		t.Errorf("Readlink(%s) = %q, want %q", path, got, want)
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), want) {
		t.Errorf("%s does not contain %q:\n%s", path, want, data)
	}
}

func assertFileExcludes(t *testing.T, path, unwanted string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), unwanted) {
		t.Errorf("%s contains %q:\n%s", path, unwanted, data)
	}
}

func installProfileSkill(t *testing.T, paths config.Paths, name, agent string) {
	t.Helper()
	canonicalSkill := filepath.Join(paths.ManagedHome, ".agents", "skills", name, "SKILL.md")
	compatibilitySkill := filepath.Join(paths.ManagedHome, ".claude", "skills", name, "SKILL.md")
	writeTestFile(t, canonicalSkill, []byte("skill "+name))
	if err := os.MkdirAll(filepath.Dir(compatibilitySkill), 0o755); err != nil {
		t.Fatal(err)
	}
	skillDestination, err := filepath.Rel(filepath.Dir(compatibilitySkill), canonicalSkill)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(skillDestination, compatibilitySkill); err != nil {
		t.Fatal(err)
	}

	canonicalAgent := filepath.Join(paths.ManagedHome, ".agents", "agents", agent)
	compatibilityAgent := filepath.Join(paths.ManagedHome, ".claude", "agents", agent)
	writeTestFile(t, canonicalAgent, []byte("agent "+name))
	if err := os.MkdirAll(filepath.Dir(compatibilityAgent), 0o755); err != nil {
		t.Fatal(err)
	}
	agentDestination, err := filepath.Rel(filepath.Dir(compatibilityAgent), canonicalAgent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(agentDestination, compatibilityAgent); err != nil {
		t.Fatal(err)
	}
}

func installLegacyProfileSkill(t *testing.T, paths config.Paths, name, agent string) {
	t.Helper()
	canonicalSkill := filepath.Join(paths.ManagedHome, ".agents", "skills", name)
	compatibilitySkill := filepath.Join(paths.ManagedHome, ".claude", "skills", name)
	writeTestFile(t, filepath.Join(canonicalSkill, "SKILL.md"), []byte("skill "+name))
	writeTestFile(t, filepath.Join(compatibilitySkill, "SKILL.md"), []byte("skill "+name))

	legacyAgent := filepath.Join(canonicalSkill, ".xoldot-agents", agent)
	compatibilityAgent := filepath.Join(paths.ManagedHome, ".claude", "agents", agent)
	writeTestFile(t, legacyAgent, []byte("agent "+name))
	if err := os.MkdirAll(filepath.Dir(compatibilityAgent), 0o755); err != nil {
		t.Fatal(err)
	}
	destination, err := filepath.Rel(filepath.Dir(compatibilityAgent), legacyAgent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(destination, compatibilityAgent); err != nil {
		t.Fatal(err)
	}
}
