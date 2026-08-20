package skills

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/xolan/xoldot/internal/config"
)

var (
	validSkillName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
	githubPart     = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

type Catalog struct {
	Skills []Skill `toml:"skill"`
}

type Skill struct {
	Name   string   `toml:"name"`
	Source string   `toml:"source"`
	Digest string   `toml:"digest"`
	Agents []string `toml:"agents,omitempty"`
}

func Load(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Catalog{}, nil
		}
		return Catalog{}, fmt.Errorf("read skills: %w", err)
	}

	var catalog Catalog
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := Validate(catalog); err != nil {
		return Catalog{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return catalog, nil
}

func Save(path string, catalog Catalog) error {
	if err := Validate(catalog); err != nil {
		return err
	}
	catalog.Skills = append([]Skill(nil), catalog.Skills...)
	for index := range catalog.Skills {
		catalog.Skills[index].Agents = append([]string(nil), catalog.Skills[index].Agents...)
		sort.Strings(catalog.Skills[index].Agents)
	}
	sort.Slice(catalog.Skills, func(i, j int) bool {
		return catalog.Skills[i].Name < catalog.Skills[j].Name
	})
	data, err := toml.Marshal(catalog)
	if err != nil {
		return fmt.Errorf("encode skills: %w", err)
	}
	return config.WriteFile(path, data, 0o644)
}

func Validate(catalog Catalog) error {
	seen := make(map[string]struct{}, len(catalog.Skills))
	ownedAgents := make(map[string]string)
	for _, skill := range catalog.Skills {
		if err := validateName(skill.Name); err != nil {
			return err
		}
		if strings.TrimSpace(skill.Source) == "" {
			return fmt.Errorf("skill %q has an empty source", skill.Name)
		}
		if _, err := NormalizeSource(skill.Source, ""); err != nil {
			return fmt.Errorf("skill %q: %w", skill.Name, err)
		}
		if !validDigest(skill.Digest) {
			return fmt.Errorf("skill %q has an invalid digest", skill.Name)
		}
		if _, exists := seen[skill.Name]; exists {
			return fmt.Errorf("skill %q is duplicated", skill.Name)
		}
		for _, agent := range skill.Agents {
			if err := validateAgentPath(agent); err != nil {
				return fmt.Errorf("skill %q: %w", skill.Name, err)
			}
			if owner, exists := ownedAgents[agent]; exists {
				return fmt.Errorf("agent %q is owned by both skill %q and skill %q", agent, owner, skill.Name)
			}
			ownedAgents[agent] = skill.Name
		}
		seen[skill.Name] = struct{}{}
	}
	return nil
}

func validateAgentPath(relative string) error {
	path := filepath.FromSlash(relative)
	clean := filepath.Clean(path)
	if relative == "" || filepath.IsAbs(path) || clean == "." || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.ToSlash(clean) != relative ||
		strings.Contains(relative, `\`) || !strings.EqualFold(filepath.Ext(relative), ".md") {
		return fmt.Errorf("invalid companion agent path %q", relative)
	}
	return nil
}

func NormalizeSource(source, baseDirectory string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", fmt.Errorf("skill source cannot be empty")
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return "", fmt.Errorf("parse skill source: %w", err)
	}
	if parsed.Scheme != "" {
		scheme := strings.ToLower(parsed.Scheme)
		httpSource := scheme == "http" || scheme == "https" || strings.HasSuffix(scheme, "+http") || strings.HasSuffix(scheme, "+https")
		if parsed.User != nil || (httpSource && parsed.RawQuery != "") {
			return "", fmt.Errorf("skill source URL must not contain credentials; use a Git credential helper")
		}
		return source, nil
	}
	if filepath.IsAbs(source) {
		return filepath.Clean(source), nil
	}
	if strings.HasPrefix(source, "."+string(filepath.Separator)) || strings.HasPrefix(source, ".."+string(filepath.Separator)) {
		if baseDirectory == "" {
			return "", fmt.Errorf("relative skill source %q requires a base directory", source)
		}
		return filepath.Abs(filepath.Join(baseDirectory, source))
	}
	return source, nil
}

func validDigest(digest string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(digest, prefix) || len(digest) != len(prefix)+64 {
		return false
	}
	for _, character := range strings.TrimPrefix(digest, prefix) {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func ParseAddArguments(arguments []string) (string, string, error) {
	if len(arguments) == 1 {
		name, repository, found := strings.Cut(arguments[0], "@")
		if !found || !validGitHubRepository(repository) {
			return "", "", fmt.Errorf("usage: xoldot skill add <skill>@<owner>/<repo> or xoldot skill add <skill> --from <source>")
		}
		if err := validateName(name); err != nil {
			return "", "", err
		}
		return name, "https://github.com/" + repository, nil
	}
	if len(arguments) == 3 && arguments[1] == "--from" {
		if err := validateName(arguments[0]); err != nil {
			return "", "", err
		}
		source := strings.TrimSpace(arguments[2])
		if source == "" {
			return "", "", fmt.Errorf("skill source cannot be empty")
		}
		if validGitHubRepository(source) {
			source = "https://github.com/" + source
		}
		return arguments[0], source, nil
	}
	return "", "", fmt.Errorf("usage: xoldot skill add <skill>@<owner>/<repo> or xoldot skill add <skill> --from <source>")
}

func validateName(name string) error {
	if len(name) > 255 || !validSkillName.MatchString(name) {
		return fmt.Errorf("invalid skill name %q; use lowercase letters, numbers, and hyphens", name)
	}
	return nil
}

func validGitHubRepository(value string) bool {
	owner, repository, found := strings.Cut(value, "/")
	return found && owner != "" && repository != "" && !strings.Contains(repository, "/") &&
		githubPart.MatchString(owner) && githubPart.MatchString(repository)
}

func find(catalog Catalog, name string) (int, bool) {
	for index := range catalog.Skills {
		if catalog.Skills[index].Name == name {
			return index, true
		}
	}
	return 0, false
}
