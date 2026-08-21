package managedhome

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/pathutil"
	reportstatus "github.com/xolan/xoldot/internal/status"
)

type Result struct {
	Created int
	Current int
	Removed int
}

type State string

const (
	StateCurrent  State = "current"
	StateMissing  State = "missing"
	StateStale    State = "stale"
	StateConflict State = "conflict"
)

type Entry struct {
	State       State
	Target      string
	Destination string
	Problem     string
}

func (entry Entry) PlanDescription() string {
	switch entry.State {
	case StateMissing:
		return fmt.Sprintf("Would link %s -> %s", entry.Target, entry.Destination)
	case StateStale:
		return fmt.Sprintf("Would remove stale link %s", entry.Target)
	case StateConflict:
		return fmt.Sprintf("Conflict at %s: %s", entry.Target, entry.Problem)
	default:
		return ""
	}
}

type Inspection struct {
	entries []Entry
}

func (inspection Inspection) Entries() []Entry {
	return append([]Entry(nil), inspection.entries...)
}

type linkLedger struct {
	Version int          `json:"version"`
	Links   []linkRecord `json:"links"`
}

type linkRecord struct {
	Target      string `json:"target"`
	Destination string `json:"destination"`
}

type linkPlan struct {
	linkRecord
	ReplaceLegacySkillDirectory bool
	LegacyLinks                 int
}

type plannedConflict struct {
	entry Entry
	err   error
}

func newPlannedConflict(record linkRecord, problem string, err error) plannedConflict {
	return plannedConflict{
		entry: Entry{
			State:       StateConflict,
			Target:      record.Target,
			Destination: record.Destination,
			Problem:     problem,
		},
		err: err,
	}
}

func newUnownedConflict(record linkRecord) plannedConflict {
	const problem = "target already exists and is not managed by xoldot"
	return newPlannedConflict(record, problem, fmt.Errorf("target %s already exists and is not managed by xoldot", record.Target))
}

type Plan struct {
	ledgerPath string
	previous   linkLedger
	links      []linkPlan
	records    []linkRecord
	stale      []linkRecord
	current    []linkRecord
	conflicts  []plannedConflict
}

func Link(managedRoot, home, configRoot string, reporter reportstatus.Reporter, dry bool) (Result, error) {
	plan, err := Prepare(managedRoot, home, configRoot)
	if err != nil {
		return Result{}, err
	}
	return plan.Apply(reporter, dry)
}

func Prepare(managedRoot, home, configRoot string) (Plan, error) {
	plan, err := prepare(managedRoot, home, configRoot)
	if err != nil {
		return Plan{}, err
	}
	if len(plan.conflicts) > 0 {
		return Plan{}, plan.conflicts[0].err
	}
	return plan, nil
}

func Inspect(managedRoot, home, configRoot string) (Inspection, error) {
	plan, err := prepare(managedRoot, home, configRoot)
	if err != nil {
		return Inspection{}, err
	}
	return plan.inspection(), nil
}

