package cli

import (
	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/doctor"
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
		label := finding.Severity.String() + ":"
		switch finding.Severity {
		case doctor.Error:
			label = a.style.failure(label)
		case doctor.Warning:
			label = a.style.warning(label)
		case doctor.Information:
			label = a.style.progress(label)
		}
		if err := writef(a.output, "%s %s\n", label, finding.Message); err != nil {
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
