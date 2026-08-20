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

	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/dotfiles"
	"github.com/xolan/xoldot/internal/gitops"
	agentskills "github.com/xolan/xoldot/internal/skills"
	toolcatalog "github.com/xolan/xoldot/internal/tools"
)

type app struct {
	input       io.Reader
	output      io.Writer
	errorOutput io.Writer
	configDir   string
	verbose     bool
	style       styler
}

func Run(arguments []string, input io.Reader, output, errorOutput io.Writer, version string) error {
	application := &app{input: input, output: output, errorOutput: errorOutput, style: newStyler(output)}
	root := application.rootCommand(version)
	root.SetArgs(arguments)
	root.SetOut(output)
	root.SetErr(errorOutput)
	return root.Execute()
}

func (a *app) rootCommand(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "xoldot",
		Short:         "Manage tools, aliases, agent skills, and home dotfile links",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Example: `  xoldot setup
  xoldot tool add ripgrep
  xoldot tool list
  xoldot alias add ll 'ls -la'
  xoldot skill add code-review@owner/repo
  xoldot skill add code-review --from ./skills/code-review
  xoldot skill list
  xoldot skill remove code-review
  xoldot skill update
  xoldot apply --dry
  xoldot sync --dry`,
	}
	root.PersistentFlags().StringVar(&a.configDir, "config-dir", "", "configuration directory (defaults to the xoldot config home)")
	root.PersistentFlags().BoolVarP(&a.verbose, "verbose", "v", false, "log underlying git and npx commands")

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the xoldot version",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return writef(a.output, "%s\n", version)
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "setup",
		Short: "Create the configuration directory and optionally enable git sync",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.setup()
		},
	})

	toolCommand := &cobra.Command{Use: "tool", Short: "Manage the tool catalog"}
	toolCommand.AddCommand(
		&cobra.Command{
			Use:   "add <tool>",
			Short: "Add a tool to the catalog",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, arguments []string) error {
				return a.toolAdd(arguments[0])
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List tools in the catalog",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				return a.toolList()
			},
		},
		&cobra.Command{
			Use:   "remove <tool>",
			Short: "Remove a tool from the catalog",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, arguments []string) error {
				return a.toolRemove(arguments[0])
			},
		},
	)
	root.AddCommand(toolCommand)

	aliasCommand := &cobra.Command{Use: "alias", Short: "Manage shell aliases"}
	aliasCommand.AddCommand(&cobra.Command{
		Use:   "add <alias> <command>",
		Short: "Add or update a shell alias",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, arguments []string) error {
			return a.aliasAdd(arguments[0], arguments[1])
		},
	})
	root.AddCommand(aliasCommand)

	skillCommand := &cobra.Command{Use: "skill", Aliases: []string{"skills"}, Short: "Manage global agent skills"}
	var skillSource string
	skillAdd := &cobra.Command{
		Use:   "add <skill>[@<owner>/<repo>]",
		Short: "Install a skill and add it to the catalog",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, arguments []string) error {
			raw := []string{arguments[0]}
			if skillSource != "" {
				raw = append(raw, "--from", skillSource)
			}
			name, source, err := agentskills.ParseAddArguments(raw)
			if err != nil {
				return err
			}
			manager, err := a.skillManager()
			if err != nil {
				return err
			}
			return manager.Add(name, source)
		},
	}
	skillAdd.Flags().StringVar(&skillSource, "from", "", "install the skill from an explicit source")
	skillCommand.AddCommand(
		skillAdd,
		&cobra.Command{
			Use:   "list",
			Short: "List skills in the catalog",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				return a.skillList()
			},
		},
		&cobra.Command{
			Use:   "remove <skill>",
			Short: "Uninstall a skill and drop it from the catalog",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, arguments []string) error {
				manager, err := a.skillManager()
				if err != nil {
					return err
				}
				return manager.Remove(arguments[0])
			},
		},
		&cobra.Command{
			Use:   "update [skill]",
			Short: "Update one skill, or all of them",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(_ *cobra.Command, arguments []string) error {
				manager, err := a.skillManager()
				if err != nil {
					return err
				}
				name := ""
				if len(arguments) == 1 {
					name = arguments[0]
				}
				return manager.Update(name)
			},
		},
	)
	root.AddCommand(skillCommand)

	var applyDry bool
	applyCommand := &cobra.Command{
		Use:   "apply",
		Short: "Install tools, link dotfiles, and render aliases",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.apply(applyDry)
		},
	}
	applyCommand.Flags().BoolVar(&applyDry, "dry", false, "show what would change without changing it")
	root.AddCommand(applyCommand)

	var syncDry bool
	syncCommand := &cobra.Command{
		Use:   "sync",
		Short: "Commit, pull, and push the configuration repository",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.sync(syncDry)
		},
	}
	syncCommand.Flags().BoolVar(&syncDry, "dry", false, "show what would change without changing it")
	root.AddCommand(syncCommand)

	return root
}

