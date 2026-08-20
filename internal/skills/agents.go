package skills

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/xolan/xoldot/internal/pathutil"
	reportstatus "github.com/xolan/xoldot/internal/status"
	"github.com/xolan/xoldot/internal/urlutil"
)

const managedAgentsDirectory = ".xoldot-agents"

type RepositoryRequest struct {
	Name        string
	Source      string
	Destination string
	Environment []string
	Stdout      io.Writer
	Stderr      io.Writer
}

type RepositoryFetcher interface {
	Fetch(RepositoryRequest) error
}

type GitRepositoryFetcher struct{}

func (GitRepositoryFetcher) Fetch(request RepositoryRequest) error {
	source := strings.TrimPrefix(request.Source, "git+")
	command := exec.Command("git", "clone", "--quiet", "--depth", "1", "--", source, request.Destination)
	command.Env = request.Environment
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	if err := command.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("git is required to install companion agents: %w", err)
		}
		return fmt.Errorf("git clone %s: %w", urlutil.RedactForDisplay(request.Source), err)
	}
	return nil
}

func (manager Manager) stageAgents(candidate *stagedSkill, name, sourceRoot string) error {
	if sourceRoot == "" {
		return nil
	}
	agentsRoot, err := findCompanionAgents(sourceRoot, candidate.canonical, name)
	if err != nil || agentsRoot == "" {
		return err
	}
	return copyCompanionAgents(candidate, agentsRoot)
}

func (manager Manager) agentSourceRoot(stageRoot, name, source string) (string, error) {
	info, err := os.Stat(source)
	if err == nil {
		if info.IsDir() {
			return source, nil
		}
		return "", nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect skill source for companion agents: %w", err)
	}
	if !isCloneableRepository(source) {
		return "", nil
	}

	destination := filepath.Join(stageRoot, "source")
	environment, err := redirectedEnvironment(filepath.Join(stageRoot, "home"))
	if err != nil {
		return "", err
	}
	if manager.Verbose {
		if err := manager.reportf(reportstatus.Command, "git clone --quiet --depth 1 -- %s %s", urlutil.RedactForDisplay(source), destination); err != nil {
			return "", err
		}
	}
	fetcher := manager.RepositoryFetcher
	if fetcher == nil {
		fetcher = GitRepositoryFetcher{}
	}
	if err := fetcher.Fetch(RepositoryRequest{
		Name:        name,
		Source:      source,
		Destination: destination,
		Environment: environment,
		Stdout:      manager.Stdout,
		Stderr:      manager.Stderr,
	}); err != nil {
		return "", err
	}
	return destination, nil
}

