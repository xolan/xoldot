package skills

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xolan/xoldot/internal/pathutil"
	reportstatus "github.com/xolan/xoldot/internal/status"
	"github.com/xolan/xoldot/internal/urlutil"
)

const (
	npmPackage         = "skills@1.5.23"
	MinimumNodeVersion = "22.20"
)

type Command struct {
	Arguments   []string
	Directory   string
	Environment []string
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}

type Runner interface {
	Run(Command) error
}

type ExecRunner struct{}

func (ExecRunner) Run(request Command) error {
	command := exec.Command("npx", request.Arguments...)
	command.Dir = request.Directory
	command.Env = request.Environment
	command.Stdin = request.Stdin
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	if err := command.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("npx is required; install Node.js %s or newer: %w", MinimumNodeVersion, err)
		}
		return fmt.Errorf("npx %s: %w", formatCommand(request.Arguments), err)
	}
	return nil
}

type Manager struct {
	CatalogPath       string
	ManagedHome       string
	SourceDirectory   string
	Stdin             io.Reader
	Stdout            io.Writer
	Stderr            io.Writer
	Verbose           bool
	Runner            Runner
	RepositoryFetcher RepositoryFetcher
	Reporter          reportstatus.Reporter
}

type InspectionState string

const (
	InspectionCurrent InspectionState = "current"
	InspectionProblem InspectionState = "problem"
)

type Inspection struct {
	Name    string
	State   InspectionState
	Problem string
}

func (manager Manager) Inspect() ([]Inspection, error) {
	catalog, err := Load(manager.CatalogPath)
	if err != nil {
		return nil, err
	}
	return manager.inspectCatalog(catalog)
}

func (manager Manager) InspectCatalog(catalog Catalog) ([]Inspection, error) {
	if err := Validate(catalog); err != nil {
		return nil, err
	}
	return manager.inspectCatalog(catalog)
}

func (manager Manager) inspectCatalog(catalog Catalog) ([]Inspection, error) {
	sort.Slice(catalog.Skills, func(i, j int) bool {
		return catalog.Skills[i].Name < catalog.Skills[j].Name
	})

	inspection := make([]Inspection, 0, len(catalog.Skills))
	for _, skill := range catalog.Skills {
		canonical := manager.canonicalPath(skill.Name)
		compatibility := manager.compatibilityPath(skill.Name)
		if err := validateManagedPaths(manager.ManagedHome, canonical, compatibility); err != nil {
			return nil, err
		}
		if _, err := os.Lstat(compatibility); errors.Is(err, os.ErrNotExist) {
			inspection = append(inspection, Inspection{
				Name:    skill.Name,
				State:   InspectionProblem,
				Problem: fmt.Sprintf("skill %q is missing Claude compatibility path %s", skill.Name, compatibility),
			})
			continue
		} else if err != nil {
			return nil, fmt.Errorf("inspect Claude compatibility path %s: %w", compatibility, err)
		}
		if _, err := verifyOwnedSkill(skill, canonical, compatibility, manager.ManagedHome); err != nil {
			var pathError *os.PathError
			if errors.As(err, &pathError) && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			inspection = append(inspection, Inspection{
				Name:    skill.Name,
				State:   InspectionProblem,
				Problem: err.Error(),
			})
			continue
		}
		inspection = append(inspection, Inspection{Name: skill.Name, State: InspectionCurrent})
	}
	return inspection, nil
}

func (manager Manager) Add(name, source string) error {
	if err := validateName(name); err != nil {
		return err
	}
	source, err := NormalizeSource(source, manager.sourceDirectory())
	if err != nil {
		return err
	}
	catalog, err := Load(manager.CatalogPath)
	if err != nil {
		return err
	}
	if _, exists := find(catalog, name); exists {
		return fmt.Errorf("skill %q already exists; use 'xoldot skill update %s'", name, name)
	}

	canonical := manager.canonicalPath(name)
	compatibility := manager.compatibilityPath(name)
	if err := validateManagedPaths(manager.ManagedHome, canonical, compatibility); err != nil {
		return err
	}
	if _, err := os.Lstat(canonical); err == nil {
		return fmt.Errorf("managed skill path %s already exists but is not owned by xoldot", canonical)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect managed skill %s: %w", canonical, err)
	}
	if _, err := os.Lstat(compatibility); err == nil {
		return fmt.Errorf("claude compatibility path %s already exists but is not owned by xoldot", compatibility)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Claude compatibility path %s: %w", compatibility, err)
	}

	candidate, err := manager.stageSkill(name, source)
	if err != nil {
		return err
	}
	if err := manager.validateNewAgentPaths(candidate, nil); err != nil {
		candidate.cleanup()
		return err
	}
	catalog.Skills = append(catalog.Skills, Skill{
		Name:   name,
		Source: source,
		Digest: candidate.digest,
		Agents: candidate.agentNames,
	})
	if err := replaceSkill(candidate, canonical, compatibility, manager.ManagedHome, nil, false, func() error {
		return Save(manager.CatalogPath, catalog)
	}); err != nil {
		return err
	}
	return manager.reportf(reportstatus.Success, "Added skill %s from %s", name, source)
}

