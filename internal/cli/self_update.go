package cli

import (
	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/selfupdate"
)

func (a *app) selfUpdateCommand(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "self-update",
		Short: "Update xoldot itself",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return (selfupdate.Updater{
				Version:  version,
				Stdout:   a.output,
				Stderr:   a.errorOutput,
				Reporter: a.reporter,
				Verbose:  a.verbose,
			}).Update(command.Context())
		},
	}
}
