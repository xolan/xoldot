package profiles

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/config"
)

func TestLoadResolvesDeterministicInheritedUnion(t *testing.T) {
	paths := profileFixture(t)
	writeProfile(t, paths, "base", `tools = ["git"]
aliases = ["ll"]
managed_home = [".config/base"]
`)
	writeProfile(t, paths, "shared", `tools = ["ripgrep"]
skills = ["thermos"]
`)
	writeProfile(t, paths, "desktop", `extends = ["base"]
aliases = ["gs"]
skills = ["unslop"]
`)
	writeProfile(t, paths, "work", `extends = ["shared", "desktop"]
managed_home = [".config/work/config.toml"]
`)
	writeProfile(t, paths, "work-reversed", `extends = ["desktop", "shared"]
managed_home = [".config/work/config.toml"]
`)

	selected, err := Load(paths, "WORK")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	reversed, err := Load(paths, "work-reversed")
	if err != nil {
		t.Fatalf("Load(reversed) error = %v", err)
	}

	if got, want := toolNames(selected), []string{"git", "ripgrep"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tools = %v, want %v", got, want)
	}
	if got, want := aliasNames(selected), []string{"ll", "gs"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Aliases = %v, want %v", got, want)
	}
	if got, want := skillNames(selected), []string{"unslop", "thermos"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Skills = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(toolNames(selected), toolNames(reversed)) ||
		!reflect.DeepEqual(aliasNames(selected), aliasNames(reversed)) ||
		!reflect.DeepEqual(skillNames(selected), skillNames(reversed)) {
		t.Errorf("parent order changed selection: selected = %#v, reversed = %#v", selected, reversed)
	}

	for _, relative := range []string{
		filepath.Join(".config", "base", "nested.toml"),
		filepath.Join(".config", "work", "config.toml"),
		filepath.Join(".agents", "skills", "unslop"),
		filepath.Join(".agents", "skills", "unslop", "SKILL.md"),
		filepath.Join(".claude", "skills", "thermos", "SKILL.md"),
		filepath.Join(".agents", "agents", "reviewer.md"),
		filepath.Join(".claude", "agents", "reviewer.md"),
		filepath.Join(".agents", "agents", "nested", "quality.md"),
	} {
		if !selected.ManagedHome.Includes(relative) {
			t.Errorf("ManagedHome.Includes(%q) = false", relative)
		}
		if !reversed.ManagedHome.Includes(relative) {
			t.Errorf("reversed ManagedHome.Includes(%q) = false", relative)
		}
	}
	for _, relative := range []string{
		filepath.Join(".config", "other.toml"),
		filepath.Join(".agents", "skills", "other", "SKILL.md"),
		filepath.Join(".agents", "agents", "unowned.md"),
	} {
		if selected.ManagedHome.Includes(relative) {
			t.Errorf("ManagedHome.Includes(%q) = true", relative)
		}
	}
}

func TestLoadRejectsInvalidProfiles(t *testing.T) {
	tests := []struct {
		name      string
		profiles  map[string]string
		wantError string
	}{
		{
			name: "missing parent",
			profiles: map[string]string{
				"selected": `extends = ["missing"]`,
			},
			wantError: "missing profile",
		},
		{
			name: "cycle",
			profiles: map[string]string{
				"selected": `extends = ["middle"]`,
				"middle":   `extends = ["selected"]`,
			},
			wantError: "inheritance cycle",
		},
		{
			name: "unknown Tool",
			profiles: map[string]string{
				"selected": `tools = ["missing"]`,
			},
			wantError: "unknown Tool",
		},
		{
			name: "unknown Alias",
			profiles: map[string]string{
				"selected": `aliases = ["missing"]`,
			},
			wantError: "unknown Alias",
		},
		{
			name: "unknown Skill",
			profiles: map[string]string{
				"selected": `skills = ["missing"]`,
			},
			wantError: "unknown Skill",
		},
		{
			name: "unknown managed home member",
			profiles: map[string]string{
				"selected": `managed_home = ["missing"]`,
			},
			wantError: "unknown managed home member",
		},
		{
			name: "unknown field",
			profiles: map[string]string{
				"selected": `hostname = "automatic"`,
			},
			wantError: "strict mode",
		},
		{
			name: "unused invalid profile",
			profiles: map[string]string{
				"selected": `tools = ["git"]`,
				"unused":   `aliases = ["missing"]`,
			},
			wantError: "unknown Alias",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths := profileFixture(t)
			for name, contents := range test.profiles {
				writeProfile(t, paths, name, contents)
			}
			if _, err := Load(paths, "selected"); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Load() error = %v, want it to contain %q", err, test.wantError)
			}
		})
	}
}

