package cli

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/lifecyclescripts"
	"github.com/xolan/xoldot/internal/managedhome"
)

const inspectionSchemaVersion = 1

type inspectionFormat string

const (
	inspectionFormatText inspectionFormat = "text"
	inspectionFormatJSON inspectionFormat = "json"
)

var inspectionFormatValues = [...]string{string(inspectionFormatText), string(inspectionFormatJSON)}

type statusJSONDocument struct {
	SchemaVersion    int                  `json:"schema_version"`
	ManagedHome      []managedHomeJSON    `json:"managed_home"`
	Backups          []backupJSON         `json:"backups"`
	Aliases          aliasStatusJSON      `json:"aliases"`
	Skills           []skillJSON          `json:"skills"`
	Tools            toolStatusJSON       `json:"tools"`
	LifecycleScripts lifecycleScriptsJSON `json:"lifecycle_scripts"`
}

type managedHomeJSON struct {
	State             string `json:"state"`
	Target            string `json:"target"`
	Destination       string `json:"destination"`
	Problem           string `json:"problem,omitempty"`
	EligibleForBackup bool   `json:"eligible_for_backup"`
}

type backupJSON struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Problem string `json:"problem,omitempty"`
}

type aliasStatusJSON struct {
	State   string `json:"state"`
	Path    string `json:"path"`
	Problem string `json:"problem,omitempty"`
}

type skillJSON struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Problem string `json:"problem,omitempty"`
}

type toolStatusJSON struct {
	Unchecked int `json:"unchecked"`
}

type lifecycleScriptsJSON struct {
	BeforeApply []scriptJSON `json:"before_apply"`
	AfterApply  []scriptJSON `json:"after_apply"`
}

type scriptJSON struct {
	Path string `json:"path"`
}

type diffJSONDocument struct {
	SchemaVersion    int                   `json:"schema_version"`
	ManagedHome      []managedHomePlanJSON `json:"managed_home"`
	Aliases          aliasPlanJSON         `json:"aliases"`
	LifecycleScripts lifecycleScriptsJSON  `json:"lifecycle_scripts"`
}

type managedHomePlanJSON struct {
	Action            string `json:"action"`
	Target            string `json:"target"`
	Destination       string `json:"destination"`
	Problem           string `json:"problem,omitempty"`
	EligibleForBackup bool   `json:"eligible_for_backup"`
}

type aliasPlanJSON struct {
	Action         string  `json:"action"`
	Path           string  `json:"path"`
	Problem        string  `json:"problem,omitempty"`
	CurrentContent *string `json:"current_content,omitempty"`
	DesiredContent *string `json:"desired_content,omitempty"`
}

func parseInspectionFormat(value string) (inspectionFormat, error) {
	switch inspectionFormat(value) {
	case inspectionFormatText:
		return inspectionFormatText, nil
	case inspectionFormatJSON:
		return inspectionFormatJSON, nil
	default:
		return "", fmt.Errorf("invalid --format %q; use text or json", value)
	}
}

func addInspectionFormatFlag(command *cobra.Command, destination *string) {
	command.Flags().StringVar(destination, "format", string(inspectionFormatText), "output format (text or json)")
	if err := command.RegisterFlagCompletionFunc(
		"format",
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return append([]string(nil), inspectionFormatValues[:]...), cobra.ShellCompDirectiveNoFileComp
		},
	); err != nil {
		panic(fmt.Sprintf("register --format completion: %v", err))
	}
}

func (a *app) machineStatusJSON(profile string) error {
	inspection, err := a.inspectMachine(profile)
	if err != nil {
		return err
	}
	return writeJSON(a.output, newStatusJSONDocument(inspection))
}

func (a *app) machineDiffJSON(profile string) error {
	inspection, err := a.inspectMachine(profile)
	if err != nil {
		return err
	}
	return writeJSON(a.output, newDiffJSONDocument(inspection))
}

