package lifecyclescripts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xolan/xoldot/internal/pathutil"
	reportstatus "github.com/xolan/xoldot/internal/status"
)

type Phase string

const (
	BeforeApply Phase = "before-apply"
	AfterApply  Phase = "after-apply"

	verifiedScriptFDPath = "/dev/fd/3"
)

var phases = [...]Phase{BeforeApply, AfterApply}

type mode uint8

const (
	modeAlways mode = iota
	modeOnce
	modeOnChange
)

type script struct {
	path             string
	relative         string
	resolvedRelative string
	digest           string
	identity         os.FileInfo
	mode             mode
	configRoot       string
}

type Catalog struct {
	scripts map[Phase][]script
}

type Environment struct {
	ConfigDir  string
	TargetHome string
	Components string
	Profile    string
}

type Entry struct {
	Path string
}

type Inspection struct {
	scripts map[Phase][]script
	state   scriptState
}

type Plan struct {
	scripts map[Phase][]script
	state   scriptState
	store   stateStore
	env     []string
}

func Load(configurationRoot, scriptsRoot string) (Catalog, error) {
	configurationRoot, err := filepath.Abs(configurationRoot)
	if err != nil {
		return Catalog{}, fmt.Errorf("resolve Configuration directory: %w", err)
	}
	configurationRoot, err = pathutil.ResolveExistingPrefix(configurationRoot)
	if err != nil {
		return Catalog{}, fmt.Errorf("resolve Configuration directory: %w", err)
	}
	scriptsRoot, err = filepath.Abs(scriptsRoot)
	if err != nil {
		return Catalog{}, fmt.Errorf("resolve lifecycle scripts directory: %w", err)
	}
	resolvedScriptsRoot, err := pathutil.ResolveExistingPrefix(scriptsRoot)
	if err != nil {
		return Catalog{}, fmt.Errorf("resolve lifecycle scripts directory: %w", err)
	}
	if !pathutil.Contains(configurationRoot, resolvedScriptsRoot) {
		return Catalog{}, fmt.Errorf("lifecycle scripts directory %s resolves outside the Configuration directory %s", scriptsRoot, configurationRoot)
	}

	catalog := Catalog{scripts: make(map[Phase][]script, len(phases))}
	for _, phase := range phases {
		loaded, err := loadPhase(scriptsRoot, resolvedScriptsRoot, phase)
		if err != nil {
			return Catalog{}, err
		}
		catalog.scripts[phase] = loaded
	}
	return catalog, nil
}

func loadPhase(scriptsRoot, resolvedScriptsRoot string, phase Phase) ([]script, error) {
	directory := filepath.Join(scriptsRoot, string(phase))
	resolvedDirectory, err := pathutil.ResolveExistingPrefix(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve lifecycle script phase %s: %w", directory, err)
	}
	if !pathutil.Contains(resolvedScriptsRoot, resolvedDirectory) {
		return nil, fmt.Errorf("lifecycle script phase %s resolves outside %s", directory, scriptsRoot)
	}
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lifecycle script phase %s: %w", directory, err)
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})

	loaded := make([]script, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		mode, err := parseMode(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("validate lifecycle script %s: %w", path, err)
		}
		inspected, err := inspectScript(path, resolvedScriptsRoot)
		if err != nil {
			return nil, err
		}
		inspected.relative = filepath.ToSlash(filepath.Join(string(phase), entry.Name()))
		inspected.mode = mode
		loaded = append(loaded, inspected)
	}
	return loaded, nil
}

func parseMode(name string) (mode, error) {
	switch {
	case strings.HasPrefix(name, "run_once_"):
		return modeOnce, nil
	case strings.HasPrefix(name, "run_onchange_"):
		return modeOnChange, nil
	case strings.HasPrefix(name, "run_"):
		return modeAlways, nil
	default:
		return 0, fmt.Errorf("filename must start with run_, run_once_, or run_onchange_")
	}
}

