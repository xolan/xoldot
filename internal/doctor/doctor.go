package doctor

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/gitops"
	"github.com/xolan/xoldot/internal/managedhome"
	"github.com/xolan/xoldot/internal/profiles"
	agentskills "github.com/xolan/xoldot/internal/skills"
	toolcatalog "github.com/xolan/xoldot/internal/tools"
)

type Severity uint8

const (
	Error Severity = iota
	Warning
	Information
)

func (severity Severity) String() string {
	switch severity {
	case Error:
		return "error"
	case Warning:
		return "warning"
	case Information:
		return "information"
	default:
		return "unknown"
	}
}

type Finding struct {
	Severity Severity
	Message  string
	Remedy   string
	order    int
}

type Report struct {
	findings []Finding
}

func (report Report) Findings() []Finding {
	findings := append([]Finding(nil), report.findings...)
	slices.SortStableFunc(findings, func(left, right Finding) int {
		if left.Severity != right.Severity {
			return int(left.Severity) - int(right.Severity)
		}
		return left.order - right.order
	})
	return findings
}

func (report Report) ErrorCount() int {
	return report.count(Error)
}

func (report Report) WarningCount() int {
	return report.count(Warning)
}

func (report Report) count(severity Severity) int {
	count := 0
	for _, finding := range report.findings {
		if finding.Severity == severity {
			count++
		}
	}
	return count
}

func (report Report) Err() error {
	if count := report.ErrorCount(); count > 0 {
		return Failure{Errors: count}
	}
	return nil
}

type Failure struct {
	Errors int
}

func (failure Failure) Error() string {
	return "doctor found " + formatCount(failure.Errors, "error")
}

const (
	orderConfiguration = iota
	orderTools
	orderAliases
	orderSkills
	orderProfiles
	orderPaths
	orderLedger
	orderShell
	orderGit
	orderNPX
	orderNode
	orderGitRemote
	orderGitBranch
	orderManagedHome
	orderAliasOutput
	orderSafety
	orderSummary
)

func Check(paths config.Paths) Report {
	return check(paths, defaultRuntime())
}

func check(paths config.Paths, commands runtime) Report {
	checker := checker{paths: paths, commands: commands}
	checker.loadInputs()
	checker.checkPaths()
	checker.checkShell()
	checker.checkManagedHome()
	checker.checkAliasOutput()
	checker.checkGit()
	checker.checkNode()
	return checker.report()
}

type checker struct {
	paths    config.Paths
	commands runtime
	findings []Finding

	configuration config.Config
	configErr     error
	tools         toolcatalog.Catalog
	toolsErr      error
	aliasFile     aliases.File
	aliasesErr    error
	skills        agentskills.Catalog
	skillsErr     error
	resolved      resolvedPaths
	shell         string
	shellErr      error
}

func (checker *checker) add(severity Severity, order int, message, remedy string) {
	checker.findings = append(checker.findings, Finding{
		Severity: severity,
		Message:  message,
		Remedy:   remedy,
		order:    order,
	})
}

func (checker *checker) loadInputs() {
	checker.configuration, checker.configErr = config.Load(checker.paths.Config)
	if checker.configErr != nil {
		checker.add(Error, orderConfiguration, checker.configErr.Error(), fmt.Sprintf("Edit %s so it matches the documented xoldot.toml format.", checker.paths.Config))
	}

	checker.tools, checker.toolsErr = toolcatalog.Load(checker.paths.Tools)
	if checker.toolsErr != nil {
		checker.add(Error, orderTools, checker.toolsErr.Error(), fmt.Sprintf("Fix %s so every Tool has a unique name and a non-empty check command.", checker.paths.Tools))
	}

	checker.aliasFile, checker.aliasesErr = aliases.Load(checker.paths.Aliases)
	if checker.aliasesErr != nil {
		checker.add(Error, orderAliases, checker.aliasesErr.Error(), fmt.Sprintf("Fix %s so every Alias has a valid, unique name and a non-empty command.", checker.paths.Aliases))
	}

	checker.skills, checker.skillsErr = agentskills.Load(checker.paths.Skills)
	if checker.skillsErr != nil {
		checker.add(Error, orderSkills, checker.skillsErr.Error(), fmt.Sprintf("Fix %s so every Skill has a valid name, source, digest, and ownership record.", checker.paths.Skills))
	}

	if err := profiles.Validate(checker.paths); err != nil {
		var catalogError *profiles.CatalogError
		if !errors.As(err, &catalogError) {
			checker.add(Error, orderProfiles, err.Error(), fmt.Sprintf("Fix the Profile declarations under %s, then rerun 'xoldot doctor'.", checker.paths.Profiles))
		}
	}
}

