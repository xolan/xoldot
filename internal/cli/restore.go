package cli

import (
	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/managedhome"
	"github.com/xolan/xoldot/internal/status"
)

func (a *app) restoreCommand() *cobra.Command {
	var dry bool
	command := &cobra.Command{
		Use:   "restore <backup-id>",
		Short: "Restore every conflict saved by one backup run",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, arguments []string) error {
			return a.restore(arguments[0], dry)
		},
	}
	command.Flags().BoolVar(&dry, "dry", false, "show what would change without changing it")
	return command
}

func (a *app) restore(id string, dry bool) error {
	paths, err := a.paths()
	if err != nil {
		return err
	}
	if _, err := config.Load(paths.Config); err != nil {
		return err
	}
	home, err := config.TargetHome()
	if err != nil {
		return err
	}
	plan, err := managedhome.PrepareRestore(id, paths.ManagedHome, home, paths.Root)
	if err != nil {
		return err
	}
	result, err := plan.Apply(a.reporter, dry)
	if err != nil {
		return err
	}
	if dry {
		return a.reportf(status.Progress, "Would restore %d managed-home conflicts from backup %s", result.Restored, id)
	}
	return a.reportf(status.Success, "Restored %d managed-home conflicts from backup %s", result.Restored, id)
}
