package tools

import (
	"bufio"
	"bytes"
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
	catalog.Tools = append([]Tool(nil), catalog.Tools...)
	sort.Slice(catalog.Tools, func(i, j int) bool {
		return catalog.Tools[i].Name < catalog.Tools[j].Name
	})
	data, err := toml.Marshal(catalog)
	if err != nil {
		return fmt.Errorf("encode tools: %w", err)
	}
	return config.WriteFile(path, data, 0o644)
}

func Validate(catalog Catalog) error {
	seen := make(map[string]struct{}, len(catalog.Tools))
	for _, tool := range catalog.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			return fmt.Errorf("tool name cannot be empty")
		}
		if strings.TrimSpace(tool.Check) == "" {
			return fmt.Errorf("tool %q has an empty check command", tool.Name)
		}
		if _, exists := seen[tool.Name]; exists {
			return fmt.Errorf("tool %q is duplicated", tool.Name)
		}
		seen[tool.Name] = struct{}{}
	}
	return nil
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
	if err := Validate(catalog); err != nil {
		return err
	}
	shell := platform.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	if dry {
		return preview(catalog, platform, stdout)
	}

	type installPlan struct {
		tool    Tool
		command string
	}
	var missing []Tool
	for _, tool := range catalog.Tools {
		if commandPasses(shell, tool.Check) {
			if _, err := fmt.Fprintf(stdout, "tool %s: already installed\n", tool.Name); err != nil {
				return fmt.Errorf("write tool status: %w", err)
			}
			continue
		}
		missing = append(missing, tool)
	}
	plans := make([]installPlan, 0, len(missing))
	for _, tool := range missing {
		command, err := tool.InstallCommand(platform)
		if err != nil {
			return err
		}
		plans = append(plans, installPlan{tool: tool, command: command})
	}
	for _, plan := range plans {
		if _, err := fmt.Fprintf(stdout, "tool %s: running install command\n", plan.tool.Name); err != nil {
			return fmt.Errorf("write tool status: %w", err)
		}
		command := exec.Command(shell, "-c", plan.command)
		command.Stdin = stdin
		command.Stdout = stdout
		command.Stderr = stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("install tool %q: %w", plan.tool.Name, err)
		}
		if !commandPasses(shell, plan.tool.Check) {
			return fmt.Errorf("tool %q still fails its check after installation", plan.tool.Name)
		}
	}
	return nil
}

func preview(catalog Catalog, platform Platform, stdout io.Writer) error {
	for _, tool := range catalog.Tools {
		if _, err := fmt.Fprintf(stdout, "tool %s: would check: %s\n", tool.Name, tool.Check); err != nil {
			return fmt.Errorf("write tool status: %w", err)
		}
		install, err := tool.InstallCommand(platform)
		if err != nil {
			if _, writeErr := fmt.Fprintf(stdout, "tool %s: if missing, %v\n", tool.Name, err); writeErr != nil {
				return fmt.Errorf("write tool status: %w", writeErr)
			}
			continue
		}
		if _, err := fmt.Fprintf(stdout, "tool %s: if missing, would run: %s\n", tool.Name, install); err != nil {
			return fmt.Errorf("write tool status: %w", err)
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