func prepare(managedRoot, home, configRoot string) (Plan, error) {
	managedRoot, err := filepath.Abs(managedRoot)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve managed home: %w", err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve target home: %w", err)
	}
	configRoot, err = filepath.Abs(configRoot)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve config root: %w", err)
	}
	managedRoot, err = pathutil.ResolveExistingPrefix(managedRoot)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve managed home symlinks: %w", err)
	}
	home, err = pathutil.ResolveExistingPrefix(home)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve target home symlinks: %w", err)
	}
	configRoot, err = pathutil.ResolveExistingPrefix(configRoot)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve config root symlinks: %w", err)
	}

	ledgerPath := filepath.Join(home, ".local", "state", "xoldot", "links.json")
	previous, err := loadLedger(ledgerPath)
	if err != nil {
		return Plan{}, err
	}
	if err := validateLedger(previous, home, managedRoot); err != nil {
		return Plan{}, fmt.Errorf("validate managed link state %s: %w", ledgerPath, err)
	}

	var plans []linkPlan
	var records []linkRecord
	var current []linkRecord
	var conflicts []plannedConflict
	err = walkManagedRoot(managedRoot, func(source string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		skillDirectory := entry.IsDir() && isSkillDirectory(managedRoot, source)
		if entry.IsDir() && !skillDirectory {
			return nil
		}
		if skillDirectory {
			if err := validateSkillDirectory(source, managedRoot); err != nil {
				return err
			}
		}
		isSymlink := entry.Type()&os.ModeSymlink != 0

		relative, err := filepath.Rel(managedRoot, source)
		if err != nil {
			return fmt.Errorf("find path for %s: %w", source, err)
		}
		target := filepath.Join(home, relative)
		if target == ledgerPath {
			return fmt.Errorf("managed path %s is reserved for xoldot link state", source)
		}
		destination := source
		if isSymlink {
			destination, err = mappedSymlinkDestination(source, target, managedRoot, home, configRoot)
			if err != nil {
				return err
			}
		}
		resolvedParent, err := pathutil.ResolveExistingPrefix(filepath.Dir(target))
		if err != nil {
			return fmt.Errorf("resolve target directory for %s: %w", target, err)
		}
		resolvedTarget := filepath.Join(resolvedParent, filepath.Base(target))
		if pathutil.Contains(configRoot, resolvedTarget) || pathutil.Contains(resolvedTarget, managedRoot) {
			return fmt.Errorf("refusing recursive link %s -> %s", target, source)
		}
		state, err := linkState(target, destination)
		if err != nil {
			return err
		}
		record := linkRecord{Target: target, Destination: destination}
		switch state {
		case linkConflict:
			if !skillDirectory {
				conflicts = append(conflicts, newUnownedConflict(record))
				break
			}
			legacyLinks, replace, err := legacySkillDirectory(target, previous)
			if err != nil {
				var pathError *os.PathError
				if errors.As(err, &pathError) && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				conflicts = append(conflicts, newPlannedConflict(record, err.Error(), err))
				break
			}
			if !replace {
				conflicts = append(conflicts, newUnownedConflict(record))
				break
			}
			plans = append(plans, linkPlan{
				linkRecord:                  record,
				ReplaceLegacySkillDirectory: true,
				LegacyLinks:                 legacyLinks,
			})
		case linkCurrent:
			current = append(current, record)
		default:
			plans = append(plans, linkPlan{linkRecord: record})
		}
		records = append(records, record)
		if skillDirectory {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return Plan{}, fmt.Errorf("plan managed home links: %w", err)
	}
	currentTargets := make(map[string]struct{}, len(records))
	for _, record := range records {
		currentTargets[record.Target] = struct{}{}
	}
	var replacedSkillDirectories []string
	for _, plan := range plans {
		if plan.ReplaceLegacySkillDirectory {
			replacedSkillDirectories = append(replacedSkillDirectories, plan.Target)
		}
	}
	var stale []linkRecord
	for _, record := range previous.Links {
		if _, exists := currentTargets[record.Target]; exists {
			continue
		}
		if containedByAny(replacedSkillDirectories, record.Target) {
			continue
		}
		owned, err := exactSymlink(record.Target, record.Destination)
		if err != nil {
			return Plan{}, err
		}
		if owned {
			stale = append(stale, record)
		}
	}

	return Plan{
		ledgerPath: ledgerPath,
		previous:   previous,
		links:      plans,
		records:    records,
		stale:      stale,
		current:    current,
		conflicts:  conflicts,
	}, nil
}

func (plan Plan) inspection() Inspection {
	entries := make([]Entry, 0, len(plan.current)+len(plan.links)+len(plan.stale)+len(plan.conflicts))
	for _, record := range plan.current {
		entries = append(entries, Entry{State: StateCurrent, Target: record.Target, Destination: record.Destination})
	}
	entries = append(entries, plan.changes()...)
	for _, conflict := range plan.conflicts {
		entries = append(entries, conflict.entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Target == entries[j].Target {
			return entries[i].State < entries[j].State
		}
		return entries[i].Target < entries[j].Target
	})
	return Inspection{entries: entries}
}

func (plan Plan) changes() []Entry {
	changes := make([]Entry, 0, len(plan.links)+len(plan.stale))
	for _, link := range plan.links {
		changes = append(changes, Entry{State: StateMissing, Target: link.Target, Destination: link.Destination})
	}
	for _, record := range plan.stale {
		changes = append(changes, Entry{State: StateStale, Target: record.Target, Destination: record.Destination})
	}
	return changes
}

func walkManagedRoot(root string, walk fs.WalkDirFunc) error {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(root, walk)
}

func (plan Plan) Apply(reporter reportstatus.Reporter, dry bool) (Result, error) {
	result := Result{Current: len(plan.current)}
	if dry {
		for _, change := range plan.changes() {
			if err := reportf(reporter, "%s", change.PlanDescription()); err != nil {
				return result, err
			}
		}
		result.Created = len(plan.links)
		result.Removed = len(plan.stale)
		for _, link := range plan.links {
			result.Removed += link.LegacyLinks
		}
		return result, nil
	}
	for _, record := range plan.current {
		owned, err := exactSymlink(record.Target, record.Destination)
		if err != nil {
			return result, err
		}
		if !owned {
			return result, fmt.Errorf("target %s changed while applying links", record.Target)
		}
	}

	transaction := linkTransaction{}
	fail := func(err error) (Result, error) {
		return result, errors.Join(err, transaction.rollback())
	}
	for _, link := range plan.links {
		if link.ReplaceLegacySkillDirectory {
			backup, err := backupLegacySkillDirectory(link.Target, plan.previous)
			if err != nil {
				return fail(err)
			}
			transaction.backups = append(transaction.backups, backup)
			result.Removed += link.LegacyLinks
		}
		if err := transaction.makeDirectories(filepath.Dir(link.Target)); err != nil {
			return fail(fmt.Errorf("create target directory for %s: %w", link.Target, err))
		}
		if err := os.Symlink(link.Destination, link.Target); err != nil {
			return fail(fmt.Errorf("link %s to %s: %w", link.Target, link.Destination, err))
		}
		transaction.created = append(transaction.created, link.Target)
		if err := reportf(reporter, "Linked %s -> %s", link.Target, link.Destination); err != nil {
			return fail(err)
		}
		result.Created++
	}
	for _, record := range plan.stale {
		owned, err := exactSymlink(record.Target, record.Destination)
		if err != nil {
			return fail(err)
		}
		if !owned {
			continue
		}
		if err := os.Remove(record.Target); err != nil {
			return fail(fmt.Errorf("remove stale managed link %s: %w", record.Target, err))
		}
		transaction.removed = append(transaction.removed, record)
		if err := reportf(reporter, "Removed stale link %s", record.Target); err != nil {
			return fail(err)
		}
		result.Removed++
	}
	if !slices.Equal(plan.previous.Links, plan.records) {
		if err := transaction.makeDirectories(filepath.Dir(plan.ledgerPath)); err != nil {
			return fail(fmt.Errorf("create managed link state directory: %w", err))
		}
		if err := saveLedger(plan.ledgerPath, plan.records); err != nil {
			return fail(err)
		}
	}
	return result, transaction.commit()
}

func isSkillDirectory(managedRoot, path string) bool {
	parent := filepath.Dir(path)
	return parent == filepath.Join(managedRoot, ".agents", "skills") ||
		parent == filepath.Join(managedRoot, ".claude", "skills")
}

func validateSkillDirectory(root, managedRoot string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		_, err := managedFileSymlinkDestination(path, managedRoot)
		return err
	})
}

