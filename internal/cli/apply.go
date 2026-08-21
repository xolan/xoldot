package cli

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
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
	tools       toolcatalog.Catalog
	managedHome managedhome.Plan
	aliases     aliases.Plan
	aliasPath   string
}

func (a *app) applyCommand() *cobra.Command {
	var dry bool
	var only []string
	command := &cobra.Command{
		Use:   "apply",
		Short: "Install tools, link managed home content, and render aliases",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			selection, err := parseApplySelection(only)
			if err != nil {
				return err
			}
			return a.apply(dry, selection)
		},
	}
	command.Flags().BoolVar(&dry, "dry", false, "show what would change without changing it")
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

func (a *app) apply(dry bool, selection applySelection) error {
	paths, err := a.paths()
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	plan, err := prepareApply(paths, cfg, selection)
	if err != nil {
		return err
	}
	return a.executeApply(plan, dry)
}

func prepareApply(paths config.Paths, cfg config.Config, selection applySelection) (applyPlan, error) {
	plan := applyPlan{selection: selection}
	var err error
	if selection.tools {
		plan.tools, err = toolcatalog.Load(paths.Tools)
		if err != nil {
			return applyPlan{}, err
		}
	}

	var home string
	if selection.managedHome || selection.aliases {
		home, err = config.TargetHome()
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
		aliasDir, err := config.ExpandHome(aliasSettings.Dir, home)
		if err != nil {
			return applyPlan{}, err
		}
		file, err := aliases.Load(paths.Aliases)
		if err != nil {
			return applyPlan{}, err
		}
		plan.aliasPath = filepath.Join(aliasDir, "alias."+shell)
		plan.aliases, err = aliases.Prepare(plan.aliasPath, shell, file.Aliases)
		if err != nil {
			return applyPlan{}, err
		}
	}
	if selection.managedHome {
		plan.managedHome, err = managedhome.Prepare(paths.ManagedHome, home, paths.Root)
		if err != nil {
			return applyPlan{}, err
		}
	}
	return plan, nil
}

func (a *app) executeApply(plan applyPlan, dry bool) error {
	if plan.selection.tools {
		if err := toolcatalog.Apply(plan.tools, toolcatalog.CurrentPlatform(), a.input, a.output, a.errorOutput, a.reporter, dry); err != nil {
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
	}
	if !plan.selection.aliases {
		return nil
	}
	if dry {
		return a.reportf(status.Progress, "Would render aliases to %s", plan.aliasPath)
	}
	if err := plan.aliases.Apply(); err != nil {
		return err
	}
	return a.reportf(status.Success, "Rendered aliases to %s", plan.aliasPath)
}
