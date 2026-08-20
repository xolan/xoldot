package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindCompanionAgentsDisambiguatesDuplicateSkillsByReferencedAgent(t *testing.T) {
	root := t.TempDir()
	name := "thermo-nuclear-code-quality-review"
	skill := []byte("---\nname: " + name + "\n---\nUse this skill for a strict code quality review.")
	installed := filepath.Join(root, "installed")
	writeTestFile(t, filepath.Join(installed, "SKILL.md"), skill)

	writeTestFile(t, filepath.Join(root, "cursor-team-kit", "skills", name, "SKILL.md"), skill)
	writeTestFile(t, filepath.Join(root, "cursor-team-kit", "agents", name+".md"), []byte("---\nname: "+name+"\n---\n"))
	writeTestFile(t, filepath.Join(root, "thermos", "skills", name, "SKILL.md"), skill)
	writeTestFile(t, filepath.Join(root, "thermos", "agents", name+"-subagent.md"), []byte("---\nname: "+name+"-subagent\n---\n"))

	agents, err := findCompanionAgents(root, installed, name)
	if err != nil {
		t.Fatalf("findCompanionAgents() error = %v", err)
	}
	want := filepath.Join(root, "cursor-team-kit", "agents")
	if agents != want {
		t.Errorf("findCompanionAgents() = %q, want %q", agents, want)
	}
}

func writeTestFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}