func (a *app) paths() (config.Paths, error) {
	root := strings.TrimSpace(a.configDir)
	if root == "" {
		var err error
		root, err = config.DefaultRoot()
		if err != nil {
			return config.Paths{}, err
		}
	} else {
		var err error
		root, err = filepath.Abs(root)
		if err != nil {
			return config.Paths{}, fmt.Errorf("resolve --config-dir: %w", err)
		}
	}
	return config.NewPaths(root), nil
}

func (a *app) gitRunner(root string) gitops.Runner {
	return gitops.Runner{Dir: root, Stdout: a.output, Stderr: a.errorOutput, Verbose: a.verbose}
}

func (a *app) setup() error {
	paths, err := a.paths()
	if err != nil {
		return err
	}
	cfg := config.Default()
	if _, statErr := os.Stat(paths.Config); statErr == nil {
		cfg, err = config.Load(paths.Config)
		if err != nil {
			return err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect configuration: %w", statErr)
	}
	git := cfg.GitSettings()

	if err := writef(a.output, "Configuration directory: %s\n", a.style.bold(paths.Root)); err != nil {
		return err
	}
	reader := bufio.NewReader(a.input)
	remotePrompt := "Git remote URL (leave blank to keep Git disabled): "
	if git.Enabled {
		remotePrompt = "Git remote URL (leave blank to keep the current remote): "
	}
	remoteURL, err := prompt(reader, a.output, a.style.bold(remotePrompt))
	if err != nil {
		return err
	}
	if remoteURL == "" {
		if err := config.Initialize(paths); err != nil {
			return err
		}
		if git.Enabled {
			return writef(a.output, "%s\n", a.style.success("Git remains enabled"))
		}
		return writef(a.output, "%s\n", a.style.warn("Git remains disabled; run setup again when a remote is ready"))
	}

	branch, err := prompt(reader, a.output, a.style.bold(fmt.Sprintf("Git branch [%s]: ", git.Branch)))
	if err != nil {
		return err
	}
	if branch == "" {
		branch = git.Branch
	}
	runner := a.gitRunner(paths.Root)
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
			if err := writef(a.output, "%s\n", a.style.success(fmt.Sprintf("Checked out existing origin/%s", branch))); err != nil {
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
	return writef(a.output, "%s\n", a.style.success(fmt.Sprintf("Git enabled with origin on branch %s", branch)))
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

func (a *app) toolCatalog() (config.Paths, toolcatalog.Catalog, error) {
	paths, err := a.paths()
	if err != nil {
		return config.Paths{}, toolcatalog.Catalog{}, err
	}
	if _, err := config.Load(paths.Config); err != nil {
		return config.Paths{}, toolcatalog.Catalog{}, err
	}
	catalog, err := toolcatalog.Load(paths.Tools)
	if err != nil {
		return config.Paths{}, toolcatalog.Catalog{}, err
	}
	return paths, catalog, nil
}

func (a *app) toolAdd(name string) error {
	paths, catalog, err := a.toolCatalog()
	if err != nil {
		return err
	}
	if err := toolcatalog.Add(&catalog, name); err != nil {
		return err
	}
	if err := toolcatalog.Save(paths.Tools, catalog); err != nil {
		return err
	}
	return writef(a.output, "%s; edit %s to set install commands\n", a.style.success("Added tool "+name), paths.Tools)
}

func (a *app) toolList() error {
	_, catalog, err := a.toolCatalog()
	if err != nil {
		return err
	}
	names := make([]string, len(catalog.Tools))
	for index, tool := range catalog.Tools {
		names[index] = tool.Name
	}
	return writeSortedNames(a.output, names)
}

func (a *app) toolRemove(name string) error {
	paths, catalog, err := a.toolCatalog()
	if err != nil {
		return err
	}
	if !toolcatalog.Remove(&catalog, name) {
		return fmt.Errorf("tool %q does not exist", name)
	}
	if err := toolcatalog.Save(paths.Tools, catalog); err != nil {
		return err
	}
	return writef(a.output, "%s\n", a.style.success("Removed tool "+name))
}

func (a *app) aliasAdd(name, command string) error {
	paths, err := a.paths()
	if err != nil {
		return err
	}
	if _, err := config.Load(paths.Config); err != nil {
		return err
	}
	file, err := aliases.Load(paths.Aliases)
	if err != nil {
		return err
	}
	updated, err := aliases.Add(&file, name, command)
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
	return writef(a.output, "%s\n", a.style.success(fmt.Sprintf("%s alias %s", verb, name)))
}

func (a *app) skillManager() (agentskills.Manager, error) {
	paths, err := a.paths()
	if err != nil {
		return agentskills.Manager{}, err
	}
	if _, err := config.Load(paths.Config); err != nil {
		return agentskills.Manager{}, err
	}
	return agentskills.Manager{
		CatalogPath: paths.Skills,
		ManagedHome: paths.ManagedHome,
		Stdin:       a.input,
		Stdout:      a.output,
		Stderr:      a.errorOutput,
		Verbose:     a.verbose,
	}, nil
}

func (a *app) skillList() error {
	manager, err := a.skillManager()
	if err != nil {
		return err
	}
	catalog, err := agentskills.Load(manager.CatalogPath)
	if err != nil {
		return err
	}
	names := make([]string, len(catalog.Skills))
	for index, skill := range catalog.Skills {
		names[index] = skill.Name
	}
	return writeSortedNames(a.output, names)
}

func (a *app) apply(dry bool) error {
	paths, err := a.paths()
	if err != nil {
		return err
	}
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

	if err := toolcatalog.Apply(catalog, toolcatalog.CurrentPlatform(), a.input, a.output, a.errorOutput, dry); err != nil {
		return err
	}
	linked, err := dotfiles.Link(paths.ManagedHome, home, paths.Root, a.output, dry)
	if err != nil {
		return err
	}
	if dry {
		if err := writef(a.output, "dotfiles: would link %d, would remove %d, %d already current\n", linked.Created, linked.Removed, linked.Current); err != nil {
			return err
		}
		return writef(a.output, "aliases: would render %s\n", aliasPath)
	}
	summary := fmt.Sprintf("dotfiles: %d linked, %d removed, %d already current", linked.Created, linked.Removed, linked.Current)
	if err := writef(a.output, "%s\n", a.style.success(summary)); err != nil {
		return err
	}
	if err := aliases.Render(aliasPath, shell, file.Aliases); err != nil {
		return err
	}
	return writef(a.output, "%s\n", a.style.success("aliases: rendered "+aliasPath))
}

func (a *app) sync(dry bool) error {
	paths, err := a.paths()
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	git := cfg.GitSettings()
	if !git.Enabled {
		return fmt.Errorf("git is disabled; run 'xoldot setup' with a remote URL")
	}
	if err := a.gitRunner(paths.Root).Sync(git.Remote, git.Branch, dry); err != nil {
		return err
	}
	if dry {
		return write(a.output, "Dry run complete; nothing was changed\n")
	}
	return writef(a.output, "%s\n", a.style.success("Sync complete"))
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

func writeSortedNames(output io.Writer, names []string) error {
	slices.Sort(names)
	if len(names) == 0 {
		return nil
	}
	return write(output, strings.Join(names, "\n")+"\n")
}
