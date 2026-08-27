package cli

import (
	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/status"
	"github.com/xolan/xoldot/internal/validation"
)

func (a *app) validateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the Configuration without inspecting a Machine",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.validate()
		},
	}
}

func (a *app) validate() error {
	paths, err := a.paths()
	if err != nil {
		return err
	}
	report := validation.Check(paths)
	for _, finding := range report.Findings() {
		if err := a.writeFinding(status.Error, finding.Message, finding.Remedy); err != nil {
			return err
		}
	}
	if err := report.Err(); err != nil {
		return err
	}
	return a.reportf(status.Success, "Configuration is valid")
}