func inspectScript(path, resolvedScriptsRoot string) (script, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return script{}, fmt.Errorf("resolve lifecycle script %s: %w", path, err)
	}
	if !pathutil.Contains(resolvedScriptsRoot, resolved) {
		return script{}, fmt.Errorf("lifecycle script %s resolves outside the scripts directory", path)
	}
	file, err := os.Open(resolved)
	if err != nil {
		return script{}, fmt.Errorf("open lifecycle script %s: %w", path, err)
	}
	inspected, inspectErr := inspectOpenScript(file, path)
	closeErr := file.Close()
	if inspectErr != nil {
		return script{}, inspectErr
	}
	if closeErr != nil {
		return script{}, fmt.Errorf("close lifecycle script %s: %w", path, closeErr)
	}
	inspected.path = path
	inspected.configRoot = resolvedScriptsRoot
	resolvedRelative, err := filepath.Rel(resolvedScriptsRoot, resolved)
	if err != nil {
		return script{}, fmt.Errorf("map lifecycle script %s into scripts directory: %w", path, err)
	}
	inspected.resolvedRelative = filepath.ToSlash(resolvedRelative)
	return inspected, nil
}

func (catalog Catalog) Empty() bool {
	for _, phase := range phases {
		if len(catalog.scripts[phase]) > 0 {
			return false
		}
	}
	return true
}

func (catalog Catalog) Inspect(targetHome string) (Inspection, error) {
	if catalog.Empty() {
		return Inspection{}, nil
	}
	_, state, err := inspectStateStore(targetHome)
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{
		scripts: catalog.scripts,
		state:   state,
	}, nil
}

func (inspection Inspection) Eligible(phase Phase) []Entry {
	return eligibleEntries(inspection.scripts[phase], inspection.state)
}

func (catalog Catalog) Prepare(environment Environment) (Plan, error) {
	if catalog.Empty() {
		return Plan{}, nil
	}
	if err := validateEnvironment(environment); err != nil {
		return Plan{}, err
	}
	store, state, err := inspectStateStore(environment.TargetHome)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		scripts: catalog.scripts,
		state:   state,
		store:   store,
		env:     scriptEnvironment(environment),
	}, nil
}

func validateEnvironment(environment Environment) error {
	for _, variable := range []struct {
		name  string
		value string
	}{
		{name: "XOLDOT_CONFIG_DIR", value: environment.ConfigDir},
		{name: "XOLDOT_TARGET_HOME", value: environment.TargetHome},
		{name: "XOLDOT_APPLY_COMPONENTS", value: environment.Components},
	} {
		if variable.value == "" {
			return fmt.Errorf("lifecycle script environment %s cannot be empty", variable.name)
		}
	}
	return nil
}

func (plan Plan) Eligible(phase Phase) []Entry {
	return eligibleEntries(plan.scripts[phase], plan.state)
}

func eligibleEntries(scripts []script, state scriptState) []Entry {
	var entries []Entry
	for _, script := range scripts {
		if script.eligible(state) {
			entries = append(entries, Entry{Path: script.relative})
		}
	}
	return entries
}

func (script script) eligible(state scriptState) bool {
	previous, exists := state[script.relative]
	switch script.mode {
	case modeAlways:
		return true
	case modeOnce:
		return !exists
	case modeOnChange:
		return !exists || previous != script.digest
	default:
		return false
	}
}

func (plan Plan) Preview(phase Phase, reporter reportstatus.Reporter) error {
	for _, entry := range plan.Eligible(phase) {
		if err := reportf(reporter, "Would run lifecycle script %s", entry.Path); err != nil {
			return err
		}
	}
	return nil
}

func (plan *Plan) Run(
	phase Phase,
	stdin io.Reader,
	stdout, stderr io.Writer,
	reporter reportstatus.Reporter,
) error {
	for _, prepared := range plan.scripts[phase] {
		if prepared.mode == modeAlways {
			if err := plan.runScript(prepared, stdin, stdout, stderr, reporter); err != nil {
				return err
			}
			continue
		}

		state, err := plan.store.withLockedState(func(transaction *stateTransaction) error {
			if !prepared.eligible(transaction.state) {
				return nil
			}
			if err := plan.runScript(prepared, stdin, stdout, stderr, reporter); err != nil {
				return err
			}
			transaction.recordSuccess(prepared.relative, prepared.digest)
			return nil
		})
		plan.state = state
		if err != nil {
			return err
		}
	}
	return nil
}

