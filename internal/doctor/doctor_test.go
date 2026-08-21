package doctor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/gitops"
)

const testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestCheckAggregatesConfigurationFileFailuresInStableOrder(t *testing.T) {
	paths, _ := newFixture(t)
	writeFile(t, paths.Config, []byte("unknown = true\n"))
	writeFile(t, paths.Tools, []byte("[[tool]]\nname = [\n"))
	writeFile(t, paths.Aliases, []byte("[[alias]]\nname = \"1bad\"\ncommand = \"true\"\n"))
	writeFile(t, paths.Skills, []byte("[[skill]]\nname = \"bad_name\"\nsource = \"local\"\ndigest = \"bad\"\n"))
	profile := filepath.Join(paths.Profiles, "broken.toml")
	writeFile(t, profile, []byte("extends = [\n"))

	report := check(paths, unusedRuntime(t))
	if got, want := report.ErrorCount(), 5; got != want {
		t.Fatalf("ErrorCount() = %d, want %d\n%+v", got, want, report.Findings())
	}
	findings := report.Findings()
	for index, wantPath := range []string{paths.Config, paths.Tools, paths.Aliases, paths.Skills, profile} {
		if findings[index].Severity != Error || !strings.Contains(findings[index].Message, wantPath) {
			t.Errorf("finding[%d] = %+v, want error for %s", index, findings[index], wantPath)
		}
		if findings[index].Remedy == "" {
			t.Errorf("finding[%d] has no remedy", index)
		}
	}
	assertSeverityOrder(t, findings)
}

func TestCheckReportsConfigurationPathContainmentAndSymlinkEscapes(t *testing.T) {
	paths, _ := newFixture(t)
	outside := filepath.Join(t.TempDir(), "outside-tools.toml")
	writeFile(t, outside, nil)
	if err := os.Remove(paths.Tools); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, paths.Tools); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.TargetHomeEnv, filepath.Join(paths.Root, "target-home"))

	report := check(paths, unusedRuntime(t))
	for _, want := range []string{"does not resolve beneath", "Target home", "alias output"} {
		if !reportContains(report, Error, want) {
			t.Errorf("report does not contain path error %q: %+v", want, report.Findings())
		}
	}
}

func TestCheckReportsLifecycleScriptsPathEscape(t *testing.T) {
	paths, _ := newFixture(t)
	if err := os.RemoveAll(paths.Scripts); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, paths.Scripts); err != nil {
		t.Fatal(err)
	}

	report := check(paths, unusedRuntime(t))
	if !reportContains(report, Error, paths.Scripts) || !reportContains(report, Error, "does not resolve beneath") {
		t.Fatalf("report = %+v", report.Findings())
	}
}

func TestCheckRejectsTargetHomeThatIsNotADirectory(t *testing.T) {
	paths, home := newFixture(t)
	if err := os.Remove(home); err != nil {
		t.Fatal(err)
	}
	writeFile(t, home, []byte("not a directory\n"))

	report := check(paths, unusedRuntime(t))
	if !reportContains(report, Error, "Target home") || !reportContains(report, Error, "not a directory") {
		t.Fatalf("report = %+v", report.Findings())
	}
}

func TestCheckReportsManagedTargetEscape(t *testing.T) {
	paths, home := newFixture(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(paths.ManagedHome, ".config", "probe"), []byte("managed"))
	if err := os.Symlink(outside, filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}

	report := check(paths, unusedRuntime(t))
	if !reportContains(report, Error, "outside the target home") {
		t.Fatalf("report = %+v", report.Findings())
	}
}

func TestCheckReportsInvalidManagedLinkLedger(t *testing.T) {
	paths, home := newFixture(t)
	ledger := filepath.Join(home, ".local", "state", "xoldot", "links.json")
	writeFile(t, ledger, []byte(`{"version":99,"links":[]}`))

	report := check(paths, unusedRuntime(t))
	if !reportContains(report, Error, "unsupported managed link state version 99") {
		t.Fatalf("report = %+v", report.Findings())
	}
	finding := findContaining(t, report, "unsupported managed link state")
	if !strings.Contains(finding.Remedy, "apply --only managed-home") {
		t.Errorf("remedy = %q", finding.Remedy)
	}
}

func TestCheckReportsUnsupportedAndDisabledShells(t *testing.T) {
	t.Run("unsupported", func(t *testing.T) {
		paths, _ := newFixture(t)
		t.Setenv("XOLDOT_SHELL", "nushell")
		report := check(paths, unusedRuntime(t))
		if !reportContains(report, Error, "unsupported shell") {
			t.Fatalf("report = %+v", report.Findings())
		}
	})

	t.Run("unsupported configured shell", func(t *testing.T) {
		paths, _ := newFixture(t)
		cfg := config.Default()
		cfg.Aliases.Shells = append(cfg.Aliases.Shells, "nushell")
		if err := config.Save(paths.Config, cfg); err != nil {
			t.Fatal(err)
		}
		report := check(paths, unusedRuntime(t))
		if !reportContains(report, Error, `unsupported configured shell "nushell"`) {
			t.Fatalf("report = %+v", report.Findings())
		}
	})

	t.Run("disabled", func(t *testing.T) {
		paths, _ := newFixture(t)
		cfg := config.Default()
		cfg.Aliases.Shells = []string{"zsh"}
		if err := config.Save(paths.Config, cfg); err != nil {
			t.Fatal(err)
		}
		report := check(paths, unusedRuntime(t))
		if !reportContains(report, Error, `detected shell "bash" is disabled`) {
			t.Fatalf("report = %+v", report.Findings())
		}
	})
}