func (manager Manager) Remove(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	catalog, err := Load(manager.CatalogPath)
	if err != nil {
		return err
	}
	index, exists := find(catalog, name)
	if !exists {
		return fmt.Errorf("skill %q is not owned by xoldot", name)
	}
	skill := catalog.Skills[index]
	canonical := manager.canonicalPath(name)
	compatibility := manager.compatibilityPath(name)
	if err := validateManagedPaths(manager.ManagedHome, canonical, compatibility); err != nil {
		return err
	}
	agents, err := verifyOwnedSkill(skill, canonical, compatibility, manager.ManagedHome)
	if err != nil {
		return err
	}

	catalog.Skills = append(catalog.Skills[:index], catalog.Skills[index+1:]...)
	if err := removeSkill(canonical, compatibility, manager.ManagedHome, agents, func() error {
		return Save(manager.CatalogPath, catalog)
	}); err != nil {
		return err
	}
	return manager.reportf(reportstatus.Success, "Removed skill %s", name)
}

func (manager Manager) Update(name string) error {
	catalog, err := Load(manager.CatalogPath)
	if err != nil {
		return err
	}
	var indices []int
	if name != "" {
		if err := validateName(name); err != nil {
			return err
		}
		index, exists := find(catalog, name)
		if !exists {
			return fmt.Errorf("skill %q is not owned by xoldot", name)
		}
		indices = []int{index}
	} else {
		indices = make([]int, len(catalog.Skills))
		for index := range catalog.Skills {
			indices[index] = index
		}
		sort.Slice(indices, func(i, j int) bool {
			return catalog.Skills[indices[i]].Name < catalog.Skills[indices[j]].Name
		})
	}

	for _, index := range indices {
		skill := catalog.Skills[index]
		canonical := manager.canonicalPath(skill.Name)
		compatibility := manager.compatibilityPath(skill.Name)
		if err := validateManagedPaths(manager.ManagedHome, canonical, compatibility); err != nil {
			return err
		}
		agents, err := verifyOwnedSkill(skill, canonical, compatibility, manager.ManagedHome)
		if err != nil {
			return err
		}

		candidate, err := manager.stageSkill(skill.Name, skill.Source)
		if err != nil {
			return err
		}
		if err := manager.validateNewAgentPaths(candidate, agents); err != nil {
			candidate.cleanup()
			return err
		}
		if _, err := verifyOwnedSkill(skill, canonical, compatibility, manager.ManagedHome); err != nil {
			candidate.cleanup()
			return err
		}
		catalog.Skills[index].Digest = candidate.digest
		catalog.Skills[index].Agents = candidate.agentNames
		if err := replaceSkill(candidate, canonical, compatibility, manager.ManagedHome, agents, true, func() error {
			return Save(manager.CatalogPath, catalog)
		}); err != nil {
			return err
		}
		if err := manager.reportf(reportstatus.Success, "Updated skill %s", skill.Name); err != nil {
			return err
		}
	}
	if len(indices) == 0 {
		return manager.reportf(reportstatus.Success, "No skills to update")
	}
	return nil
}

func (manager Manager) runAdd(name, source, managedHome string) error {
	return manager.run(managedHome,
		"--yes", npmPackage, "add", source,
		"--skill", name, "--global", "--agent", "codex", "--yes",
	)
}

