package cli

import (
	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/managedhome"
	"github.com/xolan/xoldot/internal/profiles"
	agentskills "github.com/xolan/xoldot/internal/skills"
	toolcatalog "github.com/xolan/xoldot/internal/tools"
)

type configurationNeeds struct {
	tools   bool
	aliases bool
	skills  bool
}

type configurationInput struct {
	profile           string
	tools             toolcatalog.Catalog
	aliases           aliases.File
	skills            agentskills.Catalog
	managedHomeFilter managedhome.PathFilter
}

func loadConfigurationInput(paths config.Paths, profile string, needs configurationNeeds) (configurationInput, error) {
	if profile != "" {
		selected, err := profiles.Load(paths, profile)
		if err != nil {
			return configurationInput{}, err
		}
		return configurationInput{
			profile:           selected.Name,
			tools:             selected.Tools,
			aliases:           selected.Aliases,
			skills:            selected.Skills,
			managedHomeFilter: selected.ManagedHome.Includes,
		}, nil
	}

	var input configurationInput
	var err error
	if needs.tools {
		input.tools, err = toolcatalog.Load(paths.Tools)
		if err != nil {
			return configurationInput{}, err
		}
	}
	if needs.aliases {
		input.aliases, err = aliases.Load(paths.Aliases)
		if err != nil {
			return configurationInput{}, err
		}
	}
	if needs.skills {
		input.skills, err = agentskills.Load(paths.Skills)
		if err != nil {
			return configurationInput{}, err
		}
	}
	return input, nil
}