func TestValidateChecksEveryProfile(t *testing.T) {
	tests := []struct {
		name      string
		profiles  map[string]string
		wantError string
	}{
		{
			name: "missing parent",
			profiles: map[string]string{
				"unused": `extends = ["missing"]`,
			},
			wantError: "missing profile",
		},
		{
			name: "inheritance cycle",
			profiles: map[string]string{
				"unused": `extends = ["middle"]`,
				"middle": `extends = ["unused"]`,
			},
			wantError: "inheritance cycle",
		},
		{
			name: "unknown member",
			profiles: map[string]string{
				"unused": `tools = ["missing"]`,
			},
			wantError: "unknown Tool",
		},
		{
			name: "invalid managed path",
			profiles: map[string]string{
				"unused": `managed_home = ["../escape"]`,
			},
			wantError: "clean relative path",
		},
		{
			name: "unknown field",
			profiles: map[string]string{
				"unused": `hostname = "automatic"`,
			},
			wantError: "strict mode",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths := profileFixture(t)
			writeProfile(t, paths, "selected", `tools = ["git"]`)
			for name, contents := range test.profiles {
				writeProfile(t, paths, name, contents)
			}

			if err := Validate(paths); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Validate() error = %v, want it to contain %q", err, test.wantError)
			}
		})
	}
}