func (checker *checker) checkPaths() {
	var failures []string
	checker.resolved, failures = inspectPaths(checker.paths)
	for _, failure := range failures {
		checker.add(Error, orderPaths, failure, "Choose Configuration and Target home paths that resolve inside their declared roots without placing the Target home inside the Configuration directory.")
	}
}

func (checker *checker) checkShell() {
	checker.shell, checker.shellErr = aliases.DetectShell()
	if checker.configErr == nil {
		for _, configuredShell := range checker.configuration.AliasSettings().Shells {
			if !aliases.SupportsShell(configuredShell) {
				checker.add(Error, orderShell, fmt.Sprintf("unsupported configured shell %q; supported shells are bash, zsh, and fish", configuredShell), fmt.Sprintf("Remove %q from aliases.shells in %s.", configuredShell, checker.paths.Config))
			}
		}
	}
	if checker.shellErr != nil {
		checker.add(Error, orderShell, checker.shellErr.Error(), "Set SHELL or XOLDOT_SHELL to bash, zsh, or fish.")
	} else if checker.configErr == nil && !slices.Contains(checker.configuration.AliasSettings().Shells, checker.shell) {
		checker.add(Error, orderShell, fmt.Sprintf("detected shell %q is disabled by aliases.shells", checker.shell), fmt.Sprintf("Add %q to aliases.shells in %s or select an enabled shell with XOLDOT_SHELL.", checker.shell, checker.paths.Config))
	}
}

func (checker *checker) checkManagedHome() {
	if !checker.resolved.homeOK {
		return
	}
	inspection, err := managedhome.Inspect(checker.paths.ManagedHome, checker.resolved.home, checker.paths.Root)
	if err != nil {
		var ledgerError *managedhome.LedgerError
		if errors.As(err, &ledgerError) {
			checker.add(Error, orderLedger, err.Error(), fmt.Sprintf("Move %s aside, then run 'xoldot apply --only managed-home' to rebuild managed-link state.", ledgerError.Path()))
			return
		}
		checker.add(Error, orderManagedHome, err.Error(), "Fix the reported managed home path or ownership problem, then rerun 'xoldot doctor'.")
		return
	}
	for _, entry := range inspection.Entries() {
		if entry.State == managedhome.StateConflict {
			checker.add(Warning, orderManagedHome, fmt.Sprintf("Managed home conflict at %s: %s", entry.Target, entry.Problem), fmt.Sprintf("Move the existing content at %s aside, then run 'xoldot apply --only managed-home'.", entry.Target))
		}
	}
}

func (checker *checker) checkAliasOutput() {
	if checker.configErr != nil || checker.aliasesErr != nil || !checker.resolved.rootOK || !checker.resolved.homeOK || checker.shellErr != nil || !slices.Contains(checker.configuration.AliasSettings().Shells, checker.shell) {
		return
	}
	aliasPath, err := aliases.OutputPath(checker.configuration.AliasSettings().Dir, checker.resolved.home, checker.resolved.root, checker.shell)
	if err != nil {
		checker.add(Error, orderPaths, err.Error(), fmt.Sprintf("Choose aliases.dir in %s so the Alias output resolves outside the Configuration directory.", checker.paths.Config))
		return
	}
	inspection, err := aliases.Inspect(aliasPath, checker.shell, checker.aliasFile.Aliases)
	if err != nil {
		checker.add(Error, orderAliasOutput, err.Error(), "Fix the reported Alias output path, then rerun 'xoldot doctor'.")
	} else if inspection.State == aliases.StateConflict {
		checker.add(Warning, orderAliasOutput, inspection.Problem, fmt.Sprintf("Move %s aside or restore its last xoldot-generated contents, then run 'xoldot apply --only aliases'.", inspection.Path))
	}
}

