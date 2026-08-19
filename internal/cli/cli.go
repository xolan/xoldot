package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/dotfiles"
	"github.com/xolan/xoldot/internal/gitops"
	agentskills "github.com/xolan/xoldot/internal/skills"
	toolcatalog "github.com/xolan/xoldot/internal/tools"
)

func Run(arguments []string, input io.Reader, output, errorOutput io.Writer, version string) error {
	root, arguments, err := parseGlobal(arguments)
	if err != nil {
		return err
	}
	paths := config.NewPaths(root)

	if len(arguments) == 0 {
		return write(output, usage)
	}

	switch arguments[0] {
	case "help", "-h", "--help":
		return write(output, usage)
	case "version", "--version":
		return writef(output, "%s\n", version)
	case "setup":
		if len(arguments) != 1 {
			return fmt.Errorf("usage: xoldot setup")
		}
		return setup(paths, input, output, errorOutput)
	case "tool":
		return tool(paths, arguments[1:], output)
	case "alias":
		return alias(paths, arguments[1:], output)
	case "skill", "skills":
		return skill(paths, arguments[1:], input, output, errorOutput)
	case "apply":
		if len(arguments) != 1 {
			return fmt.Errorf("usage: xoldot apply")
		}
		return apply(paths, input, output, errorOutput)
	case "sync":
		if len(arguments) != 1 {
			return fmt.Errorf("usage: xoldot sync")
		}
		return sync(paths, input, output, errorOutput)
	default:
		return fmt.Errorf("unknown command %q; run 'xoldot help'", arguments[0])
	}
}

func parseGlobal(arguments []string) (string, []string, error) {
	root, err := config.DefaultRoot()
	if err != nil {
		return "", nil, err
	}
	if len(arguments) > 0 && arguments[0] == "--config-dir" {
		if len(arguments) < 2 || strings.TrimSpace(arguments[1]) == "" {
			return "", nil, fmt.Errorf("--config-dir requires a directory")
		}
		root, err = filepath.Abs(arguments[1])
		if err != nil {
			return "", nil, fmt.Errorf("resolve --config-dir: %w", err)
		}
		arguments = arguments[2:]
	}
	return root, arguments, nil
}

func setup(paths config.Paths, input io.Reader, output, errorOutput io.Writer) error {
	cfg := config.Default()
	if _, statErr := os.Stat(paths.Config); statErr == nil {
		var err error
		cfg, err = config.Load(paths.Config)
		if err != nil {
			return err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect configuration: %w", statErr)
	}
	git := cfg.GitSettings()

	if err := writef(output, "Configuration directory: %s\n", paths.Root); err != nil {
		return err
	}
	reader := bufio.NewReader(input)
	remotePrompt := "Git remote URL (leave blank to keep Git disabled): "
	if git.Enabled {
		remotePrompt = "Git remote URL (leave blank to keep the current remote): "
	}
	remoteURL, err := prompt(reader, output, remotePrompt)
	if err != nil {
		return err
	}
	if remoteURL == "" {
		if err := config.Initialize(paths); err != nil {
			return err
		}
		if git.Enabled {
			return write(output, "Git remains enabled\n")
		} else {
			return write(output, "Git remains disabled; run setup again when a remote is ready\n")
		}
	}

	branch, err := prompt(reader, output, fmt.Sprintf("Git branch [%s]: ", git.Branch))
	if err != nil {
		return err
	}
	if branch == "" {
		branch = git.Branch
	}
	runner := gitops.Runner{Stdin: reader, Dir: paths.Root, Stdout: output, Stderr: errorOutput}
	if err := runner.Configure(remoteURL, branch); err != nil {
		return err
	}
	hasHistory, err := runner.HasLocalHistory()
	if err != nil {
		return err
	}
	if !hasHistory {
		checkedOut, err := runner.CheckoutRemote("origin", branch)
		if err != nil {
			var checkoutErr *gitops.CheckoutError
			if errors.As(err, &checkoutErr) {
				return fmt.Errorf(
					"configuration directory conflicts with existing origin/%s; move it aside, rerun setup, then merge local files manually: %w",
					branch,
					err,
				)
			}
			return fmt.Errorf("inspect existing origin/%s before setup: %w", branch, err)
		}
		if checkedOut {
			if err := writef(output, "Checked out existing origin/%s\n", branch); err != nil {
				return err
			}
		}
	}
	if err := config.Initialize(paths); err != nil {
		return err
	}
	cfg, err = config.Load(paths.Config)
	if err != nil {
		return err
	}
	git = cfg.GitSettings()
	git.Enabled = true
	git.Remote = "origin"
	git.Branch = branch
	if err := config.Save(paths.Config, cfg); err != nil {
		return err
	}
	return writef(output, "Git enabled with origin on branch %s\n", branch)
}

func prompt(reader *bufio.Reader, output io.Writer, message string) (string, error) {
	if err := write(output, message); err != nil {
		return "", err
	}
	answer, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read setup input: %w", err)
	}
	return strings.TrimSpace(answer), nil
}