func (manager Manager) run(managedHome string, arguments ...string) error {
	environment, err := redirectedEnvironment(managedHome)
	if err != nil {
		return err
	}
	runner := manager.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	if manager.Verbose {
		if err := manager.reportf(reportstatus.Command, "npx %s", formatCommand(arguments)); err != nil {
			return fmt.Errorf("write skill command: %w", err)
		}
	}
	return runner.Run(Command{
		Arguments:   arguments,
		Directory:   filepath.Dir(manager.CatalogPath),
		Environment: environment,
		Stdin:       manager.Stdin,
		Stdout:      manager.Stdout,
		Stderr:      manager.Stderr,
	})
}

func (manager Manager) sourceDirectory() string {
	if manager.SourceDirectory != "" {
		return manager.SourceDirectory
	}
	directory, err := os.Getwd()
	if err != nil {
		return filepath.Dir(manager.CatalogPath)
	}
	return directory
}

func formatCommand(arguments []string) string {
	formatted := append([]string(nil), arguments...)
	for index, argument := range formatted {
		if argument == "add" && index+1 < len(formatted) {
			formatted[index+1] = urlutil.RedactForDisplay(formatted[index+1])
			break
		}
	}
	return strings.Join(formatted, " ")
}

func (manager Manager) canonicalPath(name string) string {
	return canonicalSkillPath(manager.ManagedHome, name)
}

func (manager Manager) compatibilityPath(name string) string {
	return compatibilitySkillPath(manager.ManagedHome, name)
}

func validateManagedPaths(managedHome string, paths ...string) error {
	resolvedHome, err := filepath.EvalSymlinks(managedHome)
	if err != nil {
		return fmt.Errorf("resolve managed home: %w", err)
	}
	for _, path := range paths {
		resolvedParent, err := pathutil.ResolveExistingPrefix(filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("resolve managed skill path %s: %w", path, err)
		}
		resolvedPath := filepath.Join(resolvedParent, filepath.Base(path))
		if !pathutil.Contains(resolvedHome, resolvedPath) {
			return fmt.Errorf("managed skill path %s resolves outside the managed home", path)
		}
	}
	return nil
}

func redirectedEnvironment(managedHome string) ([]string, error) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find real home directory: %w", err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("find user cache directory: %w", err)
	}
	environment := os.Environ()
	environment = setEnvironment(environment, "HOME", managedHome)
	environment = setEnvironment(environment, "XDG_STATE_HOME", filepath.Join(managedHome, ".local", "state"))
	environment = setEnvironment(environment, "NPM_CONFIG_CACHE", filepath.Join(cache, "xoldot", "npm"))
	environment = setEnvironment(environment, "DO_NOT_TRACK", "1")
	if _, exists := environmentValue(environment, "NPM_CONFIG_USERCONFIG"); !exists {
		npmConfig := filepath.Join(realHome, ".npmrc")
		if _, err := os.Stat(npmConfig); err == nil {
			environment = setEnvironment(environment, "NPM_CONFIG_USERCONFIG", npmConfig)
		}
	}
	if _, exists := environmentValue(environment, "GIT_CONFIG_GLOBAL"); !exists {
		gitConfig := filepath.Join(realHome, ".gitconfig")
		if _, err := os.Stat(gitConfig); err == nil {
			environment = setEnvironment(environment, "GIT_CONFIG_GLOBAL", gitConfig)
		}
	}
	if _, exists := environmentValue(environment, "GH_CONFIG_DIR"); !exists {
		configHome := os.Getenv("XDG_CONFIG_HOME")
		if configHome == "" {
			configHome = filepath.Join(realHome, ".config")
		}
		environment = setEnvironment(environment, "GH_CONFIG_DIR", filepath.Join(configHome, "gh"))
	}
	return environment, nil
}

func setEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	for index, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			environment[index] = prefix + value
			return environment
		}
	}
	return append(environment, prefix+value)
}

func environmentValue(environment []string, key string) (string, bool) {
	prefix := key + "="
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix), true
		}
	}
	return "", false
}

func verifyOwnedSkill(skill Skill, canonical, compatibility, managedHome string) ([]string, error) {
	digest, err := digestSkillWithAgents(canonical, managedHome, skill.Agents)
	if err != nil {
		return nil, err
	}
	if digest != skill.Digest {
		return nil, fmt.Errorf("skill %q has local changes; refusing to overwrite content xoldot cannot verify", skill.Name)
	}
	if err := validateCompatibilityMirror(canonical, compatibility); err != nil {
		return nil, err
	}
	return ownedAgents(skill, canonical, managedHome)
}

