package profiles

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/pathutil"
	agentskills "github.com/xolan/xoldot/internal/skills"
	toolcatalog "github.com/xolan/xoldot/internal/tools"
)

var validName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9_-]*[a-z0-9])?$`)

type document struct {
	extends     []string
	tools       []string
	aliases     []string
	skills      []string
	managedHome []managedPath
}

type file struct {
	Extends     []string `toml:"extends"`
	Tools       []string `toml:"tools"`
	Aliases     []string `toml:"aliases"`
	Skills      []string `toml:"skills"`
	ManagedHome []string `toml:"managed_home"`
}

type managedPath struct {
	path      string
	recursive bool
}

type members struct {
	tools       map[string]struct{}
	aliases     map[string]struct{}
	skills      map[string]struct{}
	managedHome map[string]bool
}

type Configuration struct {
	Name        string
	Tools       toolcatalog.Catalog
	Aliases     aliases.File
	Skills      agentskills.Catalog
	ManagedHome ManagedHomeSelection
}

type ManagedHomeSelection struct {
	paths []managedPath
}

type profileSet struct {
	resolved map[string]members
	tools    toolcatalog.Catalog
	aliases  aliases.File
	skills   agentskills.Catalog
}

type CatalogError struct {
	err error
}

func (err *CatalogError) Error() string {
	return err.err.Error()
}

func (err *CatalogError) Unwrap() error {
	return err.err
}

func (selection ManagedHomeSelection) Includes(relative string) bool {
	relative = filepath.Clean(relative)
	for _, selected := range selection.paths {
		if relative == selected.path || selected.recursive && pathutil.Contains(selected.path, relative) {
			return true
		}
	}
	return false
}

// Validate checks every Profile and each referenced catalog member.
func Validate(paths config.Paths) error {
	_, err := loadProfileSet(paths)
	return err
}

func loadProfileSet(paths config.Paths) (profileSet, error) {
	documents, err := loadDocuments(paths.Profiles)
	if err != nil {
		return profileSet{}, err
	}
	tools, err := toolcatalog.Load(paths.Tools)
	if err != nil {
		return profileSet{}, &CatalogError{err: err}
	}
	aliasFile, err := aliases.Load(paths.Aliases)
	if err != nil {
		return profileSet{}, &CatalogError{err: err}
	}
	skills, err := agentskills.Load(paths.Skills)
	if err != nil {
		return profileSet{}, &CatalogError{err: err}
	}

	if err := validateDocuments(documents, paths.ManagedHome, tools, aliasFile, skills); err != nil {
		return profileSet{}, err
	}
	resolved, err := resolveAll(documents)
	if err != nil {
		return profileSet{}, err
	}
	return profileSet{
		resolved: resolved,
		tools:    tools,
		aliases:  aliasFile,
		skills:   skills,
	}, nil
}

func Load(paths config.Paths, selectedName string) (Configuration, error) {
	profiles, err := loadProfileSet(paths)
	if err != nil {
		return Configuration{}, err
	}
	selectedName, err = normalizeName(selectedName)
	if err != nil {
		return Configuration{}, err
	}
	if _, exists := profiles.resolved[selectedName]; !exists {
		return Configuration{}, fmt.Errorf("profile %q does not exist", selectedName)
	}
	selected := profiles.resolved[selectedName]

	for _, skill := range profiles.skills.Skills {
		if _, exists := selected.skills[skill.Name]; !exists {
			continue
		}
		managedPaths, err := agentskills.ManagedHomePaths(paths.ManagedHome, skill)
		if err != nil {
			return Configuration{}, err
		}
		for _, path := range managedPaths {
			addManagedPath(selected.managedHome, path.Relative, path.Recursive)
		}
	}

	return Configuration{
		Name:        selectedName,
		Tools:       filterTools(profiles.tools, selected.tools),
		Aliases:     filterAliases(profiles.aliases, selected.aliases),
		Skills:      filterSkills(profiles.skills, selected.skills),
		ManagedHome: newManagedHomeSelection(selected.managedHome),
	}, nil
}

func loadDocuments(directory string) (map[string]document, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("profiles directory %s does not exist", directory)
		}
		return nil, fmt.Errorf("read profiles directory %s: %w", directory, err)
	}

	documents := make(map[string]document)
	sources := make(map[string]string)
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".toml" {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("profile %s is not an ordinary file", entry.Name())
		}
		name, err := normalizeName(strings.TrimSuffix(entry.Name(), ".toml"))
		if err != nil {
			return nil, fmt.Errorf("profile file %q: %w", entry.Name(), err)
		}
		if previous, exists := sources[name]; exists {
			return nil, fmt.Errorf("profile files %q and %q have duplicate name %q after normalization", previous, entry.Name(), name)
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect profile %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("profile %s is not an ordinary file", entry.Name())
		}
		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read profile %s: %w", path, err)
		}
		var decoded file
		if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&decoded); err != nil {
			return nil, fmt.Errorf("parse profile %s: %w", path, err)
		}
		managedHome := make([]managedPath, len(decoded.ManagedHome))
		for index, relative := range decoded.ManagedHome {
			managedHome[index] = managedPath{path: relative}
		}
		documents[name] = document{
			extends:     decoded.Extends,
			tools:       decoded.Tools,
			aliases:     decoded.Aliases,
			skills:      decoded.Skills,
			managedHome: managedHome,
		}
		sources[name] = entry.Name()
	}
	return documents, nil
}

func validateDocuments(
	documents map[string]document,
	managedRoot string,
	tools toolcatalog.Catalog,
	aliasFile aliases.File,
	skills agentskills.Catalog,
) error {
	toolNames := make(map[string]struct{}, len(tools.Tools))
	for _, tool := range tools.Tools {
		toolNames[tool.Name] = struct{}{}
	}
	aliasNames := make(map[string]struct{}, len(aliasFile.Aliases))
	for _, alias := range aliasFile.Aliases {
		aliasNames[alias.Name] = struct{}{}
	}
	skillNames := make(map[string]struct{}, len(skills.Skills))
	for _, skill := range skills.Skills {
		skillNames[skill.Name] = struct{}{}
	}

	var resolvedManagedRoot string
	for _, name := range sortedDocumentNames(documents) {
		document := documents[name]
		for index, parent := range document.extends {
			normalized, err := normalizeName(parent)
			if err != nil {
				return fmt.Errorf("profile %q has invalid parent %q: %w", name, parent, err)
			}
			if _, exists := documents[normalized]; !exists {
				return fmt.Errorf("profile %q extends missing profile %q", name, parent)
			}
			document.extends[index] = normalized
		}
		for _, tool := range document.tools {
			if _, exists := toolNames[tool]; !exists {
				return fmt.Errorf("profile %q references unknown Tool %q", name, tool)
			}
		}
		for _, alias := range document.aliases {
			if _, exists := aliasNames[alias]; !exists {
				return fmt.Errorf("profile %q references unknown Alias %q", name, alias)
			}
		}
		for _, skill := range document.skills {
			if _, exists := skillNames[skill]; !exists {
				return fmt.Errorf("profile %q references unknown Skill %q", name, skill)
			}
		}
		for index, selected := range document.managedHome {
			if resolvedManagedRoot == "" {
				resolved, err := filepath.EvalSymlinks(managedRoot)
				if err != nil {
					return fmt.Errorf("resolve managed home %s: %w", managedRoot, err)
				}
				resolvedManagedRoot = resolved
			}
			normalized, err := validateManagedPath(selected.path)
			if err != nil {
				return fmt.Errorf("profile %q has invalid managed home member %q: %w", name, selected.path, err)
			}
			if agentskills.IsReservedManagedHomeSelection(normalized) {
				return fmt.Errorf("profile %q cannot select reserved Skill or Companion-agent path %q through managed_home", name, selected.path)
			}
			info, err := inspectManagedPath(resolvedManagedRoot, normalized)
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("profile %q references unknown managed home member %q", name, selected.path)
			}
			if err != nil {
				return fmt.Errorf("inspect managed home member %s: %w", selected.path, err)
			}
			document.managedHome[index] = managedPath{path: normalized, recursive: info.IsDir()}
		}
		documents[name] = document
	}
	return nil
}

func inspectManagedPath(managedRoot, relative string) (os.FileInfo, error) {
	path := filepath.Join(managedRoot, relative)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if filepath.Clean(resolvedParent) != filepath.Clean(filepath.Dir(path)) || !pathutil.Contains(managedRoot, resolvedParent) {
		return nil, fmt.Errorf("managed home member %q escapes files/home through a directory symlink", relative)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return info, nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	if !pathutil.Contains(managedRoot, resolved) {
		return nil, fmt.Errorf("managed home member %q escapes files/home through a symlink", relative)
	}
	return info, nil
}

func resolveAll(documents map[string]document) (map[string]members, error) {
	const (
		visiting = 1
		visited  = 2
	)
	states := make(map[string]int, len(documents))
	resolved := make(map[string]members, len(documents))
	var stack []string
	var resolve func(string) (members, error)
	resolve = func(name string) (members, error) {
		switch states[name] {
		case visited:
			return resolved[name], nil
		case visiting:
			start := 0
			for stack[start] != name {
				start++
			}
			cycle := append(append([]string(nil), stack[start:]...), name)
			return members{}, fmt.Errorf("profile inheritance cycle: %s", strings.Join(cycle, " -> "))
		}
		states[name] = visiting
		stack = append(stack, name)
		result := newMembers()
		parents := append([]string(nil), documents[name].extends...)
		sort.Strings(parents)
		for _, parent := range parents {
			inherited, err := resolve(parent)
			if err != nil {
				return members{}, err
			}
			mergeMembers(&result, inherited)
		}
		addDocumentMembers(&result, documents[name])
		stack = stack[:len(stack)-1]
		states[name] = visited
		resolved[name] = result
		return result, nil
	}

	for _, name := range sortedDocumentNames(documents) {
		if _, err := resolve(name); err != nil {
			return nil, err
		}
	}
	return resolved, nil
}

func newMembers() members {
	return members{
		tools:       make(map[string]struct{}),
		aliases:     make(map[string]struct{}),
		skills:      make(map[string]struct{}),
		managedHome: make(map[string]bool),
	}
}

func mergeMembers(target *members, source members) {
	for name := range source.tools {
		target.tools[name] = struct{}{}
	}
	for name := range source.aliases {
		target.aliases[name] = struct{}{}
	}
	for name := range source.skills {
		target.skills[name] = struct{}{}
	}
	for path, recursive := range source.managedHome {
		target.managedHome[path] = recursive
	}
}

func addDocumentMembers(target *members, source document) {
	for _, name := range source.tools {
		target.tools[name] = struct{}{}
	}
	for _, name := range source.aliases {
		target.aliases[name] = struct{}{}
	}
	for _, name := range source.skills {
		target.skills[name] = struct{}{}
	}
	for _, selected := range source.managedHome {
		target.managedHome[selected.path] = selected.recursive
	}
}

func normalizeName(name string) (string, error) {
	normalized := strings.ToLower(name)
	if !validName.MatchString(normalized) {
		return "", fmt.Errorf("invalid profile name %q; use letters, numbers, underscores, or hyphens", name)
	}
	return normalized, nil
}

func validateManagedPath(relative string) (string, error) {
	if relative == "" || strings.Contains(relative, `\`) {
		return "", fmt.Errorf("use a clean relative path")
	}
	path := filepath.FromSlash(relative)
	clean := filepath.Clean(path)
	if filepath.IsAbs(path) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.ToSlash(clean) != relative {
		return "", fmt.Errorf("use a clean relative path that stays within files/home")
	}
	return clean, nil
}

func sortedDocumentNames(documents map[string]document) []string {
	names := make([]string, 0, len(documents))
	for name := range documents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func addManagedPath(paths map[string]bool, path string, recursive bool) {
	path = filepath.Clean(path)
	paths[path] = recursive
}

func newManagedHomeSelection(paths map[string]bool) ManagedHomeSelection {
	selection := ManagedHomeSelection{paths: make([]managedPath, 0, len(paths))}
	for path, recursive := range paths {
		selection.paths = append(selection.paths, managedPath{path: path, recursive: recursive})
	}
	return selection
}

func filterTools(catalog toolcatalog.Catalog, selected map[string]struct{}) toolcatalog.Catalog {
	filtered := toolcatalog.Catalog{Tools: make([]toolcatalog.Tool, 0, len(selected))}
	for _, tool := range catalog.Tools {
		if _, exists := selected[tool.Name]; exists {
			filtered.Tools = append(filtered.Tools, tool)
		}
	}
	return filtered
}

func filterAliases(file aliases.File, selected map[string]struct{}) aliases.File {
	filtered := aliases.File{Aliases: make([]aliases.Alias, 0, len(selected))}
	for _, alias := range file.Aliases {
		if _, exists := selected[alias.Name]; exists {
			filtered.Aliases = append(filtered.Aliases, alias)
		}
	}
	return filtered
}

func filterSkills(catalog agentskills.Catalog, selected map[string]struct{}) agentskills.Catalog {
	filtered := agentskills.Catalog{Skills: make([]agentskills.Skill, 0, len(selected))}
	for _, skill := range catalog.Skills {
		if _, exists := selected[skill.Name]; exists {
			filtered.Skills = append(filtered.Skills, skill)
		}
	}
	return filtered
}