func tool(paths config.Paths, arguments []string, output io.Writer) error {
	if _, err := config.Load(paths.Config); err != nil {
		return err
	}
	if len(arguments) != 2 {
		return fmt.Errorf("usage: xoldot tool <add|remove> <tool>")
	}
	catalog, err := toolcatalog.Load(paths.Tools)
	if err != nil {
		return err
	}
	name := arguments[1]
	switch arguments[0] {
	case "add":
		if err := toolcatalog.Add(&catalog, name); err != nil {
			return err
		}
		if err := toolcatalog.Save(paths.Tools, catalog); err != nil {
			return err
		}
		return writef(output, "Added tool %s; edit %s to set install commands\n", name, paths.Tools)
	case "remove":
		if !toolcatalog.Remove(&catalog, name) {
			return fmt.Errorf("tool %q does not exist", name)
		}
		if err := toolcatalog.Save(paths.Tools, catalog); err != nil {
			return err
		}
		return writef(output, "Removed tool %s\n", name)
	default:
		return fmt.Errorf("usage: xoldot tool <add|remove> <tool>")
	}
}

func alias(paths config.Paths, arguments []string, output io.Writer) error {
	if _, err := config.Load(paths.Config); err != nil {
		return err
	}
	if len(arguments) != 3 || arguments[0] != "add" {
		return fmt.Errorf("usage: xoldot alias add <alias> <command>")
	}
	file, err := aliases.Load(paths.Aliases)
	if err != nil {
		return err
	}
	updated, err := aliases.Add(&file, arguments[1], arguments[2])
	if err != nil {
		return err
	}
	if err := aliases.Save(paths.Aliases, file); err != nil {
		return err
	}
	verb := "Added"
	if updated {
		verb = "Updated"
	}
	return writef(output, "%s alias %s\n", verb, arguments[1])
}

func skill(paths config.Paths, arguments []string, input io.Reader, output, errorOutput io.Writer) error {
	if _, err := config.Load(paths.Config); err != nil {
		return err
	}
	if len(arguments) == 0 {
		return fmt.Errorf("usage: xoldot skill <add|remove|update>")
	}
	manager := agentskills.Manager{
		CatalogPath: paths.Skills,
		ManagedHome: paths.ManagedHome,
		Stdin:       input,
		Stdout:      output,
		Stderr:      errorOutput,
	}
	switch arguments[0] {
	case "add":
		name, source, err := agentskills.ParseAddArguments(arguments[1:])
		if err != nil {
			return err
		}
		return manager.Add(name, source)
	case "remove":
		if len(arguments) != 2 {
			return fmt.Errorf("usage: xoldot skill remove <skill>")
		}
		return manager.Remove(arguments[1])
	case "update":
		if len(arguments) > 2 {
			return fmt.Errorf("usage: xoldot skill update [skill]")
		}
		name := ""
		if len(arguments) == 2 {
			name = arguments[1]
		}
		return manager.Update(name)
	default:
		return fmt.Errorf("usage: xoldot skill <add|remove|update>")
	}
}

func apply(paths config.Paths, input io.Reader, output, errorOutput io.Writer) error {
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	catalog, err := toolcatalog.Load(paths.Tools)
	if err != nil {
		return err
	}
	home, err := config.TargetHome()
	if err != nil {
		return err
	}
	shell, err := aliases.DetectShell()
	if err != nil {
		return err
	}
	aliasSettings := cfg.AliasSettings()
	if !slices.Contains(aliasSettings.Shells, shell) {
		return fmt.Errorf("detected shell %q is disabled by aliases.shells", shell)
	}
	aliasDir, err := config.ExpandHome(aliasSettings.Dir, home)
	if err != nil {
		return err
	}
	file, err := aliases.Load(paths.Aliases)
	if err != nil {
		return err
	}
	if err := aliases.Validate(file.Aliases); err != nil {
		return err
	}
	aliasPath := filepath.Join(aliasDir, "alias."+shell)

	if err := toolcatalog.Apply(catalog, toolcatalog.CurrentPlatform(), input, output, errorOutput); err != nil {
		return err
	}
	linked, err := dotfiles.Link(paths.ManagedHome, home, paths.Root)
	if err != nil {
		return err
	}
	if err := writef(output, "dotfiles: %d linked, %d removed, %d already current\n", linked.Created, linked.Removed, linked.Current); err != nil {
		return err
	}
	if err := aliases.Render(aliasPath, shell, file.Aliases); err != nil {
		return err
	}
	return writef(output, "aliases: rendered %s\n", aliasPath)
}

func sync(paths config.Paths, input io.Reader, output, errorOutput io.Writer) error {
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	git := cfg.GitSettings()
	if !git.Enabled {
		return fmt.Errorf("git is disabled; run 'xoldot setup' with a remote URL")
	}
	runner := gitops.Runner{Stdin: input, Dir: paths.Root, Stdout: output, Stderr: errorOutput}
	if err := runner.Sync(git.Remote, git.Branch); err != nil {
		return err
	}
	return write(output, "Sync complete\n")
}

func write(output io.Writer, value string) error {
	if _, err := io.WriteString(output, value); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func writef(output io.Writer, format string, arguments ...any) error {
	if _, err := fmt.Fprintf(output, format, arguments...); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

const usage = `xoldot manages tools, aliases, agent skills, and home dotfile links.

Usage:
  xoldot [--config-dir DIR] setup
  xoldot [--config-dir DIR] tool add <tool>
  xoldot [--config-dir DIR] tool remove <tool>
  xoldot [--config-dir DIR] alias add <alias> <command>
  xoldot [--config-dir DIR] skill add <skill>@<owner>/<repo>
  xoldot [--config-dir DIR] skill add <skill> --from <source>
  xoldot [--config-dir DIR] skill remove <skill>
  xoldot [--config-dir DIR] skill update [skill]
  xoldot [--config-dir DIR] apply
  xoldot [--config-dir DIR] sync
  xoldot version
`
