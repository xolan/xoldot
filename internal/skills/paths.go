package skills

import (
	"fmt"
	"path/filepath"

	"github.com/xolan/xoldot/internal/pathutil"
)

var (
	canonicalSkillsRoot     = filepath.Join(".agents", "skills")
	compatibilitySkillsRoot = filepath.Join(".claude", "skills")
	canonicalAgentsRoot     = filepath.Join(".agents", "agents")
	compatibilityAgentsRoot = filepath.Join(".claude", "agents")
	reservedManagedRoots    = [...]string{
		canonicalSkillsRoot,
		compatibilitySkillsRoot,
		canonicalAgentsRoot,
		compatibilityAgentsRoot,
	}
)

type ManagedHomePath struct {
	Relative  string
	Recursive bool
}

func ManagedHomePaths(managedHome string, skill Skill) ([]ManagedHomePath, error) {
	if len(skill.Agents) == 0 {
		agents, err := ownedAgents(skill, canonicalSkillPath(managedHome, skill.Name), managedHome)
		if err != nil {
			return nil, fmt.Errorf("resolve managed home paths for skill %q: %w", skill.Name, err)
		}
		skill.Agents = agents
	}
	paths := []ManagedHomePath{
		{Relative: filepath.Join(canonicalSkillsRoot, skill.Name), Recursive: true},
		{Relative: filepath.Join(compatibilitySkillsRoot, skill.Name), Recursive: true},
	}
	for _, agent := range skill.Agents {
		relative := filepath.FromSlash(agent)
		paths = append(paths,
			ManagedHomePath{Relative: filepath.Join(canonicalAgentsRoot, relative)},
			ManagedHomePath{Relative: filepath.Join(compatibilityAgentsRoot, relative)},
		)
	}
	return paths, nil
}

func IsReservedManagedHomeSelection(relative string) bool {
	for _, reserved := range reservedManagedRoots {
		if pathutil.Contains(reserved, relative) || pathutil.Contains(relative, reserved) {
			return true
		}
	}
	return false
}

func IsManagedSkillDirectory(relative string) bool {
	parent := filepath.Dir(relative)
	return parent == canonicalSkillsRoot || parent == compatibilitySkillsRoot
}

func canonicalSkillPath(managedHome, name string) string {
	return filepath.Join(managedHome, canonicalSkillsRoot, name)
}

func compatibilitySkillPath(managedHome, name string) string {
	return filepath.Join(managedHome, compatibilitySkillsRoot, name)
}

func canonicalAgentPath(managedHome, relative string) string {
	return filepath.Join(managedHome, canonicalAgentsRoot, filepath.FromSlash(relative))
}

func claudeAgentPath(managedHome, relative string) string {
	return filepath.Join(managedHome, compatibilityAgentsRoot, filepath.FromSlash(relative))
}

func canonicalAgentsPath(managedHome string) string {
	return filepath.Join(managedHome, canonicalAgentsRoot)
}
