package validation

import (
	"errors"
	"fmt"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/lifecyclescripts"
	"github.com/xolan/xoldot/internal/managedhome"
	"github.com/xolan/xoldot/internal/profiles"
	agentskills "github.com/xolan/xoldot/internal/skills"
	toolcatalog "github.com/xolan/xoldot/internal/tools"
)

type Kind uint8

const (
	Configuration Kind = iota
	Tools
	Aliases
	Skills
	Profiles
	ManagedHome
	LifecycleScripts
	AliasShells
)

type Finding struct {
	Kind    Kind
	Message string
	Remedy  string
}

type Report struct {
	findings []Finding
}

func Check(paths config.Paths) Report {
	var findings []Finding

	configuration, configErr := config.Load(paths.Config)
	if configErr != nil {
		findings = append(findings, Finding{
			Kind:    Configuration,
			Message: configErr.Error(),
			Remedy:  fmt.Sprintf("Edit %s so it matches the documented xoldot.toml format.", paths.Config),
		})
	}
	if _, err := toolcatalog.Load(paths.Tools); err != nil {
		findings = append(findings, Finding{
			Kind:    Tools,
			Message: err.Error(),
			Remedy:  fmt.Sprintf("Fix %s so every Tool has a unique name and a non-empty check command.", paths.Tools),
		})
	}
	if _, err := aliases.Load(paths.Aliases); err != nil {
		findings = append(findings, Finding{
			Kind:    Aliases,
			Message: err.Error(),
			Remedy:  fmt.Sprintf("Fix %s so every Alias has a valid, unique name and a non-empty command.", paths.Aliases),
		})
	}
	if _, err := agentskills.Load(paths.Skills); err != nil {
		findings = append(findings, Finding{
			Kind:    Skills,
			Message: err.Error(),
			Remedy:  fmt.Sprintf("Fix %s so every Skill has a valid name, source, digest, and ownership record.", paths.Skills),
		})
	}
	if err := profiles.Validate(paths); err != nil {
		var catalogError *profiles.CatalogError
		if !errors.As(err, &catalogError) {
			findings = append(findings, Finding{
				Kind:    Profiles,
				Message: err.Error(),
				Remedy:  fmt.Sprintf("Fix the Profile declarations under %s.", paths.Profiles),
			})
		}
	}
	if err := managedhome.Validate(paths.ManagedHome, paths.Root); err != nil {
		findings = append(findings, Finding{
			Kind:    ManagedHome,
			Message: err.Error(),
			Remedy:  fmt.Sprintf("Fix the managed home declarations under %s.", paths.ManagedHome),
		})
	}
	if _, err := lifecyclescripts.Load(paths.Root, paths.Scripts); err != nil {
		findings = append(findings, Finding{
			Kind:    LifecycleScripts,
			Message: err.Error(),
			Remedy:  fmt.Sprintf("Fix lifecycle script names, paths, and permissions under %s.", paths.Scripts),
		})
	}
	if configErr == nil {
		for _, shell := range configuration.AliasSettings().Shells {
			if aliases.SupportsShell(shell) {
				continue
			}
			findings = append(findings, Finding{
				Kind:    AliasShells,
				Message: fmt.Sprintf("unsupported configured shell %q; supported shells are bash, zsh, and fish", shell),
				Remedy:  fmt.Sprintf("Remove %q from aliases.shells in %s.", shell, paths.Config),
			})
		}
	}
	return Report{findings: findings}
}

func (report Report) Findings() []Finding {
	return append([]Finding(nil), report.findings...)
}

func (report Report) ErrorCount() int {
	return len(report.findings)
}

func (report Report) Err() error {
	if len(report.findings) == 0 {
		return nil
	}
	return Failure{Errors: len(report.findings)}
}

type Failure struct {
	Errors int
}

func (failure Failure) Error() string {
	noun := "errors"
	if failure.Errors == 1 {
		noun = "error"
	}
	return fmt.Sprintf("configuration validation found %d %s", failure.Errors, noun)
}