func (plan Plan) runScript(
	prepared script,
	stdin io.Reader,
	stdout, stderr io.Writer,
	reporter reportstatus.Reporter,
) error {
	file, err := openVerifiedScript(prepared)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if err := reportf(reporter, "Running lifecycle script %s", prepared.relative); err != nil {
		return err
	}
	// The first ExtraFiles entry is inherited as file descriptor 3.
	command := exec.Command(verifiedScriptFDPath)
	command.Args[0] = prepared.path
	command.ExtraFiles = []*os.File{file}
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	command.Env = plan.env
	if err := command.Run(); err != nil {
		return fmt.Errorf("run lifecycle script %s: %w", prepared.relative, err)
	}
	return nil
}

func openVerifiedScript(prepared script) (*os.File, error) {
	resolved, err := filepath.EvalSymlinks(prepared.path)
	if err != nil {
		return nil, fmt.Errorf("resolve lifecycle script %s: %w", prepared.path, err)
	}
	if !pathutil.Contains(prepared.configRoot, resolved) {
		return nil, fmt.Errorf("lifecycle script %s resolves outside the scripts directory", prepared.path)
	}
	currentResolvedRelative, err := filepath.Rel(prepared.configRoot, resolved)
	if err != nil || filepath.ToSlash(currentResolvedRelative) != prepared.resolvedRelative {
		return nil, fmt.Errorf("lifecycle script %s changed after preparation", prepared.path)
	}
	root, err := os.OpenRoot(prepared.configRoot)
	if err != nil {
		return nil, fmt.Errorf("open lifecycle scripts directory %s: %w", prepared.configRoot, err)
	}
	file, err := root.Open(filepath.FromSlash(prepared.resolvedRelative))
	closeErr := root.Close()
	if err != nil {
		return nil, fmt.Errorf("open lifecycle script %s: %w", prepared.path, err)
	}
	if closeErr != nil {
		_ = file.Close()
		return nil, fmt.Errorf("close lifecycle scripts directory %s: %w", prepared.configRoot, closeErr)
	}
	current, err := inspectOpenScript(file, prepared.path)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !os.SameFile(prepared.identity, current.identity) || prepared.digest != current.digest {
		_ = file.Close()
		return nil, fmt.Errorf("lifecycle script %s changed after preparation", prepared.path)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("rewind lifecycle script %s: %w", prepared.path, err)
	}
	return file, nil
}

func inspectOpenScript(file *os.File, path string) (script, error) {
	info, err := file.Stat()
	if err != nil {
		return script{}, fmt.Errorf("inspect lifecycle script %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return script{}, fmt.Errorf("lifecycle script %s is not an ordinary file", path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return script{}, fmt.Errorf("lifecycle script %s is not executable", path)
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return script{}, fmt.Errorf("read lifecycle script %s: %w", path, err)
	}
	return script{
		digest:   "sha256:" + hex.EncodeToString(hasher.Sum(nil)),
		identity: info,
	}, nil
}

func scriptEnvironment(settings Environment) []string {
	names := map[string]struct{}{
		"XOLDOT":                  {},
		"XOLDOT_CONFIG_DIR":       {},
		"XOLDOT_TARGET_HOME":      {},
		"XOLDOT_APPLY_COMPONENTS": {},
		"XOLDOT_PROFILE":          {},
	}
	environment := make([]string, 0, len(os.Environ())+5)
	for _, value := range os.Environ() {
		name, _, found := strings.Cut(value, "=")
		if _, replaced := names[name]; found && replaced {
			continue
		}
		environment = append(environment, value)
	}
	environment = append(environment,
		"XOLDOT=1",
		"XOLDOT_CONFIG_DIR="+settings.ConfigDir,
		"XOLDOT_TARGET_HOME="+settings.TargetHome,
		"XOLDOT_APPLY_COMPONENTS="+settings.Components,
	)
	if settings.Profile != "" {
		environment = append(environment, "XOLDOT_PROFILE="+settings.Profile)
	}
	return environment
}

func reportf(reporter reportstatus.Reporter, format string, arguments ...any) error {
	if err := reportstatus.Reportf(reporter, reportstatus.Progress, format, arguments...); err != nil {
		return fmt.Errorf("write lifecycle script status: %w", err)
	}
	return nil
}