func newStatusJSONDocument(inspection machineInspection) statusJSONDocument {
	document := statusJSONDocument{
		SchemaVersion: inspectionSchemaVersion,
		ManagedHome:   make([]managedHomeJSON, 0, len(inspection.managedHome)),
		Backups:       make([]backupJSON, 0, len(inspection.backups)),
		Aliases: aliasStatusJSON{
			State:   string(inspection.alias.State),
			Path:    inspection.alias.Path,
			Problem: inspection.alias.Problem,
		},
		Skills: make([]skillJSON, 0, len(inspection.skills)),
		Tools:  toolStatusJSON{Unchecked: inspection.tools},
		LifecycleScripts: newLifecycleScriptsJSON(
			inspection.beforeScripts,
			inspection.afterScripts,
		),
	}
	for _, entry := range inspection.managedHome {
		document.ManagedHome = append(document.ManagedHome, managedHomeJSON{
			State:             string(entry.State),
			Target:            entry.Target,
			Destination:       entry.Destination,
			Problem:           entry.Problem,
			EligibleForBackup: entry.EligibleForBackup,
		})
	}
	for _, backup := range inspection.backups {
		document.Backups = append(document.Backups, backupJSON{
			ID:      backup.ID,
			State:   string(backup.State),
			Problem: backup.Problem,
		})
	}
	for _, skill := range inspection.skills {
		document.Skills = append(document.Skills, skillJSON{
			Name:    skill.Name,
			State:   string(skill.State),
			Problem: skill.Problem,
		})
	}
	sortStatusJSON(&document)
	return document
}

func newDiffJSONDocument(inspection machineInspection) diffJSONDocument {
	document := diffJSONDocument{
		SchemaVersion: inspectionSchemaVersion,
		ManagedHome:   make([]managedHomePlanJSON, 0, len(inspection.managedHome)),
		Aliases:       newAliasPlanJSON(inspection.alias),
		LifecycleScripts: newLifecycleScriptsJSON(
			inspection.beforeScripts,
			inspection.afterScripts,
		),
	}
	for _, entry := range inspection.managedHome {
		plan := managedHomePlanJSON{
			Target:            entry.Target,
			Destination:       entry.Destination,
			Problem:           entry.Problem,
			EligibleForBackup: entry.EligibleForBackup,
		}
		switch entry.State {
		case managedhome.StateMissing:
			plan.Action = "link"
		case managedhome.StateStale:
			plan.Action = "remove_stale"
		case managedhome.StateConflict:
			plan.Action = "conflict"
		default:
			continue
		}
		document.ManagedHome = append(document.ManagedHome, plan)
	}
	slices.SortFunc(document.ManagedHome, func(left, right managedHomePlanJSON) int {
		if order := cmp.Compare(left.Target, right.Target); order != 0 {
			return order
		}
		return cmp.Compare(left.Action, right.Action)
	})
	return document
}

func newAliasPlanJSON(inspection aliases.Inspection) aliasPlanJSON {
	plan := aliasPlanJSON{Path: inspection.Path}
	switch inspection.State {
	case aliases.StateMissing:
		plan.Action = "create"
	case aliases.StateReplaceable:
		plan.Action = "replace"
		current := inspection.CurrentContent()
		desired := inspection.DesiredContent()
		plan.CurrentContent = &current
		plan.DesiredContent = &desired
	case aliases.StateConflict:
		plan.Action = "conflict"
		plan.Problem = inspection.Problem
	default:
		plan.Action = "none"
	}
	return plan
}

func newLifecycleScriptsJSON(before, after []lifecyclescripts.Entry) lifecycleScriptsJSON {
	scripts := lifecycleScriptsJSON{
		BeforeApply: scriptEntriesJSON(before),
		AfterApply:  scriptEntriesJSON(after),
	}
	slices.SortFunc(scripts.BeforeApply, func(left, right scriptJSON) int {
		return cmp.Compare(left.Path, right.Path)
	})
	slices.SortFunc(scripts.AfterApply, func(left, right scriptJSON) int {
		return cmp.Compare(left.Path, right.Path)
	})
	return scripts
}

func scriptEntriesJSON(entries []lifecyclescripts.Entry) []scriptJSON {
	result := make([]scriptJSON, 0, len(entries))
	for _, entry := range entries {
		result = append(result, scriptJSON{Path: entry.Path})
	}
	return result
}

func sortStatusJSON(document *statusJSONDocument) {
	slices.SortFunc(document.ManagedHome, func(left, right managedHomeJSON) int {
		if order := cmp.Compare(left.Target, right.Target); order != 0 {
			return order
		}
		return cmp.Compare(left.State, right.State)
	})
	slices.SortFunc(document.Backups, func(left, right backupJSON) int {
		return cmp.Compare(left.ID, right.ID)
	})
	slices.SortFunc(document.Skills, func(left, right skillJSON) int {
		return cmp.Compare(left.Name, right.Name)
	})
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
