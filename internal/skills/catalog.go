package skills

import (
	"errors"
	"fmt"
	"os"
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
	Name   string `toml:"name"`
	Source string `toml:"source"`
	Digest string `toml:"digest"`
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
	if err := toml.Unmarshal(data, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return catalog, nil
}

func Save(path string, catalog Catalog) error {
	sort.Slice(catalog.Skills, func(i, j int) bool {
		return catalog.Skills[i].Name < catalog.Skills[j].Name
	})
	data, err := toml.Marshal(catalog)
	if err != nil {
		return fmt.Errorf("encode skills: %w", err)
	}
	return config.WriteFile(path, data, 0o644)
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