func digestSkillWithAgents(root, managedHome string, agents []string) (string, error) {
	skillDigest, err := digestSkill(root)
	if err != nil || len(agents) == 0 {
		return skillDigest, err
	}
	hash := sha256.New()
	if err := writeDigestField(hash, skillDigest); err != nil {
		return "", err
	}
	agents = append([]string(nil), agents...)
	sort.Strings(agents)
	for _, relative := range agents {
		path := canonicalAgentPath(managedHome, relative)
		info, err := os.Lstat(path)
		if err != nil {
			return "", fmt.Errorf("inspect managed companion agent %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("managed companion agent %s is not an ordinary file", path)
		}
		for _, field := range []string{relative, info.Mode().Perm().String()} {
			if err := writeDigestField(hash, field); err != nil {
				return "", err
			}
		}
		if err := writeDigestFile(hash, path, info.Size()); err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func digestSkill(root string) (string, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("inspect managed skill %s: %w", root, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("managed skill %s is not an ordinary directory", root)
	}

	hash := sha256.New()
	hasSkillFile := false
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		kind := "file"
		if entry.IsDir() {
			kind = "directory"
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("skill contains unsupported non-regular file %s", path)
		}
		for _, field := range []string{kind, filepath.ToSlash(relative), info.Mode().Perm().String()} {
			if err := writeDigestField(hash, field); err != nil {
				return err
			}
		}
		if entry.IsDir() {
			return nil
		}
		if relative == "SKILL.md" {
			hasSkillFile = true
		}
		return writeDigestFile(hash, path, info.Size())
	})
	if err != nil {
		return "", fmt.Errorf("hash managed skill %s: %w", root, err)
	}
	if !hasSkillFile {
		return "", fmt.Errorf("installed skill %s does not contain SKILL.md", root)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func writeDigestField(destination io.Writer, value string) error {
	if err := binary.Write(destination, binary.BigEndian, uint64(len(value))); err != nil {
		return err
	}
	_, err := io.WriteString(destination, value)
	return err
}

func writeDigestFile(destination io.Writer, path string, size int64) error {
	if err := binary.Write(destination, binary.BigEndian, uint64(size)); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, file)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func validateCompatibilityMirror(canonical, compatibility string) error {
	info, err := os.Lstat(compatibility)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Claude compatibility path %s: %w", compatibility, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("claude compatibility path %s is not owned by xoldot", compatibility)
	}
	return filepath.WalkDir(compatibility, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == compatibility {
				return nil
			}
			relative, err := filepath.Rel(compatibility, path)
			if err != nil {
				return err
			}
			expected, err := os.Lstat(filepath.Join(canonical, relative))
			if err != nil || !expected.IsDir() || expected.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("claude compatibility directory %s is not owned by xoldot", path)
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return fmt.Errorf("claude compatibility file %s is not an xoldot symlink", path)
		}
		destination, err := os.Readlink(path)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(destination) {
			destination = filepath.Join(filepath.Dir(path), destination)
		}
		relative, err := filepath.Rel(compatibility, path)
		if err != nil {
			return err
		}
		expected := filepath.Join(canonical, relative)
		if filepath.Clean(destination) != expected {
			return fmt.Errorf("claude compatibility symlink %s is not owned by xoldot", path)
		}
		expectedInfo, err := os.Lstat(expected)
		if err != nil || !expectedInfo.Mode().IsRegular() {
			return fmt.Errorf("claude compatibility symlink %s does not represent an individual skill file", path)
		}
		return nil
	})
}

func buildCompatibilityMirror(canonical, compatibility string) error {
	if err := os.MkdirAll(compatibility, 0o755); err != nil {
		return err
	}
	err := filepath.WalkDir(canonical, func(source string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(canonical, source)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(compatibility, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		destination, err := filepath.Rel(filepath.Dir(target), source)
		if err != nil {
			return err
		}
		return os.Symlink(destination, target)
	})
	if err != nil {
		return fmt.Errorf("build Claude compatibility links: %w", err)
	}
	return nil
}

func (manager Manager) reportf(kind reportstatus.Kind, format string, arguments ...any) error {
	if err := reportstatus.Reportf(manager.Reporter, kind, format, arguments...); err != nil {
		return fmt.Errorf("write skill status: %w", err)
	}
	return nil
}