func TestCheckRequiresGitForSyncOrGitBackedSkill(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(t *testing.T, paths config.Paths)
	}{
		{
			name: "Sync",
			configure: func(t *testing.T, paths config.Paths) {
				cfg := config.Default()
				cfg.Git.Enabled = true
				if err := config.Save(paths.Config, cfg); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "Git-backed Skill",
			configure: func(t *testing.T, paths config.Paths) {
				writeSkillCatalog(t, paths.Skills, "https://github.com/owner/repo")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths, _ := newFixture(t)
			test.configure(t, paths)
			commands := runtime{
				lookPath: func(name string) (string, error) {
					if name == "git" {
						return "", exec.ErrNotFound
					}
					return "/standin/" + name, nil
				},
				output: func(_ string, executable string, arguments ...string) (string, error) {
					if strings.HasSuffix(executable, "node") && slices.Equal(arguments, []string{"--version"}) {
						return "v24.1.0\n", nil
					}
					t.Fatalf("unexpected command: %s %q", executable, arguments)
					return "", nil
				},
			}
			report := check(paths, commands)
			if !reportContains(report, Error, "git is required") {
				t.Fatalf("report = %+v", report.Findings())
			}
		})
	}
}

func TestCheckValidatesNPXAndNodeVersionForSkills(t *testing.T) {
	t.Run("missing runtimes", func(t *testing.T) {
		paths, _ := newFixture(t)
		writeSkillCatalog(t, paths.Skills, t.TempDir())
		commands := runtime{
			lookPath: func(string) (string, error) { return "", exec.ErrNotFound },
			output:   func(string, string, ...string) (string, error) { t.Fatal("unexpected command"); return "", nil },
		}
		report := check(paths, commands)
		if !reportContains(report, Error, "npx is required") || !reportContains(report, Error, "Node.js is required") {
			t.Fatalf("report = %+v", report.Findings())
		}
	})

	t.Run("old Node.js", func(t *testing.T) {
		paths, _ := newFixture(t)
		writeSkillCatalog(t, paths.Skills, t.TempDir())
		commands := runtime{
			lookPath: func(name string) (string, error) { return "/standin/" + name, nil },
			output: func(_ string, executable string, arguments ...string) (string, error) {
				if executable != "/standin/node" || !slices.Equal(arguments, []string{"--version"}) {
					t.Fatalf("unexpected command: %s %q", executable, arguments)
				}
				return "v22.19.9\n", nil
			},
		}
		report := check(paths, commands)
		if !reportContains(report, Error, "does not meet the required minimum 22.20") {
			t.Fatalf("report = %+v", report.Findings())
		}
	})

	t.Run("current Node.js", func(t *testing.T) {
		paths, _ := newFixture(t)
		writeSkillCatalog(t, paths.Skills, t.TempDir())
		commands := runtime{
			lookPath: func(name string) (string, error) { return "/standin/" + name, nil },
			output:   func(string, string, ...string) (string, error) { return "v22.20.0\n", nil },
		}
		report := check(paths, commands)
		if report.ErrorCount() != 0 {
			t.Fatalf("report = %+v", report.Findings())
		}
	})
}

func TestCheckValidatesLocalGitRemoteAndBranchWithoutNetworkCommands(t *testing.T) {
	paths, _ := newFixture(t)
	cfg := config.Default()
	cfg.Git.Enabled = true
	cfg.Git.Remote = "upstream"
	cfg.Git.Branch = "trunk"
	if err := config.Save(paths.Config, cfg); err != nil {
		t.Fatal(err)
	}
	var gotRoot, gotRemote, gotBranch, gotExecutable string
	commands := runtime{
		lookPath: func(name string) (string, error) { return "/standin/" + name, nil },
		inspectGit: func(root, remote, branch, executable string) (gitops.LocalInspection, error) {
			gotRoot, gotRemote, gotBranch, gotExecutable = root, remote, branch, executable
			return gitops.LocalInspection{Repository: true}, nil
		},
	}

	report := check(paths, commands)
	if !reportContains(report, Error, `remote "upstream" does not exist`) || !reportContains(report, Error, `branch "trunk" does not exist`) {
		t.Fatalf("report = %+v", report.Findings())
	}
	if gotRoot != paths.Root || gotRemote != "upstream" || gotBranch != "trunk" || gotExecutable != "/standin/git" {
		t.Errorf("inspectGit() = %q, %q, %q, %q", gotRoot, gotRemote, gotBranch, gotExecutable)
	}
}