func containedByAny(parents []string, path string) bool {
	for _, parent := range parents {
		if pathutil.Contains(parent, path) {
			return true
		}
	}
	return false
}

func legacySkillDirectory(target string, previous linkLedger) (int, bool, error) {
	info, err := os.Lstat(target)
	if err != nil {
		return 0, false, fmt.Errorf("inspect legacy skill directory %s: %w", target, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, false, nil
	}

	owned := make(map[string]string)
	for _, record := range previous.Links {
		if record.Target != target && pathutil.Contains(target, record.Target) {
			owned[record.Target] = record.Destination
		}
	}
	if len(owned) == 0 {
		return 0, false, nil
	}

	links := 0
	err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		destination, exists := owned[path]
		if !exists {
			return fmt.Errorf("legacy skill directory %s contains unowned path %s", target, path)
		}
		exact, err := exactSymlink(path, destination)
		if err != nil {
			return err
		}
		if !exact {
			return fmt.Errorf("legacy skill path %s is no longer managed by xoldot", path)
		}
		links++
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return links, true, nil
}

type legacyBackup struct {
	target    string
	directory string
}

func backupLegacySkillDirectory(target string, previous linkLedger) (legacyBackup, error) {
	_, replace, err := legacySkillDirectory(target, previous)
	if err != nil {
		return legacyBackup{}, err
	}
	if !replace {
		return legacyBackup{}, fmt.Errorf("legacy skill directory %s changed while applying links", target)
	}
	directory, err := os.MkdirTemp(filepath.Dir(target), ".xoldot-link-backup-*")
	if err != nil {
		return legacyBackup{}, fmt.Errorf("create backup for legacy skill directory %s: %w", target, err)
	}
	backup := legacyBackup{target: target, directory: directory}
	if err := os.Rename(target, backup.path()); err != nil {
		_ = os.RemoveAll(directory)
		return legacyBackup{}, fmt.Errorf("back up legacy skill directory %s: %w", target, err)
	}
	return backup, nil
}

func (backup legacyBackup) path() string {
	return filepath.Join(backup.directory, "skill")
}

type linkTransaction struct {
	created            []string
	createdDirectories []string
	removed            []linkRecord
	backups            []legacyBackup
}

func (transaction *linkTransaction) makeDirectories(path string) error {
	var missing []string
	for current := path; ; current = filepath.Dir(current) {
		if _, err := os.Lstat(current); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missing = append(missing, current)
		next := filepath.Dir(current)
		if next == current {
			break
		}
	}
	transaction.createdDirectories = append(transaction.createdDirectories, missing...)
	return os.MkdirAll(path, 0o755)
}

func (transaction linkTransaction) rollback() error {
	var rollbackErrors []error
	for index := len(transaction.created) - 1; index >= 0; index-- {
		if err := os.Remove(transaction.created[index]); err != nil && !errors.Is(err, os.ErrNotExist) {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("roll back link %s: %w", transaction.created[index], err))
		}
	}
	for index := len(transaction.removed) - 1; index >= 0; index-- {
		record := transaction.removed[index]
		if err := os.MkdirAll(filepath.Dir(record.Target), 0o755); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore target directory for %s: %w", record.Target, err))
			continue
		}
		if err := os.Symlink(record.Destination, record.Target); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore stale link %s: %w", record.Target, err))
		}
	}
	for index := len(transaction.backups) - 1; index >= 0; index-- {
		backup := transaction.backups[index]
		if err := os.RemoveAll(backup.target); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("clear replacement link %s: %w", backup.target, err))
			continue
		}
		if err := os.Rename(backup.path(), backup.target); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore legacy skill directory %s: %w", backup.target, err))
			continue
		}
		if err := os.RemoveAll(backup.directory); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	for _, directory := range transaction.createdDirectories {
		if err := os.Remove(directory); err != nil && !errors.Is(err, os.ErrNotExist) {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("roll back directory %s: %w", directory, err))
		}
	}
	return errors.Join(rollbackErrors...)
}