func isCloneableRepository(source string) bool {
	if strings.HasPrefix(source, "git@") || strings.HasPrefix(source, "ssh://") || strings.HasPrefix(source, "git://") {
		return true
	}
	parsed, err := url.Parse(source)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "git+http" && parsed.Scheme != "git+https") {
		return false
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	if strings.HasSuffix(path, ".git") {
		return true
	}
	if parsed.Host != "github.com" && parsed.Host != "gitlab.com" && parsed.Host != "bitbucket.org" {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func findCompanionAgents(sourceRoot, installedSkill, name string) (string, error) {
	installed, err := os.ReadFile(filepath.Join(installedSkill, "SKILL.md"))
	if err != nil {
		return "", fmt.Errorf("read installed skill while finding companion agents: %w", err)
	}
	type match struct {
		skill  string
		agents string
	}
	var matches []match
	err = filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() || entry.Name() != "SKILL.md" || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(contents, installed) {
			return nil
		}
		agents := nearestAgentsDirectory(filepath.Dir(path), sourceRoot)
		if agents != "" {
			matches = append(matches, match{skill: filepath.Dir(path), agents: agents})
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("find companion agents: %w", err)
	}
	if len(matches) == 0 {
		return "", nil
	}
	if len(matches) == 1 {
		return matches[0].agents, nil
	}
	var named []match
	for _, candidate := range matches {
		if filepath.Base(candidate.skill) == name {
			named = append(named, candidate)
		}
	}
	if len(named) == 1 {
		return named[0].agents, nil
	}
	return "", fmt.Errorf("skill %q matches more than one source directory with companion agents", name)
}

func nearestAgentsDirectory(skillDirectory, sourceRoot string) string {
	for directory := skillDirectory; ; directory = filepath.Dir(directory) {
		candidate := filepath.Join(directory, "agents")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		if directory == sourceRoot {
			return ""
		}
		parent := filepath.Dir(directory)
		if parent == directory || !pathutil.Contains(sourceRoot, parent) {
			return ""
		}
	}
}

func copyCompanionAgents(candidate *stagedSkill, sourceRoot string) error {
	skill, err := os.ReadFile(filepath.Join(candidate.canonical, "SKILL.md"))
	if err != nil {
		return fmt.Errorf("read installed skill while selecting companion agents: %w", err)
	}
	destinationRoot := filepath.Join(candidate.canonical, managedAgentsDirectory)
	return filepath.WalkDir(sourceRoot, func(source string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("companion agents contain unsupported symlink %s", source)
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(sourceRoot, source)
		if err != nil {
			return err
		}
		contents, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if !referencesAgent(skill, contents, relative) {
			return nil
		}
		destination := filepath.Join(destinationRoot, relative)
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(destination, contents, info.Mode().Perm()); err != nil {
			return fmt.Errorf("write companion agent %s: %w", destination, err)
		}
		link := filepath.Join(candidate.root, "home", ".claude", "agents", relative)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return err
		}
		linkDestination, err := filepath.Rel(filepath.Dir(link), destination)
		if err != nil {
			return err
		}
		if err := os.Symlink(linkDestination, link); err != nil {
			return err
		}
		candidate.agents[relative] = link
		return nil
	})
}

func referencesAgent(skill, agent []byte, relative string) bool {
	name := strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative))
	for _, line := range strings.Split(string(agent), "\n") {
		key, value, found := strings.Cut(line, ":")
		if found && strings.TrimSpace(key) == "name" {
			value = strings.Trim(strings.TrimSpace(value), "\"'")
			if value != "" {
				name = value
			}
			break
		}
	}
	return bytes.Contains(skill, []byte(name))
}

func ownedAgents(canonical, managedHome string) ([]string, error) {
	root := filepath.Join(canonical, managedAgentsDirectory)
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect managed companion agents: %w", err)
	}
	var relatives []string
	var links []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("managed companion agent %s is not an ordinary file", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return fmt.Errorf("managed companion agent %s is not a Markdown file", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		link := managedAgentPath(managedHome, relative)
		links = append(links, link)
		relatives = append(relatives, relative)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := validateManagedPaths(managedHome, links...); err != nil {
		return nil, err
	}
	for index, link := range links {
		linkInfo, err := os.Lstat(link)
		if err != nil {
			return nil, fmt.Errorf("inspect Claude agent path %s: %w", link, err)
		}
		if linkInfo.Mode()&os.ModeSymlink == 0 {
			return nil, fmt.Errorf("claude agent path %s is not owned by xoldot", link)
		}
		destination, err := os.Readlink(link)
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(destination) {
			destination = filepath.Join(filepath.Dir(link), destination)
		}
		expected := filepath.Join(root, relatives[index])
		if filepath.Clean(destination) != expected {
			return nil, fmt.Errorf("claude agent symlink %s is not owned by xoldot", link)
		}
	}
	return relatives, nil
}

func (manager Manager) validateNewAgentPaths(candidate stagedSkill, previous []string) error {
	previousSet := make(map[string]struct{}, len(previous))
	for _, relative := range previous {
		previousSet[relative] = struct{}{}
	}
	paths := make([]string, 0, len(candidate.agents))
	for relative := range candidate.agents {
		path := managedAgentPath(manager.ManagedHome, relative)
		paths = append(paths, path)
		if _, replacing := previousSet[relative]; replacing {
			continue
		}
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("Claude agent path %s already exists but is not owned by this skill", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect Claude agent path %s: %w", path, err)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return validateManagedPaths(manager.ManagedHome, paths...)
}

func managedAgentPath(managedHome, relative string) string {
	return filepath.Join(managedHome, ".claude", "agents", relative)
}
