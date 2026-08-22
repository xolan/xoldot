package cli

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/lifecyclescripts"
	"github.com/xolan/xoldot/internal/managedhome"
	agentskills "github.com/xolan/xoldot/internal/skills"
)

type machineInspection struct {
	managedHome   []managedhome.Entry
	backups       []managedhome.BackupInspection
	alias         aliases.Inspection
	skills        []agentskills.Inspection
	beforeScripts []lifecyclescripts.Entry
	afterScripts  []lifecyclescripts.Entry
	tools         int
}

type machineInputs struct {
	configurationInput
	paths     config.Paths
	home      string
	shell     string
	aliasPath string
	scripts   lifecyclescripts.Inspection
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
		Short: "Show managed home, alias, and lifecycle script changes without applying them",
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
	scriptCatalog, err := lifecyclescripts.Load(paths.Root, paths.Scripts)
	if err != nil {
		return machineInputs{}, err
	}
	scriptInspection, err := scriptCatalog.Inspect(home)
	if err != nil {
		return machineInputs{}, err
	}
	return machineInputs{
		configurationInput: input,
		paths:              paths,
		home:               home,
		shell:              shell,
		aliasPath:          aliasPath,
		scripts:            scriptInspection,
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
		managedHome:   managedHomeInspection.Entries(),
		backups:       backupInspection,
		alias:         aliasInspection,
		skills:        skillInspection,
		beforeScripts: inputs.scripts.Eligible(lifecyclescripts.BeforeApply),
		afterScripts:  inputs.scripts.Eligible(lifecyclescripts.AfterApply),
		tools:         len(inputs.tools.Tools),
	}, nil
}

func (a *app) machineStatus(profile string) error {
	inspection, err := a.inspectMachine(profile)
	if err != nil {
		return err
	}
	if err := writef(a.output, "%s\n", a.style.heading("Managed home:")); err != nil {
		return err
	}
	if len(inspection.managedHome) == 0 {
		if err := writef(a.output, "  %s\n", a.style.muted("no content declared")); err != nil {
			return err
		}
	}
	for _, item := range inspection.managedHome {
		if item.State == managedhome.StateConflict {
			if item.EligibleForBackup {
				if err := writef(a.output, "  %s %s: %s\n", a.style.warning("eligible backup conflict"), item.Target, item.Problem); err != nil {
					return err
				}
				continue
			}
			if err := writef(a.output, "  %s %s: %s\n", a.style.warning("conflict"), item.Target, item.Problem); err != nil {
				return err
			}
			continue
		}
		state := styleManagedHomeState(a.style, item.State)
		if err := writef(a.output, "  %s %s -> %s\n", state, item.Target, item.Destination); err != nil {
			return err
		}
	}
	if len(inspection.backups) > 0 {
		if err := writef(a.output, "%s\n", a.style.heading("Backups:")); err != nil {
			return err
		}
		for _, backup := range inspection.backups {
			state := styleBackupState(a.style, backup.State)
			if backup.Problem == "" {
				if err := writef(a.output, "  %s %s\n", state, backup.ID); err != nil {
					return err
				}
				continue
			}
			if err := writef(a.output, "  %s %s: %s\n", state, backup.ID, backup.Problem); err != nil {
				return err
			}
		}
	}
	switch inspection.alias.State {
	case aliases.StateConflict:
		if err := writef(a.output, "%s\n  %s: %s", a.style.heading("Aliases:"), a.style.warning("conflict"), inspection.alias.Problem); err != nil {
			return err
		}
	default:
		state := styleAliasState(a.style, inspection.alias.State)
		if err := writef(a.output, "%s\n  %s %s", a.style.heading("Aliases:"), state, inspection.alias.Path); err != nil {
			return err
		}
	}
	if err := writef(a.output, "\n%s\n", a.style.heading("Skills:")); err != nil {
		return err
	}
	if len(inspection.skills) == 0 {
		if err := writef(a.output, "  %s\n", a.style.muted("none declared")); err != nil {
			return err
		}
	}
	for _, skill := range inspection.skills {
		if skill.State == agentskills.InspectionProblem {
			if err := writef(a.output, "  %s %s: %s\n", a.style.failure("problem"), skill.Name, skill.Problem); err != nil {
				return err
			}
			continue
		}
		state := string(skill.State)
		if skill.State == agentskills.InspectionCurrent {
			state = a.style.success(state)
		}
		if err := writef(a.output, "  %s %s\n", state, skill.Name); err != nil {
			return err
		}
	}
	toolNoun := "tools"
	if inspection.tools == 1 {
		toolNoun = "tool"
	}
	if err := writef(
		a.output,
		"%s\n  %s %d declared %s; checks were not run because status is read-only and tool checks are user-authored commands\n",
		a.style.heading("Tools:"),
		a.style.muted("unchecked"),
		inspection.tools,
		toolNoun,
	); err != nil {
		return err
	}
	if len(inspection.beforeScripts) == 0 && len(inspection.afterScripts) == 0 {
		return nil
	}
	if err := writef(a.output, "%s\n", a.style.heading("Lifecycle scripts:")); err != nil {
		return err
	}
	for _, scripts := range [][]lifecyclescripts.Entry{inspection.beforeScripts, inspection.afterScripts} {
		for _, script := range scripts {
			if err := writef(a.output, "  %s %s\n", a.style.progress("would run"), script.Path); err != nil {
				return err
			}
		}
	}
	return nil
}