func TestValidateAcceptsValidProfiles(t *testing.T) {
	paths := profileFixture(t)
	writeProfile(t, paths, "base", `tools = ["git"]`)
	writeProfile(t, paths, "work", `extends = ["base"]
aliases = ["ll"]
managed_home = [".config/work/config.toml"]`)

	if err := Validate(paths); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsSymlinkedProfile(t *testing.T) {
	paths := profileFixture(t)
	target := filepath.Join(t.TempDir(), "target.toml")
	writeFile(t, target, `tools = ["git"]`)
	if err := os.Symlink(target, filepath.Join(paths.Profiles, "linked.toml")); err != nil {
		t.Fatal(err)
	}

	if err := Validate(paths); err == nil || !strings.Contains(err.Error(), "not an ordinary file") {
		t.Fatalf("Validate() error = %v, want ordinary file error", err)
	}
}

func TestLoadRejectsUnsafeManagedHomeMembers(t *testing.T) {
	for _, relative := range []string{
		"/absolute",
		"../escape",
		".config/../escape",
		".config//file",
		`.config\file`,
		".",
		".agents",
		".agents/skills",
		".agents/skills/unslop",
		".claude/agents/reviewer.md",
	} {
		t.Run(strings.ReplaceAll(relative, "/", "_"), func(t *testing.T) {
			paths := profileFixture(t)
			writeProfile(t, paths, "selected", `managed_home = ["`+strings.ReplaceAll(relative, `\`, `\\`)+`"]`)
			if _, err := Load(paths, "selected"); err == nil ||
				(!strings.Contains(err.Error(), "clean relative path") && !strings.Contains(err.Error(), "reserved")) {
				t.Fatalf("Load() error = %v, want path validation error", err)
			}
		})
	}
}

func TestLoadRejectsDuplicateNormalizedProfileNames(t *testing.T) {
	paths := profileFixture(t)
	writeProfile(t, paths, "Work", `tools = ["git"]`)
	writeProfile(t, paths, "work", `tools = ["git"]`)

	if _, err := Load(paths, "work"); err == nil || !strings.Contains(err.Error(), "after normalization") {
		t.Fatalf("Load() error = %v, want normalized duplicate error", err)
	}
}

func TestLoadRejectsManagedHomeSymlinkEscapes(t *testing.T) {
	t.Run("selected file escapes", func(t *testing.T) {
		paths := profileFixture(t)
		outside := filepath.Join(t.TempDir(), "outside")
		writeFile(t, outside, "outside")
		if err := os.Symlink(outside, filepath.Join(paths.ManagedHome, "escape")); err != nil {
			t.Fatal(err)
		}
		writeProfile(t, paths, "selected", `managed_home = ["escape"]`)

		if _, err := Load(paths, "selected"); err == nil || !strings.Contains(err.Error(), "escapes files/home") {
			t.Fatalf("Load() error = %v, want symlink escape error", err)
		}
	})

	t.Run("selected path traverses directory symlink", func(t *testing.T) {
		paths := profileFixture(t)
		destination := filepath.Join(paths.ManagedHome, "actual")
		writeFile(t, filepath.Join(destination, "file"), "managed")
		if err := os.Symlink(destination, filepath.Join(paths.ManagedHome, "alias")); err != nil {
			t.Fatal(err)
		}
		writeProfile(t, paths, "selected", `managed_home = ["alias/file"]`)

		if _, err := Load(paths, "selected"); err == nil || !strings.Contains(err.Error(), "directory symlink") {
			t.Fatalf("Load() error = %v, want directory symlink error", err)
		}
	})
}

func TestLoadAllowsProfileWithoutManagedHomeDirectory(t *testing.T) {
	paths := profileFixture(t)
	if err := os.RemoveAll(paths.ManagedHome); err != nil {
		t.Fatal(err)
	}
	writeProfile(t, paths, "selected", `tools = ["git"]`)

	selected, err := Load(paths, "selected")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := toolNames(selected), []string{"git"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tools = %v, want %v", got, want)
	}
}

func profileFixture(t *testing.T) config.Paths {
	t.Helper()
	paths := config.NewPaths(t.TempDir())
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	writeFile(t, paths.Tools, `[[tool]]
name = "git"
check = "command -v git"

[[tool]]
name = "ripgrep"
check = "command -v rg"

[[tool]]
name = "jq"
check = "command -v jq"
`)
	writeFile(t, paths.Aliases, `[[alias]]
name = "ll"
command = "ls -la"

[[alias]]
name = "gs"
command = "git status"

[[alias]]
name = "unused"
command = "true"
`)
	writeFile(t, paths.Skills, `[[skill]]
name = "unslop"
source = "owner/repo"
digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
agents = ["reviewer.md"]

[[skill]]
name = "thermos"
source = "owner/repo"
digest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
agents = ["nested/quality.md"]

[[skill]]
name = "other"
source = "owner/repo"
digest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
`)
	writeFile(t, filepath.Join(paths.ManagedHome, ".config", "base", "nested.toml"), "base")
	writeFile(t, filepath.Join(paths.ManagedHome, ".config", "work", "config.toml"), "work")
	return paths
}

func writeProfile(t *testing.T, paths config.Paths, name, contents string) {
	t.Helper()
	writeFile(t, filepath.Join(paths.Profiles, name+".toml"), contents+"\n")
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func toolNames(configuration Configuration) []string {
	names := make([]string, 0, len(configuration.Tools.Tools))
	for _, tool := range configuration.Tools.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func aliasNames(configuration Configuration) []string {
	names := make([]string, 0, len(configuration.Aliases.Aliases))
	for _, alias := range configuration.Aliases.Aliases {
		names = append(names, alias.Name)
	}
	return names
}

func skillNames(configuration Configuration) []string {
	names := make([]string, 0, len(configuration.Skills.Skills))
	for _, skill := range configuration.Skills.Skills {
		names = append(names, skill.Name)
	}
	return names
}
