package cli

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/managedhome"
	agentskills "github.com/xolan/xoldot/internal/skills"
	toolcatalog "github.com/xolan/xoldot/internal/tools"
)

type machineInspection struct {
	managedHome []managedhome.Entry
	alias       aliases.Inspection
	skills      []agentskills.Inspection
	tools       int
}

type machineInputs struct {
	paths     config.Paths
	tools     toolcatalog.Catalog
	home      string
	shell     string
	aliasFile aliases.File
	aliasPath string
}

func (a *app) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Inspect the current machine without changing it",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.machineStatus()
		},
	}
}

func (a *app) diffCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "diff",
		Short: "Show managed home and alias changes without applying them",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.machineDiff()
		},
	}
}

func (a *app) loadMachineInputs() (machineInputs, error) {
	paths, err := a.paths()
	if err != nil {
		return machineInputs{}, err
	}
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return machineInputs{}, err
	}
	catalog, err := toolcatalog.Load(paths.Tools)
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
	aliasDir, err := config.ExpandHome(aliasSettings.Dir, home)
	if err != nil {
		return machineInputs{}, err
	}
	file, err := aliases.Load(paths.Aliases)
	if err != nil {
		return machineInputs{}, err
	}
	aliasPath := filepath.Join(aliasDir, "alias."+shell)
	return machineInputs{
		paths:     paths,
		tools:     catalog,
		home:      home,
		shell:     shell,
		aliasFile: file,
		aliasPath: aliasPath,
	}, nil
}

func (a *app) inspectMachine() (machineInspection, error) {
	inputs, err := a.loadMachineInputs()
	if err != nil {
		return machineInspection{}, err
	}
	aliasInspection, err := aliases.Inspect(inputs.aliasPath, inputs.shell, inputs.aliasFile.Aliases)
	if err != nil {
		return machineInspection{}, err
	}
	managedHomeInspection, err := managedhome.Inspect(inputs.paths.ManagedHome, inputs.home, inputs.paths.Root)
	if err != nil {
		return machineInspection{}, err
	}
	skillInspection, err := (agentskills.Manager{
		CatalogPath: inputs.paths.Skills,
		ManagedHome: inputs.paths.ManagedHome,
	}).Inspect()
	if err != nil {
		return machineInspection{}, err
	}
	return machineInspection{
		managedHome: managedHomeInspection.Entries(),
		alias:       aliasInspection,
		skills:      skillInspection,
		tools:       len(inputs.tools.Tools),
	}, nil
}

func (a *app) machineStatus() error {
	inspection, err := a.inspectMachine()
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
			if err := writef(a.output, "  conflict %s: %s\n", item.Target, item.Problem); err != nil {
				return err
			}
			continue
		}
		if err := writef(a.output, "  %s %s -> %s\n", item.State, item.Target, item.Destination); err != nil {
			return err
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

func (a *app) machineDiff() error {
	inspection, err := a.inspectMachine()
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
