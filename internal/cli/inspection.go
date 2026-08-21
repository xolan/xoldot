package cli

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/managedhome"
	agentskills "github.com/xolan/xoldot/internal/skills"
)

type machineInspection struct {
	managedHome []managedhome.Entry
	backups     []managedhome.BackupInspection
	alias       aliases.Inspection
	skills      []agentskills.Inspection
	tools       int
}

type machineInputs struct {
	configurationInput
	paths     config.Paths
	home      string
	shell     string
	aliasPath string
}

func (a *app) statusCommand() *cobra.Command {
	var profile string
	command := &cobra.Command{
		Use:   "status",
		Short: "Inspect the current machine without changing it",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.machineStatus(profile)
		},
	}
	command.Flags().StringVar(&profile, "profile", "", "inspect one selected profile")
	return command
}

func (a *app) diffCommand() *cobra.Command {
	var profile string
	command := &cobra.Command{
		Use:   "diff",
		Short: "Show managed home and alias changes without applying them",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.machineDiff(profile)
		},
	}
	command.Flags().StringVar(&profile, "profile", "", "show changes for one selected profile")
	return command
}

func (a *app) loadMachineInputs(profile string) (machineInputs, error) {
	paths, err := a.paths()
	if err != nil {
		return machineInputs{}, err
	}
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return machineInputs{}, err
	}
	input, err := loadConfigurationInput(paths, profile, configurationNeeds{tools: true, aliases: true, skills: true})
	if err != nil {
		return machineInputs{}, err
	}
	home, err := config.TargetHome()
	if err != nil {
		return machineInputs{}, err
	}
	shell, err := aliases.DetectShell()
	if err != nil {
		return machineInputs{}, err
	}
	aliasSettings := cfg.AliasSettings()
	if !slices.Contains(aliasSettings.Shells, shell) {
		return machineInputs{}, fmt.Errorf("detected shell %q is disabled by aliases.shells", shell)
	}
	aliasPath, err := aliases.OutputPath(aliasSettings.Dir, home, paths.Root, shell)
	if err != nil {
		return machineInputs{}, err
	}
	return machineInputs{
		configurationInput: input,
		paths:              paths,
		home:               home,
		shell:              shell,
		aliasPath:          aliasPath,
	}, nil
}

func (a *app) inspectMachine(profile string) (machineInspection, error) {
	inputs, err := a.loadMachineInputs(profile)
	if err != nil {
		return machineInspection{}, err
	}
	aliasInspection, err := aliases.Inspect(inputs.aliasPath, inputs.shell, inputs.aliases.Aliases)
	if err != nil {
		return machineInspection{}, err
	}
	managedHomeInspection, err := managedhome.InspectSelected(
		inputs.paths.ManagedHome,
		inputs.home,
		inputs.paths.Root,
		inputs.managedHomeFilter,
	)
	if err != nil {
		return machineInspection{}, err
	}
	backupInspection, err := managedhome.InspectBackups(inputs.paths.ManagedHome, inputs.home, inputs.paths.Root)
	if err != nil {
		return machineInspection{}, err
	}
	manager := agentskills.Manager{
		CatalogPath: inputs.paths.Skills,
		ManagedHome: inputs.paths.ManagedHome,
	}
	skillInspection, err := manager.InspectCatalog(inputs.skills)
	if err != nil {
		return machineInspection{}, err
	}
	return machineInspection{
		managedHome: managedHomeInspection.Entries(),
		backups:     backupInspection,
		alias:       aliasInspection,
		skills:      skillInspection,
		tools:       len(inputs.tools.Tools),
	}, nil
}

func (a *app) machineStatus(profile string) error {
	inspection, err := a.inspectMachine(profile)
	if err != nil {
		return err
	}
	if err := write(a.output, "Managed home:\n"); err != nil {
		return err
	}
	if len(inspection.managedHome) == 0 {
		if err := write(a.output, "  no content declared\n"); err != nil {
			return err
		}
	}
	for _, item := range inspection.managedHome {
		if item.State == managedhome.StateConflict {
			if item.EligibleForBackup {
				if err := writef(a.output, "  eligible backup conflict %s: %s\n", item.Target, item.Problem); err != nil {
					return err
				}
				continue
			}
			if err := writef(a.output, "  conflict %s: %s\n", item.Target, item.Problem); err != nil {
				return err
			}
			continue
		}
		if err := writef(a.output, "  %s %s -> %s\n", item.State, item.Target, item.Destination); err != nil {
			return err
		}
	}
	if len(inspection.backups) > 0 {
		if err := write(a.output, "Backups:\n"); err != nil {
			return err
		}
		for _, backup := range inspection.backups {
			if backup.Problem == "" {
				if err := writef(a.output, "  %s %s\n", backup.State, backup.ID); err != nil {
					return err
				}
				continue
			}
			if err := writef(a.output, "  %s %s: %s\n", backup.State, backup.ID, backup.Problem); err != nil {
				return err
			}
		}
	}
	if inspection.alias.State == aliases.StateConflict {
		if err := writef(a.output, "Aliases:\n  conflict: %s", inspection.alias.Problem); err != nil {
			return err
		}
	} else if err := writef(a.output, "Aliases:\n  %s %s", inspection.alias.State, inspection.alias.Path); err != nil {
		return err
	}
	if err := write(a.output, "\nSkills:\n"); err != nil {
		return err
	}
	if len(inspection.skills) == 0 {
		if err := write(a.output, "  none declared\n"); err != nil {
			return err
		}
	}
	for _, skill := range inspection.skills {
		if skill.State == agentskills.InspectionProblem {
			if err := writef(a.output, "  problem %s: %s\n", skill.Name, skill.Problem); err != nil {
				return err
			}
			continue
		}
		if err := writef(a.output, "  current %s\n", skill.Name); err != nil {
			return err
		}
	}
	toolNoun := "tools"
	if inspection.tools == 1 {
		toolNoun = "tool"
	}
	return writef(
		a.output,
		"Tools:\n  unchecked %d declared %s; checks were not run because status is read-only and tool checks are user-authored commands\n",
		inspection.tools,
		toolNoun,
	)
}

func (a *app) machineDiff(profile string) error {
	inspection, err := a.inspectMachine(profile)
	if err != nil {
		return err
	}
	reported := false
	for _, item := range inspection.managedHome {
		if description := item.PlanDescription(); description != "" {
			reported = true
			if err := writef(a.output, "%s\n", description); err != nil {
				return err
			}
		}
	}
	switch inspection.alias.State {
	case aliases.StateMissing:
		reported = true
		if err := writef(a.output, "Would create alias output %s\n", inspection.alias.Path); err != nil {
			return err
		}
	case aliases.StateReplaceable:
		reported = true
		if err := write(a.output, inspection.alias.UnifiedDiff()); err != nil {
			return err
		}
	case aliases.StateConflict:
		reported = true
		if err := writef(a.output, "Conflict: %s\n", inspection.alias.Problem); err != nil {
			return err
		}
	}
	if !reported {
		return write(a.output, "No managed home or Alias changes.\n")
	}
	return nil
}
