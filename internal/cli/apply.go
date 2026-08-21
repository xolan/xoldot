package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/lifecyclescripts"
	"github.com/xolan/xoldot/internal/managedhome"
	"github.com/xolan/xoldot/internal/status"
	toolcatalog "github.com/xolan/xoldot/internal/tools"
)

type applyPart string

const (
	applyPartTools       applyPart = "tools"
	applyPartManagedHome applyPart = "managed-home"
	applyPartAliases     applyPart = "aliases"
)

var applyParts = [...]applyPart{applyPartTools, applyPartManagedHome, applyPartAliases}

func applyPartValues() []string {
	values := make([]string, len(applyParts))
	for index, part := range applyParts {
		values[index] = string(part)
	}
	return values
}

type applySelection struct {
	tools       bool
	managedHome bool
	aliases     bool
}

func (selection applySelection) values() []string {
	values := make([]string, 0, len(applyParts))
	if selection.tools {
		values = append(values, string(applyPartTools))
	}
	if selection.managedHome {
		values = append(values, string(applyPartManagedHome))
	}
	if selection.aliases {
		values = append(values, string(applyPartAliases))
	}
	return values
}

func parseApplySelection(values []string) (applySelection, error) {
	if len(values) == 0 {
		return applySelection{tools: true, managedHome: true, aliases: true}, nil
	}

	var selection applySelection
	for _, value := range values {
		switch applyPart(value) {
		case applyPartTools:
			selection.tools = true
		case applyPartManagedHome:
			selection.managedHome = true
		case applyPartAliases:
			selection.aliases = true
		default:
			return applySelection{}, fmt.Errorf(
				"unknown apply part %q; use %s",
				value,
				strings.Join(applyPartValues(), ", "),
			)
		}
	}
	return selection, nil
}

type applyPlan struct {
	selection   applySelection
	tools       toolcatalog.Plan
	managedHome managedhome.Plan
	aliases     aliases.Plan
	scripts     lifecyclescripts.Plan
	aliasPath   string
}

func (a *app) applyCommand() *cobra.Command {
	var dry bool
	var backup bool
	var only []string
	var profile string
	command := &cobra.Command{
		Use:   "apply",
		Short: "Install tools, link managed home content, and render aliases",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			selection, err := parseApplySelection(only)
			if err != nil {
				return err
			}
			if backup && !selection.managedHome {
				return fmt.Errorf("--backup requires the managed-home Apply part")
			}
			return a.apply(dry, backup, selection, profile)
		},
	}
	command.Flags().BoolVar(&dry, "dry", false, "show what would change without changing it")
	command.Flags().BoolVar(&backup, "backup", false, "back up eligible managed-home conflicts before linking")
	command.Flags().StringVar(&profile, "profile", "", "select one profile before applying")
	command.Flags().StringArrayVar(
		&only,
		"only",
		nil,
		fmt.Sprintf("apply only this part; repeat for multiple (%s; default: all)", strings.Join(applyPartValues(), ", ")),
	)
	if err := command.RegisterFlagCompletionFunc(
		"only",
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return applyPartValues(), cobra.ShellCompDirectiveNoFileComp
		},
	); err != nil {
		panic(fmt.Sprintf("register --only completion: %v", err))
	}
	return command
}

func (a *app) apply(dry, backup bool, selection applySelection, profile string) error {
	paths, err := a.paths()
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	input, err := loadConfigurationInput(paths, profile, configurationNeeds{
		tools:   selection.tools,
		aliases: selection.aliases,
	})
	if err != nil {
		return err
	}
	scripts, err := lifecyclescripts.Load(paths.Root, paths.Scripts)
	if err != nil {
		return err
	}
	plan, err := prepareApply(paths, cfg, selection, input, backup, scripts, dry)
	if err != nil {
		var refusal *managedhome.PreparationRefusal
		if dry && errors.As(err, &refusal) {
			if previewErr := refusal.Preview(a.reporter); previewErr != nil {
				return errors.Join(err, previewErr)
			}
		}
		return err
	}
	return a.executeApply(plan, dry)
}

