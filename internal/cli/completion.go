package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

type completionGenerator struct {
	shell    string
	generate func(*cobra.Command, io.Writer) error
}

var completionGenerators = []completionGenerator{
	{
		shell: "bash",
		generate: func(root *cobra.Command, output io.Writer) error {
			return root.GenBashCompletionV2(output, true)
		},
	},
	{
		shell: "zsh",
		generate: func(root *cobra.Command, output io.Writer) error {
			return root.GenZshCompletion(output)
		},
	},
	{
		shell: "fish",
		generate: func(root *cobra.Command, output io.Writer) error {
			return root.GenFishCompletion(output, true)
		},
	},
}

func completionShellNames() []string {
	shells := make([]string, len(completionGenerators))
	for index, generator := range completionGenerators {
		shells[index] = generator.shell
	}
	return shells
}

func completionShellList(conjunction string) string {
	shells := completionShellNames()
	return strings.Join(shells[:len(shells)-1], ", ") + ", " + conjunction + " " + shells[len(shells)-1]
}

func findCompletionGenerator(shell string) (completionGenerator, bool) {
	for _, generator := range completionGenerators {
		if generator.shell == shell {
			return generator, true
		}
	}
	return completionGenerator{}, false
}

func (a *app) completionCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:       "completion <shell>",
		Short:     "Generate a shell completion script",
		ValidArgs: completionShellNames(),
		Args: func(_ *cobra.Command, arguments []string) error {
			if len(arguments) != 1 {
				return fmt.Errorf("completion requires exactly one shell: %s", completionShellList("or"))
			}
			return nil
		},
		RunE: func(_ *cobra.Command, arguments []string) error {
			generator, found := findCompletionGenerator(arguments[0])
			if !found {
				return fmt.Errorf(
					"unsupported shell %q; supported shells are %s",
					arguments[0],
					completionShellList("and"),
				)
			}
			return generator.generate(root, a.output)
		},
	}
}