func TestCheckTreatsManagedHomeAndAliasConflictsAsWarnings(t *testing.T) {
	paths, home := newFixture(t)
	writeFile(t, filepath.Join(paths.ManagedHome, ".vimrc"), []byte("managed\n"))
	writeFile(t, filepath.Join(home, ".vimrc"), []byte("local\n"))
	aliasPath := filepath.Join(home, ".aliases", "alias.bash")
	writeFile(t, aliasPath, []byte("alias ll='ls -la'\n"))

	report := check(paths, unusedRuntime(t))
	if report.Err() != nil {
		t.Fatalf("Err() = %v, report = %+v", report.Err(), report.Findings())
	}
	if got, want := report.WarningCount(), 2; got != want {
		t.Fatalf("WarningCount() = %d, want %d", got, want)
	}
	for _, finding := range report.Findings()[:2] {
		if finding.Severity != Warning || finding.Remedy == "" {
			t.Errorf("warning = %+v", finding)
		}
	}
}

func TestCheckDoesNotRunToolChecksOrNPXAndDoesNotChangeFiles(t *testing.T) {
	paths, home := newFixture(t)
	marker := filepath.Join(t.TempDir(), "tool-check-ran")
	tools := fmt.Sprintf("[[tool]]\nname = \"unsafe\"\ncheck = \"touch %s\"\n", marker)
	writeFile(t, paths.Tools, []byte(tools))
	writeSkillCatalog(t, paths.Skills, t.TempDir())
	before := snapshot(t, filepath.Dir(paths.Root)) + snapshot(t, home)
	commands := runtime{
		lookPath: func(name string) (string, error) { return "/standin/" + name, nil },
		output: func(_ string, executable string, arguments ...string) (string, error) {
			if executable != "/standin/node" || !slices.Equal(arguments, []string{"--version"}) {
				t.Fatalf("Doctor invoked forbidden command: %s %q", executable, arguments)
			}
			return "v24.0.0\n", nil
		},
	}

	report := check(paths, commands)
	if report.ErrorCount() != 0 {
		t.Fatalf("report = %+v", report.Findings())
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Tool check ran: %v", err)
	}
	after := snapshot(t, filepath.Dir(paths.Root)) + snapshot(t, home)
	if after != before {
		t.Fatalf("Doctor changed files\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestNodeVersionAtLeast(t *testing.T) {
	for _, test := range []struct {
		actual string
		want   bool
	}{
		{actual: "v22.19.9", want: false},
		{actual: "v22.20.0", want: true},
		{actual: "23.0.0", want: true},
		{actual: "unexpected", want: false},
	} {
		if got := nodeVersionAtLeast(test.actual, "22.20"); got != test.want {
			t.Errorf("nodeVersionAtLeast(%q) = %t, want %t", test.actual, got, test.want)
		}
	}
}

func newFixture(t *testing.T) (config.Paths, string) {
	t.Helper()
	base := t.TempDir()
	paths := config.NewPaths(filepath.Join(base, "config"))
	home := filepath.Join(base, "home")
	if err := config.Initialize(paths); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.TargetHomeEnv, home)
	t.Setenv("XOLDOT_SHELL", "bash")
	return paths, home
}

func unusedRuntime(t *testing.T) runtime {
	t.Helper()
	return runtime{
		lookPath: func(name string) (string, error) {
			t.Fatalf("unexpected executable lookup: %s", name)
			return "", nil
		},
		output: func(_ string, executable string, arguments ...string) (string, error) {
			t.Fatalf("unexpected command: %s %q", executable, arguments)
			return "", nil
		},
		inspectGit: func(root, remote, branch, executable string) (gitops.LocalInspection, error) {
			t.Fatalf("unexpected Git inspection: %s %s %s %s", root, remote, branch, executable)
			return gitops.LocalInspection{}, nil
		},
	}
}

func writeSkillCatalog(t *testing.T, path, source string) {
	t.Helper()
	data := fmt.Sprintf("[[skill]]\nname = \"example\"\nsource = %q\ndigest = %q\n", source, testDigest)
	writeFile(t, path, []byte(data))
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func reportContains(report Report, severity Severity, text string) bool {
	for _, finding := range report.Findings() {
		if finding.Severity == severity && strings.Contains(finding.Message, text) {
			return true
		}
	}
	return false
}

func findContaining(t *testing.T, report Report, text string) Finding {
	t.Helper()
	for _, finding := range report.Findings() {
		if strings.Contains(finding.Message, text) {
			return finding
		}
	}
	t.Fatalf("no finding contains %q: %+v", text, report.Findings())
	return Finding{}
}

func assertSeverityOrder(t *testing.T, findings []Finding) {
	t.Helper()
	for index := 1; index < len(findings); index++ {
		if findings[index].Severity < findings[index-1].Severity {
			t.Errorf("findings are not ordered by severity: %+v", findings)
			return
		}
	}
}

func snapshot(t *testing.T, root string) string {
	t.Helper()
	var result strings.Builder
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&result, "%s %s\n", relative, info.Mode())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&result, "%q\n", data)
		} else if info.Mode()&os.ModeSymlink != 0 {
			destination, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(&result, "-> %s\n", destination)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.String()
}
