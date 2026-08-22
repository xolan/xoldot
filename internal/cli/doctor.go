package cli

import (
	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/doctor"
	"github.com/xolan/xoldot/internal/status"
)

func (a *app) doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check whether xoldot can use the current Configuration and Machine",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.doctor()
		},
	}
}

func (a *app) doctor() error {
	paths, err := a.paths()
	if err != nil {
		return err
	}
	report := doctor.Check(paths)
	for _, finding := range report.Findings() {
		kind := status.Progress
		switch finding.Severity {
		case doctor.Error:
			kind = status.Error
		case doctor.Warning:
			kind = status.Warning
		}
		decoration, err := decorationForStatus(kind)
		if err != nil {
			return err
		}
		line := formatStatus(a.style, decoration.color, decoration.prefix, finding.Message, false)
		if err := write(a.output, line); err != nil {
			return err
		}
		if finding.Remedy != "" {
			if err := writef(a.output, "  %s %s\n", a.style.heading("remedy:"), finding.Remedy); err != nil {
				return err
			}
		}
	}
	return report.Err()
}