func (transaction linkTransaction) commit() error {
	var cleanupErrors []error
	for _, backup := range transaction.backups {
		if err := os.RemoveAll(backup.directory); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove legacy skill backup: %w", err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func reportf(reporter reportstatus.Reporter, format string, arguments ...any) error {
	if err := reportstatus.Reportf(reporter, reportstatus.Progress, format, arguments...); err != nil {
		return fmt.Errorf("write link status: %w", err)
	}
	return nil
}

type linkStatus uint8

const (
	linkMissing linkStatus = iota
	linkCurrent
	linkConflict
)

func linkState(target, source string) (linkStatus, error) {
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return linkMissing, nil
		}
		return linkConflict, fmt.Errorf("inspect target %s: %w", target, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return linkConflict, nil
	}

	destination, err := os.Readlink(target)
	if err != nil {
		return linkConflict, fmt.Errorf("read target link %s: %w", target, err)
	}
	if destination == source {
		return linkCurrent, nil
	}
	return linkConflict, nil
}

func exactSymlink(path, destination string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect previous managed target %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}
	actual, err := os.Readlink(path)
	if err != nil {
		return false, fmt.Errorf("read previous managed link %s: %w", path, err)
	}
	return actual == destination, nil
}

func loadLedger(path string) (linkLedger, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return linkLedger{Version: 1}, nil
	}
	if err != nil {
		return linkLedger{}, fmt.Errorf("read managed link state: %w", err)
	}
	var ledger linkLedger
	if err := json.Unmarshal(data, &ledger); err != nil {
		return linkLedger{}, fmt.Errorf("parse managed link state %s: %w", path, err)
	}
	if ledger.Version != 1 {
		return linkLedger{}, fmt.Errorf("unsupported managed link state version %d", ledger.Version)
	}
	return ledger, nil
}

