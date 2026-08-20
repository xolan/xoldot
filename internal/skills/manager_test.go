package skills

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/status"
)

type fakeRunner struct {
	commands      []Command
	versions      []map[string]string
	agentVersions []map[string]string
	fetches       int
	failAt        int
}

func (runner *fakeRunner) Fetch(request RepositoryRequest) error {
	index := runner.fetches
	runner.fetches++
	if index >= len(runner.versions) {
		return nil
	}
	skill := filepath.Join(request.Destination, "plugin", "skills", request.Name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		return err
	}
	if contents, exists := runner.versions[index]["SKILL.md"]; exists {
		if err := os.WriteFile(skill, []byte(contents), 0o644); err != nil {
			return err
		}
	}
	if index >= len(runner.agentVersions) {
		return nil
	}
	for relative, contents := range runner.agentVersions[index] {
		path := filepath.Join(request.Destination, "plugin", "agents", filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			return err
		}
	}
	return nil
}

type noopRunner struct{}

func (noopRunner) Run(Command) error { return nil }

func (runner *fakeRunner) Run(command Command) error {
	runner.commands = append(runner.commands, command)
	if runner.failAt > 0 && len(runner.commands) == runner.failAt {
		return errors.New("simulated npx failure")
	}
	home, _ := environmentValue(command.Environment, "HOME")
	if slices.Contains(command.Arguments, "add") {
		name := argumentAfter(command.Arguments, "--skill")
		version := runner.versions[len(runner.commands)-1]
		for relative, contents := range version {
			path := filepath.Join(home, ".agents", "skills", name, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	if slices.Contains(command.Arguments, "remove") {
		name := command.Arguments[3]
		if err := os.RemoveAll(filepath.Join(home, ".agents", "skills", name)); err != nil {
			return err
		}
		return os.RemoveAll(filepath.Join(home, ".claude", "skills", name))
	}
	return errors.New("unexpected command")
}

func TestManagerAddInstallsGlobalSkillAndClaudeFileMirror(t *testing.T) {
	manager, runner, root := testManager(t, []map[string]string{{
		"SKILL.md":           "---\nname: unslop\ndescription: test\n---\n",
		"references/help.md": "help\n",
	}})

	if err := manager.Add("unslop", "https://github.com/poteto/plugins"); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(runner.commands))
	}
	command := runner.commands[0]
	wantArguments := []string{"--yes", npmPackage, "add"}
	if !slices.Equal(command.Arguments[:3], wantArguments) || !slices.Equal(command.Arguments[4:], []string{
		"--skill", "unslop", "--global", "--agent", "codex", "--yes",
	}) {
		t.Errorf("arguments = %q, want staged source with the standard add options", command.Arguments)
	}
	if got := filepath.Base(command.Arguments[3]); got != "source" {
		t.Errorf("source directory = %q, want staged source", command.Arguments[3])
	}
	home, _ := environmentValue(command.Environment, "HOME")
	if home == manager.ManagedHome || filepath.Dir(filepath.Dir(home)) != filepath.Dir(manager.ManagedHome) {
		t.Errorf("HOME = %q, want an isolated staging home next to %q", home, manager.ManagedHome)
	}
	stateHome, _ := environmentValue(command.Environment, "XDG_STATE_HOME")
	if stateHome != filepath.Join(home, ".local", "state") {
		t.Errorf("XDG_STATE_HOME = %q", stateHome)
	}

	catalog, err := Load(manager.CatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Skills) != 1 || catalog.Skills[0].Name != "unslop" || catalog.Skills[0].Digest == "" {
		t.Fatalf("catalog = %#v", catalog)
	}
	for _, relative := range []string{"SKILL.md", "references/help.md"} {
		canonical := filepath.Join(manager.ManagedHome, ".agents", "skills", "unslop", filepath.FromSlash(relative))
		compatibility := filepath.Join(manager.ManagedHome, ".claude", "skills", "unslop", filepath.FromSlash(relative))
		destination, err := os.Readlink(compatibility)
		if err != nil {
			t.Fatalf("Readlink(%s) error = %v", compatibility, err)
		}
		want, err := filepath.Rel(filepath.Dir(compatibility), canonical)
		if err != nil {
			t.Fatal(err)
		}
		if destination != want {
			t.Errorf("destination = %q, want %q", destination, want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "skills.toml")); err != nil {
		t.Fatal(err)
	}
}

func TestManagerAddInstallsCompanionAgents(t *testing.T) {
	manager, runner, _ := testManager(t, []map[string]string{{
		"SKILL.md": "---\nname: thermos\ndescription: test\n---\nUse reviewer and quality.",
	}})
	runner.agentVersions = []map[string]string{{
		"reviewer.md":       "---\nname: reviewer\n---\nreview",
		"nested/quality.md": "---\nname: quality\n---\nquality",
	}}

	if err := manager.Add("thermos", "https://github.com/poteto/plugins"); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	for relative, want := range runner.agentVersions[0] {
		canonical := filepath.Join(manager.ManagedHome, ".agents", "skills", "thermos", managedAgentsDirectory, filepath.FromSlash(relative))
		contents, err := os.ReadFile(canonical)
		if err != nil || string(contents) != want {
			t.Errorf("agent %s = %q, %v", relative, contents, err)
		}
		link := managedAgentPath(manager.ManagedHome, filepath.FromSlash(relative))
		destination, err := os.Readlink(link)
		if err != nil {
			t.Fatalf("Readlink(%s) error = %v", link, err)
		}
		expected, err := filepath.Rel(filepath.Dir(link), canonical)
		if err != nil {
			t.Fatal(err)
		}
		if destination != expected {
			t.Errorf("agent destination = %q, want %q", destination, expected)
		}
	}
}

func TestManagerAddIgnoresUnreferencedPluginAgents(t *testing.T) {
	manager, runner, _ := testManager(t, []map[string]string{{"SKILL.md": "Use reviewer."}})
	runner.agentVersions = []map[string]string{{
		"reviewer.md":  "reviewer",
		"unrelated.md": "unrelated",
	}}

	if err := manager.Add("thermos", "https://github.com/poteto/plugins"); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := os.Stat(managedAgentPath(manager.ManagedHome, "reviewer.md")); err != nil {
		t.Errorf("referenced agent missing: %v", err)
	}
	if _, err := os.Lstat(managedAgentPath(manager.ManagedHome, "unrelated.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unreferenced agent installed: %v", err)
	}
}

func TestManagerUpdateReplacesCompanionAgents(t *testing.T) {
	manager, runner, _ := testManager(t, []map[string]string{
		{"SKILL.md": "Use old-reviewer."},
		{"SKILL.md": "Use new-reviewer."},
	})
	runner.agentVersions = []map[string]string{
		{"old-reviewer.md": "old agent"},
		{"new-reviewer.md": "new agent"},
	}
	if err := manager.Add("thermos", "https://github.com/poteto/plugins"); err != nil {
		t.Fatal(err)
	}

	if err := manager.Update("thermos"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	old := managedAgentPath(manager.ManagedHome, "old-reviewer.md")
	if _, err := os.Lstat(old); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old agent remains: %v", err)
	}
	updated := managedAgentPath(manager.ManagedHome, "new-reviewer.md")
	contents, err := os.ReadFile(updated)
	if err != nil || string(contents) != "new agent" {
		t.Errorf("updated agent = %q, %v", contents, err)
	}
}

func TestManagerAddRefusesUnownedCompanionAgent(t *testing.T) {
	manager, runner, _ := testManager(t, []map[string]string{{"SKILL.md": "Use reviewer."}})
	runner.agentVersions = []map[string]string{{"reviewer.md": "managed agent"}}
	path := managedAgentPath(manager.ManagedHome, "reviewer.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("user agent"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := manager.Add("thermos", "https://github.com/poteto/plugins")
	if err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("Add() error = %v, want ownership error", err)
	}
	contents, readErr := os.ReadFile(path)
	if readErr != nil || string(contents) != "user agent" {
		t.Errorf("unowned agent changed: %q, %v", contents, readErr)
	}
}

func TestManagerUpdateRefusesLocallyModifiedCompanionAgent(t *testing.T) {
	manager, runner, _ := testManager(t, []map[string]string{
		{"SKILL.md": "Use reviewer. Old."},
		{"SKILL.md": "Use reviewer. New."},
	})
	runner.agentVersions = []map[string]string{
		{"reviewer.md": "old agent"},
		{"reviewer.md": "new agent"},
	}
	if err := manager.Add("thermos", "https://github.com/poteto/plugins"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedAgentPath(manager.ManagedHome, "reviewer.md"), []byte("local edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := manager.Update("thermos")
	if err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("Update() error = %v, want local changes error", err)
	}
	if len(runner.commands) != 1 {
		t.Errorf("npx command count = %d, want only initial add", len(runner.commands))
	}
}

func TestManagerReportsEmptyUpdateAsSuccess(t *testing.T) {
	var gotKind status.Kind
	var gotText string
	reporter := status.ReporterFunc(func(kind status.Kind, text string) error {
		gotKind = kind
		gotText = text
		return nil
	})
	manager := Manager{
		CatalogPath: filepath.Join(t.TempDir(), "skills.toml"),
		Reporter:    reporter,
	}

	if err := manager.Update(""); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if gotKind != status.Success || gotText != "No skills to update" {
		t.Errorf("reported (%d, %q)", gotKind, gotText)
	}
}

func TestManagerReportsVerboseNpxCommand(t *testing.T) {
	var gotKind status.Kind
	var gotText string
	reporter := status.ReporterFunc(func(kind status.Kind, text string) error {
		gotKind = kind
		gotText = text
		return nil
	})
	manager := Manager{
		CatalogPath: filepath.Join(t.TempDir(), "skills.toml"),
		ManagedHome: t.TempDir(),
		Verbose:     true,
		Runner:      noopRunner{},
		Reporter:    reporter,
	}

	if err := manager.runAdd("example", "https://github.com/example/skills", manager.ManagedHome); err != nil {
		t.Fatalf("runAdd() error = %v", err)
	}
	want := "npx --yes " + npmPackage + " add https://github.com/example/skills --skill example --global --agent codex --yes"
	if gotKind != status.Command || gotText != want {
		t.Errorf("reported (%d, %q)", gotKind, gotText)
	}
}

func TestManagerAddRefusesUnownedDestination(t *testing.T) {
	manager, runner, _ := testManager(t, []map[string]string{{"SKILL.md": "new"}})
	path := filepath.Join(manager.ManagedHome, ".agents", "skills", "unslop", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("user data"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := manager.Add("unslop", "https://github.com/poteto/plugins")
	if err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("Add() error = %v, want ownership error", err)
	}
	if len(runner.commands) != 0 {
		t.Errorf("npx was invoked %d times", len(runner.commands))
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "user data" {
		t.Errorf("unowned file changed: %q, %v", data, err)
	}
}

func TestManagerAddRefusesManagedParentSymlinkOutsideHome(t *testing.T) {
	manager, runner, root := testManager(t, []map[string]string{{"SKILL.md": "new"}})
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(filepath.Join(manager.ManagedHome, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(manager.ManagedHome, ".agents", "skills")); err != nil {
		t.Fatal(err)
	}

	err := manager.Add("unslop", "https://github.com/poteto/plugins")
	if err == nil || !strings.Contains(err.Error(), "outside the managed home") {
		t.Fatalf("Add() error = %v, want managed path error", err)
	}
	if len(runner.commands) != 0 {
		t.Errorf("npx was invoked %d times", len(runner.commands))
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Errorf("outside directory contains %d entries, want none", len(entries))
	}
}

func TestManagerAddRollsBackInvalidInstall(t *testing.T) {
	manager, runner, _ := testManager(t, []map[string]string{{"README.md": "invalid"}})

	err := manager.Add("unslop", "https://github.com/poteto/plugins")
	if err == nil || !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("Add() error = %v, want invalid skill error", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %d, want one staged add", len(runner.commands))
	}
	for _, path := range []string{
		filepath.Join(manager.ManagedHome, ".agents", "skills", "unslop"),
		filepath.Join(manager.ManagedHome, ".claude", "skills", "unslop"),
	} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("incomplete path %s remains: %v", path, statErr)
		}
	}
	catalog, loadErr := Load(manager.CatalogPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(catalog.Skills) != 0 {
		t.Errorf("catalog = %#v, want empty", catalog)
	}
}

func TestManagerUpdateReplacesOwnedSkillAndRefreshesMirror(t *testing.T) {
	manager, _, _ := testManager(t, []map[string]string{
		{"SKILL.md": "old", "references/old.md": "old"},
		{"SKILL.md": "new", "references/new.md": "new"},
	})
	if err := manager.Add("unslop", "https://github.com/poteto/plugins"); err != nil {
		t.Fatal(err)
	}

	if err := manager.Update("unslop"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	canonical := filepath.Join(manager.ManagedHome, ".agents", "skills", "unslop")
	data, err := os.ReadFile(filepath.Join(canonical, "SKILL.md"))
	if err != nil || string(data) != "new" {
		t.Errorf("updated skill = %q, %v", data, err)
	}
	compatibility := filepath.Join(manager.ManagedHome, ".claude", "skills", "unslop")
	if _, err := os.Lstat(filepath.Join(compatibility, "references", "old.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale compatibility link remains: %v", err)
	}
	if _, err := os.Readlink(filepath.Join(compatibility, "references", "new.md")); err != nil {
		t.Errorf("new compatibility link missing: %v", err)
	}
}

func TestManagerUpdateRefusesLocallyModifiedSkill(t *testing.T) {
	manager, runner, _ := testManager(t, []map[string]string{
		{"SKILL.md": "old"},
		{"SKILL.md": "new"},
	})
	if err := manager.Add("unslop", "https://github.com/poteto/plugins"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(manager.ManagedHome, ".agents", "skills", "unslop", "SKILL.md")
	if err := os.WriteFile(path, []byte("local edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := manager.Update("unslop"); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("Update() error = %v, want local changes error", err)
	}
	if len(runner.commands) != 1 {
		t.Errorf("npx command count = %d, want only initial add", len(runner.commands))
	}
}

func TestManagerUpdateRefusesClaudeDirectorySymlink(t *testing.T) {
	manager, runner, _ := testManager(t, []map[string]string{
		{"SKILL.md": "old", "references/help.md": "help"},
		{"SKILL.md": "new"},
	})
	if err := manager.Add("unslop", "https://github.com/poteto/plugins"); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(manager.ManagedHome, ".agents", "skills", "unslop")
	compatibility := filepath.Join(manager.ManagedHome, ".claude", "skills", "unslop")
	compatibilityReferences := filepath.Join(compatibility, "references")
	if err := os.RemoveAll(compatibilityReferences); err != nil {
		t.Fatal(err)
	}
	destination, err := filepath.Rel(compatibility, filepath.Join(canonical, "references"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(destination, compatibilityReferences); err != nil {
		t.Fatal(err)
	}

	if err := manager.Update("unslop"); err == nil || !strings.Contains(err.Error(), "individual skill file") {
		t.Fatalf("Update() error = %v, want directory symlink ownership error", err)
	}
	if len(runner.commands) != 1 {
		t.Errorf("npx command count = %d, want only initial add", len(runner.commands))
	}
}

func TestManagerUpdateRestoresSkillWhenNpxFails(t *testing.T) {
	manager, runner, _ := testManager(t, []map[string]string{{"SKILL.md": "old"}})
	if err := manager.Add("unslop", "https://github.com/poteto/plugins"); err != nil {
		t.Fatal(err)
	}
	runner.failAt = 2

	if err := manager.Update("unslop"); err == nil {
		t.Fatal("Update() error = nil, want npx failure")
	}
	path := filepath.Join(manager.ManagedHome, ".agents", "skills", "unslop", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "old" {
		t.Errorf("restored skill = %q, %v", data, err)
	}
}

func TestManagerUpdateValidatesCandidateBeforeReplacingLiveSkill(t *testing.T) {
	manager, _, _ := testManager(t, []map[string]string{
		{"SKILL.md": "old"},
		{"README.md": "invalid"},
	})
	if err := manager.Add("unslop", "https://github.com/poteto/plugins"); err != nil {
		t.Fatal(err)
	}

	err := manager.Update("unslop")
	if err == nil || !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("Update() error = %v, want candidate validation error", err)
	}
	path := filepath.Join(manager.ManagedHome, ".agents", "skills", "unslop", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "old" {
		t.Errorf("live skill changed before candidate validation: %q, %v", data, err)
	}
}

func TestManagerRemoveUsesTrackedOwnership(t *testing.T) {
	manager, runner, _ := testManager(t, []map[string]string{{"SKILL.md": "managed"}})
	if err := manager.Add("unslop", "https://github.com/poteto/plugins"); err != nil {
		t.Fatal(err)
	}

	if err := manager.Remove("unslop"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %d, want only the initial staged add", len(runner.commands))
	}
	for _, path := range []string{
		filepath.Join(manager.ManagedHome, ".agents", "skills", "unslop"),
		filepath.Join(manager.ManagedHome, ".claude", "skills", "unslop"),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("removed path %s still exists: %v", path, err)
		}
	}
	catalog, err := Load(manager.CatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Skills) != 0 {
		t.Errorf("catalog = %#v, want empty", catalog)
	}
}

func TestManagerRemoveDeletesCompanionAgents(t *testing.T) {
	manager, runner, _ := testManager(t, []map[string]string{{"SKILL.md": "Use reviewer."}})
	runner.agentVersions = []map[string]string{{"reviewer.md": "managed agent"}}
	if err := manager.Add("thermos", "https://github.com/poteto/plugins"); err != nil {
		t.Fatal(err)
	}

	if err := manager.Remove("thermos"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Lstat(managedAgentPath(manager.ManagedHome, "reviewer.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("removed companion agent remains: %v", err)
	}
}

func TestManagerAddResolvesRelativeSourceFromCallerDirectory(t *testing.T) {
	manager, runner, root := testManager(t, []map[string]string{{"SKILL.md": "managed"}})
	manager.SourceDirectory = filepath.Join(root, "project")
	if err := manager.Add("unslop", "./skills/unslop"); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	want := filepath.Join(manager.SourceDirectory, "skills", "unslop")
	if got := runner.commands[0].Arguments[3]; got != want {
		t.Errorf("source argument = %q, want %q", got, want)
	}
	catalog, err := Load(manager.CatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := catalog.Skills[0].Source; got != want {
		t.Errorf("catalog source = %q, want %q", got, want)
	}
}

func testManager(t *testing.T, versions []map[string]string) (Manager, *fakeRunner, string) {
	t.Helper()
	root := t.TempDir()
	managedHome := filepath.Join(root, "files", "home")
	if err := os.MkdirAll(managedHome, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{versions: versions}
	var output bytes.Buffer
	return Manager{
		CatalogPath:       filepath.Join(root, "skills.toml"),
		ManagedHome:       managedHome,
		Stdout:            &output,
		Stderr:            &output,
		Runner:            runner,
		RepositoryFetcher: runner,
	}, runner, root
}

func argumentAfter(arguments []string, option string) string {
	for index := range arguments {
		if arguments[index] == option && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	return ""
}
