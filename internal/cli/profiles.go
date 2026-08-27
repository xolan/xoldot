package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/xolan/xoldot/internal/profiles"
)

func (a *app) profileCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "profile",
		Short: "List and describe configuration profiles",
	}
	show := &cobra.Command{
		Use:   "show <name>",
		Short: "Show the resolved members of one profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, arguments []string) error {
			return a.profileShow(arguments[0])
		},
		ValidArgsFunction: a.completeProfileNames,
	}
	command.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List profiles and their direct parents",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				return a.profileList()
			},
		},
		show,
	)
	return command
}

func (a *app) profileList() error {
	paths, err := a.paths()
	if err != nil {
		return err
	}
	summaries, err := profiles.List(paths)
	if err != nil {
		return err
	}
	for _, summary := range summaries {
		line := summary.Name
		if len(summary.Extends) > 0 {
			line += " extends " + strings.Join(summary.Extends, ", ")
		}
		if err := writef(a.output, "%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) profileShow(name string) error {
	paths, err := a.paths()
	if err != nil {
		return err
	}
	description, err := profiles.Describe(paths, name)
	if err != nil {
		return err
	}
	if err := writef(a.output, "Profile: %s\n", description.Name); err != nil {
		return err
	}
	for _, section := range []struct {
		name    string
		members []string
	}{
		{"Tools", description.Tools},
		{"Aliases", description.Aliases},
		{"Skills", description.Skills},
		{"Managed home", description.ManagedHome},
	} {
		if err := writef(a.output, "%s:\n", section.name); err != nil {
			return err
		}
		for _, member := range section.members {
			if err := writef(a.output, "  %s\n", member); err != nil {
				return err
			}
		}
	}
	return nil
}