func validateLedger(ledger linkLedger, home, managedRoot string) error {
	seen := make(map[string]struct{}, len(ledger.Links))
	for _, record := range ledger.Links {
		if !filepath.IsAbs(record.Target) || record.Target == home || !pathutil.Contains(home, record.Target) {
			return fmt.Errorf("recorded target %q is outside the target home", record.Target)
		}
		if record.Destination == "" {
			return fmt.Errorf("recorded target %q has an empty destination", record.Target)
		}
		if filepath.IsAbs(record.Destination) && !pathutil.Contains(managedRoot, record.Destination) {
			return fmt.Errorf("recorded destination %q is outside the managed home", record.Destination)
		}
		if _, exists := seen[record.Target]; exists {
			return fmt.Errorf("recorded target %q is duplicated", record.Target)
		}
		seen[record.Target] = struct{}{}
	}
	return nil
}

func saveLedger(path string, records []linkRecord) error {
	data, err := json.MarshalIndent(linkLedger{Version: 1, Links: records}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode managed link state: %w", err)
	}
	data = append(data, '\n')
	if err := config.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("save managed link state: %w", err)
	}
	return nil
}

func mappedSymlinkDestination(source, target, managedRoot, home, configRoot string) (string, error) {
	destination, err := managedFileSymlinkDestination(source, managedRoot)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(managedRoot, destination)
	if err != nil {
		return "", fmt.Errorf("map managed symlink %s: %w", source, err)
	}
	targetDestination := filepath.Join(home, relative)
	resolvedParent, err := pathutil.ResolveExistingPrefix(filepath.Dir(targetDestination))
	if err != nil {
		return "", fmt.Errorf("resolve target for managed symlink %s: %w", source, err)
	}
	resolvedTargetDestination := filepath.Join(resolvedParent, filepath.Base(targetDestination))
	if pathutil.Contains(configRoot, resolvedTargetDestination) || pathutil.Contains(resolvedTargetDestination, managedRoot) {
		return "", fmt.Errorf("refusing recursive managed symlink %s -> %s", target, targetDestination)
	}
	resolvedLinkParent, err := pathutil.ResolveExistingPrefix(filepath.Dir(target))
	if err != nil {
		return "", fmt.Errorf("resolve target directory for managed symlink %s: %w", source, err)
	}
	linkDestination, err := filepath.Rel(resolvedLinkParent, resolvedTargetDestination)
	if err != nil {
		return "", fmt.Errorf("make target symlink for %s: %w", target, err)
	}
	return linkDestination, nil
}

func managedFileSymlinkDestination(source, managedRoot string) (string, error) {
	info, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("inspect managed symlink %s: %w", source, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("managed directory symlink %s is not supported; track its files individually", source)
	}
	destination, err := os.Readlink(source)
	if err != nil {
		return "", fmt.Errorf("read managed symlink %s: %w", source, err)
	}
	if !filepath.IsAbs(destination) {
		destination = filepath.Join(filepath.Dir(source), destination)
	}
	destination = filepath.Clean(destination)
	if !pathutil.Contains(managedRoot, destination) {
		return "", fmt.Errorf("managed symlink %s points outside the managed home", source)
	}
	resolved, err := filepath.EvalSymlinks(destination)
	if err != nil {
		return "", fmt.Errorf("resolve managed symlink %s: %w", source, err)
	}
	if !pathutil.Contains(managedRoot, resolved) {
		return "", fmt.Errorf("managed symlink %s resolves outside the managed home", source)
	}
	return destination, nil
}
