package tools

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/xolan/xoldot/internal/config"
)

type Catalog struct {
	Tools []Tool `toml:"tool"`
}

type Tool struct {
	Name    string  `toml:"name"`
	Check   string  `toml:"check"`
	Install Install `toml:"install"`
}

type Install struct {
	MacOS string            `toml:"macos"`
	Linux map[string]string `toml:"linux"`
}

type Platform struct {
	OS       string
	LinuxIDs []string
	Shell    string
}

func Load(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Catalog{}, nil
		}
		return Catalog{}, fmt.Errorf("read tools: %w", err)
	}

	var catalog Catalog
	if err := toml.Unmarshal(data, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return catalog, nil
}

func Save(path string, catalog Catalog) error {
	sort.Slice(catalog.Tools, func(i, j int) bool {
		return catalog.Tools[i].Name < catalog.Tools[j].Name
	})
	data, err := toml.Marshal(catalog)
	if err != nil {
		return fmt.Errorf("encode tools: %w", err)
	}
	return config.WriteFile(path, data, 0o644)
}

func Add(catalog *Catalog, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}
	for _, tool := range catalog.Tools {
		if tool.Name == name {
			return fmt.Errorf("tool %q already exists", name)
		}
	}
	catalog.Tools = append(catalog.Tools, Tool{
		Name:  name,
		Check: "command -v " + shellWord(name),
		Install: Install{
			MacOS: "",
			Linux: map[string]string{"default": ""},
		},
	})
	return nil
}

func Remove(catalog *Catalog, name string) bool {
	for index := range catalog.Tools {
		if catalog.Tools[index].Name == name {
			catalog.Tools = append(catalog.Tools[:index], catalog.Tools[index+1:]...)
			return true
		}
	}
	return false
}

func CurrentPlatform() Platform {
	return Platform{
		OS:       runtime.GOOS,
		LinuxIDs: linuxDistributionIDs("/etc/os-release"),
		Shell:    "/bin/sh",
	}
}

func (tool Tool) InstallCommand(platform Platform) (string, error) {
	switch platform.OS {
	case "darwin":
		if strings.TrimSpace(tool.Install.MacOS) == "" {
			return "", fmt.Errorf("tool %q has no macOS install command", tool.Name)
		}
		return tool.Install.MacOS, nil
	case "linux":
		for _, id := range platform.LinuxIDs {
			if command := strings.TrimSpace(tool.Install.Linux[id]); command != "" {
				return tool.Install.Linux[id], nil
			}
		}
		if command := strings.TrimSpace(tool.Install.Linux["default"]); command != "" {
			return tool.Install.Linux["default"], nil
		}
		return "", fmt.Errorf("tool %q has no Linux install command for %s", tool.Name, distributionLabel(platform.LinuxIDs))
	default:
		return "", fmt.Errorf("tool installation is not supported on %s", platform.OS)
	}
}

func Apply(catalog Catalog, platform Platform, stdin io.Reader, stdout, stderr io.Writer, dry bool) error {
	shell := platform.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	for _, tool := range catalog.Tools {
		if strings.TrimSpace(tool.Check) == "" {
			return fmt.Errorf("tool %q has an empty check command", tool.Name)
		}
		if commandPasses(shell, tool.Check) {
			if _, err := fmt.Fprintf(stdout, "tool %s: already installed\n", tool.Name); err != nil {
				return fmt.Errorf("write tool status: %w", err)
			}
			continue
		}

		install, err := tool.InstallCommand(platform)
		if err != nil {
			return err
		}
		if dry {
			if _, err := fmt.Fprintf(stdout, "tool %s: would run: %s\n", tool.Name, install); err != nil {
				return fmt.Errorf("write tool status: %w", err)
			}
			continue
		}
		if _, err := fmt.Fprintf(stdout, "tool %s: running install command\n", tool.Name); err != nil {
			return fmt.Errorf("write tool status: %w", err)
		}
		command := exec.Command(shell, "-c", install)
		command.Stdin = stdin
		command.Stdout = stdout
		command.Stderr = stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("install tool %q: %w", tool.Name, err)
		}
		if !commandPasses(shell, tool.Check) {
			return fmt.Errorf("tool %q still fails its check after installation", tool.Name)
		}
	}
	return nil
}

func commandPasses(shell, script string) bool {
	command := exec.Command(shell, "-c", script)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run() == nil
}

func linuxDistributionIDs(path string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if !found {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}

	var ids []string
	if id := strings.ToLower(values["ID"]); id != "" {
		ids = append(ids, id)
	}
	for _, id := range strings.Fields(strings.ToLower(values["ID_LIKE"])) {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

func distributionLabel(ids []string) string {
	if len(ids) == 0 {
		return "unknown distribution"
	}
	return strings.Join(ids, "/")
}

func shellWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