func prepareApply(
	paths config.Paths,
	cfg config.Config,
	selection applySelection,
	input configurationInput,
	backup bool,
	scripts lifecyclescripts.Catalog,
	dry bool,
) (applyPlan, error) {
	plan := applyPlan{selection: selection}
	var err error

	var home string
	if selection.managedHome || selection.aliases || !scripts.Empty() {
		home, err = config.TargetHome()
		if err != nil {
			return applyPlan{}, err
		}
	}
	if !scripts.Empty() {
		plan.scripts, err = scripts.Prepare(lifecyclescripts.Environment{
			ConfigDir:  paths.Root,
			TargetHome: home,
			Components: strings.Join(selection.values(), ","),
			Profile:    input.profile,
		})
		if err != nil {
			return applyPlan{}, err
		}
	}
	if selection.aliases {
		shell, err := aliases.DetectShell()
		if err != nil {
			return applyPlan{}, err
		}
		aliasSettings := cfg.AliasSettings()
		if !slices.Contains(aliasSettings.Shells, shell) {
			return applyPlan{}, fmt.Errorf("detected shell %q is disabled by aliases.shells", shell)
		}
		plan.aliasPath, err = aliases.OutputPath(aliasSettings.Dir, home, paths.Root, shell)
		if err != nil {
			return applyPlan{}, err
		}
		plan.aliases, err = aliases.Prepare(plan.aliasPath, shell, input.aliases.Aliases)
		if err != nil {
			return applyPlan{}, err
		}
	}
	if selection.managedHome {
		if backup {
			plan.managedHome, err = managedhome.PrepareBackupSelected(
				paths.ManagedHome,
				home,
				paths.Root,
				input.managedHomeFilter,
			)
		} else {
			plan.managedHome, err = managedhome.PrepareSelected(
				paths.ManagedHome,
				home,
				paths.Root,
				input.managedHomeFilter,
			)
		}
		if err != nil {
			return applyPlan{}, err
		}
	}
	if selection.tools {
		plan.tools, err = toolcatalog.Prepare(input.tools, toolcatalog.CurrentPlatform(), dry)
		if err != nil {
			return applyPlan{}, err
		}
	}
	return plan, nil
}

func (a *app) executeApply(plan applyPlan, dry bool) error {
	if dry {
		if err := plan.scripts.Preview(lifecyclescripts.BeforeApply, a.reporter); err != nil {
			return err
		}
	} else if err := plan.scripts.Run(lifecyclescripts.BeforeApply, a.input, a.output, a.errorOutput, a.reporter); err != nil {
		return err
	}
	if plan.selection.tools {
		if err := plan.tools.Apply(a.input, a.output, a.errorOutput, a.reporter); err != nil {
			return err
		}
	}
	if plan.selection.managedHome {
		linked, err := plan.managedHome.Apply(a.reporter, dry)
		if err != nil {
			return err
		}
		if dry {
			if err := a.reportf(
				status.Progress,
				"Managed home links: would create %d, remove %d, leave %d current",
				linked.Created,
				linked.Removed,
				linked.Current,
			); err != nil {
				return err
			}
		} else if err := a.reportf(
			status.Success,
			"Managed home links: %d created, %d removed, %d already current",
			linked.Created,
			linked.Removed,
			linked.Current,
		); err != nil {
			return err
		}
		if linked.BackupID != "" {
			if err := a.reportf(status.Success, "Backup ID: %s", linked.BackupID); err != nil {
				return err
			}
		}
	}
	if plan.selection.aliases {
		if dry {
			if err := a.reportf(status.Progress, "Would render aliases to %s", plan.aliasPath); err != nil {
				return err
			}
		} else {
			if err := plan.aliases.Apply(); err != nil {
				return err
			}
			if err := a.reportf(status.Success, "Rendered aliases to %s", plan.aliasPath); err != nil {
				return err
			}
		}
	}
	if dry {
		return plan.scripts.Preview(lifecyclescripts.AfterApply, a.reporter)
	}
	return plan.scripts.Run(lifecyclescripts.AfterApply, a.input, a.output, a.errorOutput, a.reporter)
}