func (checker *checker) checkGit() {
	needsGit := checker.configErr == nil && checker.configuration.GitSettings().Enabled
	if checker.skillsErr == nil {
		for _, skill := range checker.skills.Skills {
			if agentskills.SourceNeedsGit(skill.Source) {
				needsGit = true
				break
			}
		}
	}
	gitPath := ""
	if needsGit {
		var err error
		gitPath, err = checker.commands.lookPath("git")
		if err != nil {
			checker.add(Error, orderGit, "git is required by Sync or a saved Skill source but is not available", "Install git and make it available on PATH.")
		}
	}
	if checker.configErr != nil || !checker.configuration.GitSettings().Enabled || gitPath == "" || !checker.resolved.rootOK {
		return
	}
	git := checker.configuration.GitSettings()
	inspection, err := checker.commands.inspectGit(checker.paths.Root, git.Remote, git.Branch, gitPath)
	if err != nil {
		checker.add(Error, orderGitRemote, err.Error(), fmt.Sprintf("Repair the local Git repository in %s, then rerun 'xoldot doctor'.", checker.paths.Root))
		return
	}
	addGitFindings(checker.paths, inspection, git, checker.add)
}

func (checker *checker) checkNode() {
	if checker.skillsErr != nil || len(checker.skills.Skills) == 0 {
		return
	}
	if _, err := checker.commands.lookPath("npx"); err != nil {
		checker.add(Error, orderNPX, "npx is required when the Configuration contains Skills but is not available", fmt.Sprintf("Install Node.js %s or newer with npx, then make npx available on PATH.", agentskills.MinimumNodeVersion))
	}
	nodePath, err := checker.commands.lookPath("node")
	if err != nil {
		checker.add(Error, orderNode, "Node.js is required when the Configuration contains Skills but is not available", fmt.Sprintf("Install Node.js %s or newer and make node available on PATH.", agentskills.MinimumNodeVersion))
		return
	}
	version, err := checker.commands.output(checker.paths.Root, nodePath, "--version")
	if err != nil {
		checker.add(Error, orderNode, fmt.Sprintf("read Node.js version: %v", err), fmt.Sprintf("Repair or replace node with Node.js %s or newer.", agentskills.MinimumNodeVersion))
	} else if !nodeVersionAtLeast(version, agentskills.MinimumNodeVersion) {
		checker.add(Error, orderNode, fmt.Sprintf("Node.js version %q does not meet the required minimum %s", strings.TrimSpace(version), agentskills.MinimumNodeVersion), fmt.Sprintf("Upgrade Node.js to %s or newer.", agentskills.MinimumNodeVersion))
	}
}

func (checker *checker) report() Report {
	declaredTools := 0
	if checker.toolsErr == nil {
		declaredTools = len(checker.tools.Tools)
	}
	checker.add(Information, orderSafety, fmt.Sprintf("Doctor did not run %d user-authored Tool checks, invoke npx, fetch Git remotes, authenticate, install, or change files.", declaredTools), "")

	report := Report{findings: checker.findings}
	errorsFound := report.ErrorCount()
	warningsFound := report.WarningCount()
	if errorsFound == 0 && warningsFound == 0 {
		checker.add(Information, orderSummary, "Doctor found no problems.", "")
	} else {
		checker.add(Information, orderSummary, fmt.Sprintf("Doctor found %s and %s.", formatCount(errorsFound, "error"), formatCount(warningsFound, "warning")), "")
	}
	return Report{findings: checker.findings}
}

func addGitFindings(paths config.Paths, inspection gitops.LocalInspection, settings *config.GitConfig, add func(Severity, int, string, string)) {
	if !inspection.Repository {
		add(Error, orderGitRemote, fmt.Sprintf("Sync is enabled but %s is not a local Git repository", paths.Root), "Run 'xoldot setup' with the configured remote URL to initialize the Configuration repository.")
		return
	}
	if !inspection.Remote {
		add(Error, orderGitRemote, fmt.Sprintf("configured Git remote %q does not exist in %s", settings.Remote, paths.Root), fmt.Sprintf("Add the %q remote to %s or update git.remote in %s.", settings.Remote, paths.Root, paths.Config))
	}
	if !inspection.Branch {
		add(Error, orderGitBranch, fmt.Sprintf("configured Git branch %q does not exist locally in %s", settings.Branch, paths.Root), fmt.Sprintf("Create or check out the %q branch in %s, or update git.branch in %s.", settings.Branch, paths.Root, paths.Config))
	}
}

func formatCount(count int, singular string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %ss", count, singular)
}