func styleManagedHomeState(style styler, state managedhome.State) string {
	switch state {
	case managedhome.StateCurrent:
		return style.success(string(state))
	case managedhome.StateMissing:
		return style.progress(string(state))
	case managedhome.StateStale:
		return style.warning(string(state))
	default:
		return string(state)
	}
}

func styleBackupState(style styler, state managedhome.BackupState) string {
	switch state {
	case managedhome.BackupReady:
		return style.success(string(state))
	case managedhome.BackupIncomplete:
		return style.warning(string(state))
	case managedhome.BackupInvalid:
		return style.failure(string(state))
	default:
		return string(state)
	}
}

func styleAliasState(style styler, state aliases.State) string {
	switch state {
	case aliases.StateCurrent:
		return style.success(string(state))
	case aliases.StateMissing:
		return style.progress(string(state))
	case aliases.StateReplaceable:
		return style.warning(string(state))
	default:
		return string(state)
	}
}

func (a *app) machineDiff(profile string) error {
	inspection, err := a.inspectMachine(profile)
	if err != nil {
		return err
	}
	reported := false
	for _, script := range inspection.beforeScripts {
		reported = true
		if err := writef(a.output, "%s\n", a.style.progressPlan(fmt.Sprintf("Would run lifecycle script %s", script.Path))); err != nil {
			return err
		}
	}
	for _, item := range inspection.managedHome {
		if description := item.PlanDescription(); description != "" {
			reported = true
			stylePlan := a.style.progressPlan
			if item.State == managedhome.StateConflict {
				stylePlan = a.style.warningPlan
			}
			if err := writef(a.output, "%s\n", stylePlan(description)); err != nil {
				return err
			}
		}
	}
	switch inspection.alias.State {
	case aliases.StateMissing:
		reported = true
		if err := writef(a.output, "%s\n", a.style.progressPlan(fmt.Sprintf("Would create alias output %s", inspection.alias.Path))); err != nil {
			return err
		}
	case aliases.StateReplaceable:
		reported = true
		if err := write(a.output, a.style.unifiedDiff(inspection.alias.UnifiedDiff())); err != nil {
			return err
		}
	case aliases.StateConflict:
		reported = true
		if err := writef(a.output, "%s\n", a.style.warningPlan("Conflict: "+inspection.alias.Problem)); err != nil {
			return err
		}
	}
	for _, script := range inspection.afterScripts {
		reported = true
		if err := writef(a.output, "%s\n", a.style.progressPlan(fmt.Sprintf("Would run lifecycle script %s", script.Path))); err != nil {
			return err
		}
	}
	if !reported {
		return writef(a.output, "%s\n", a.style.success("No managed home or Alias changes."))
	}
	return nil
}
