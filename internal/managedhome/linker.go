package managedhome

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/xolan/xoldot/internal/pathutil"
	agentskills "github.com/xolan/xoldot/internal/skills"
	reportstatus "github.com/xolan/xoldot/internal/status"
)

type Result struct {
	Created  int
	Current  int
	Removed  int
	BackupID string
}

type State string

const (
	StateCurrent  State = "current"
	StateMissing  State = "missing"
	StateStale    State = "stale"
	StateConflict State = "conflict"
)

type Entry struct {
	State             State
	Target            string
	Destination       string
	Problem           string
	EligibleForBackup bool
}

func (entry Entry) PlanDescription() string {
	switch entry.State {
	case StateMissing:
		return fmt.Sprintf("Would link %s -> %s", entry.Target, entry.Destination)
	case StateStale:
		return fmt.Sprintf("Would remove stale link %s", entry.Target)
	case StateConflict:
		if entry.EligibleForBackup {
			return fmt.Sprintf("Conflict at %s: %s; eligible for --backup", entry.Target, entry.Problem)
		}
		return fmt.Sprintf("Conflict at %s: %s", entry.Target, entry.Problem)
	default:
		return ""
	}
}

type Inspection struct {
	entries []Entry
}

type PathFilter func(relative string) bool

type LedgerError struct {
	path string
	err  error
}

func (err *LedgerError) Error() string {
	return err.err.Error()
}

func (err *LedgerError) Unwrap() error {
	return err.err
}

func (err *LedgerError) Path() string {
	return err.path
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
	BackupConflict              bool
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
	layout    managedHomeLayout
	previous  linkLedger
	links     []linkPlan
	records   []linkRecord
	stale     []linkRecord
	current   []linkRecord
	conflicts []plannedConflict
	// Adoption roots target mutations to the prepared home. Link retains its
	// existing support for home directories redirected through absolute links.
	rootedTargets bool
}

type PreparationRefusal struct {
	err       error
	backups   []linkRecord
	changes   []Entry
	conflicts []Entry
}

func (refusal *PreparationRefusal) Error() string {
	return refusal.err.Error()
}

func (refusal *PreparationRefusal) Unwrap() error {
	return refusal.err
}

func (refusal *PreparationRefusal) Preview(reporter reportstatus.Reporter) error {
	for _, backup := range refusal.backups {
		if err := reportf(reporter, "Would back up %s before linking", backup.Target); err != nil {
			return err
		}
	}
	for _, change := range refusal.changes {
		if err := reportf(reporter, "%s", change.PlanDescription()); err != nil {
			return err
		}
	}
	for _, conflict := range refusal.conflicts {
		if err := reportf(reporter, "%s", conflict.PlanDescription()); err != nil {
			return err
		}
	}
	return nil
}

func Link(managedRoot, home, configRoot string, reporter reportstatus.Reporter, dry bool) (Result, error) {
	plan, err := Prepare(managedRoot, home, configRoot)
	if err != nil {
		return Result{}, err
	}
	return plan.Apply(reporter, dry)
}

func Prepare(managedRoot, home, configRoot string) (Plan, error) {
	return PrepareSelected(managedRoot, home, configRoot, nil)
}

func PrepareSelected(managedRoot, home, configRoot string, include PathFilter) (Plan, error) {
	plan, err := prepare(managedRoot, home, configRoot, include)
	if err != nil {
		return Plan{}, err
	}
	if len(plan.conflicts) > 0 {
		return Plan{}, plan.conflicts[0].err
	}
	return plan, nil
}

func PrepareBackup(managedRoot, home, configRoot string) (Plan, error) {
	return PrepareBackupSelected(managedRoot, home, configRoot, nil)
}

func PrepareBackupSelected(managedRoot, home, configRoot string, include PathFilter) (Plan, error) {
	plan, err := prepare(managedRoot, home, configRoot, include)
	if err != nil {
		return Plan{}, err
	}
	plan.rootedTargets = true
	if err := plan.classifyBackupConflicts(); err != nil {
		return Plan{}, err
	}
	var backups []linkRecord
	var refusalErr error
	var unsupported []Entry
	for _, conflict := range plan.conflicts {
		if !conflict.entry.EligibleForBackup {
			if refusalErr == nil {
				refusalErr = conflict.err
			}
			unsupported = append(unsupported, conflict.entry)
			continue
		}
		backup := linkRecord{
			Target:      conflict.entry.Target,
			Destination: conflict.entry.Destination,
		}
		backups = append(backups, backup)
		plan.links = append(plan.links, linkPlan{linkRecord: backup, BackupConflict: true})
	}
	if refusalErr != nil {
		return Plan{}, &PreparationRefusal{
			err:       refusalErr,
			backups:   backups,
			changes:   plan.changes(),
			conflicts: unsupported,
		}
	}
	plan.conflicts = nil
	return plan, nil
}

func Inspect(managedRoot, home, configRoot string) (Inspection, error) {
	return InspectSelected(managedRoot, home, configRoot, nil)
}

func InspectSelected(managedRoot, home, configRoot string, include PathFilter) (Inspection, error) {
	plan, err := prepare(managedRoot, home, configRoot, include)
	if err != nil {
		return Inspection{}, err
	}
	if err := plan.classifyBackupConflicts(); err != nil {
		return Inspection{}, err
	}
	return plan.inspection(), nil
}

func (plan *Plan) classifyBackupConflicts() error {
	if len(plan.conflicts) == 0 || plan.layout.homeIdentity == nil {
		return nil
	}
	root, err := plan.layout.openHomeRoot()
	if err != nil {
		return err
	}
	for index := range plan.conflicts {
		plan.conflicts[index].entry.EligibleForBackup = eligibleBackupConflict(root, plan.layout, plan.conflicts[index])
	}
	return root.Close()
}

func prepare(managedRoot, home, configRoot string, include PathFilter) (Plan, error) {
	layout, err := newManagedHomeLayout(managedRoot, home, configRoot)
	if err != nil {
		return Plan{}, err
	}
	managedRoot = layout.ManagedRoot
	home = layout.Home
	configRoot = layout.ConfigRoot

	previous, err := layout.loadLedger()
	if err != nil {
		return Plan{}, &LedgerError{path: layout.LedgerPath, err: err}
	}
	if err := layout.validateLedger(previous); err != nil {
		return Plan{}, &LedgerError{path: layout.LedgerPath, err: err}
	}

	var plans []linkPlan
	var records []linkRecord
	var current []linkRecord
	var conflicts []plannedConflict
	err = walkManagedRoot(managedRoot, func(source string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(managedRoot, source)
		if err != nil {
			return fmt.Errorf("find path for %s: %w", source, err)
		}
		skillDirectory := entry.IsDir() && agentskills.IsManagedSkillDirectory(relative)
		if entry.IsDir() && !skillDirectory {
			return nil
		}
		if include != nil && !include(relative) {
			if skillDirectory {
				return filepath.SkipDir
			}
			return nil
		}
		if skillDirectory {
			if err := validateSkillDirectory(source, managedRoot); err != nil {
				return err
			}
		}
		isSymlink := entry.Type()&os.ModeSymlink != 0

		target := filepath.Join(home, relative)
		if layout.reservedTarget(target) {
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
		if !pathutil.Contains(home, resolvedTarget) && !agentskills.IsReservedManagedHomeSelection(relative) {
			return fmt.Errorf("target %s resolves outside the target home %s", target, home)
		}
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
		layout:    layout,
		previous:  previous,
		links:     plans,
		records:   records,
		stale:     stale,
		current:   current,
		conflicts: conflicts,
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
	if dry {
		return plan.apply(reporter, true, nil, nil)
	}
	home, err := plan.layout.openLockedHome(plan.previous)
	if err != nil {
		return Result{}, err
	}

	transaction := linkTransaction{}
	result, err := plan.apply(reporter, false, &transaction, home.root)
	if err != nil {
		return result, errors.Join(
			err,
			transaction.rollback(),
			home.close(),
		)
	}
	return result, errors.Join(
		transaction.commit(),
		home.close(),
	)
}

func (plan Plan) apply(
	reporter reportstatus.Reporter,
	dry bool,
	transaction *linkTransaction,
	homeRoot *os.Root,
) (Result, error) {
	result := Result{Current: len(plan.current)}
	if dry {
		for _, link := range plan.links {
			if link.BackupConflict {
				if err := reportf(reporter, "Would back up %s before linking", link.Target); err != nil {
					return result, err
				}
			}
		}
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
		owned, err := plan.targetIsExactLink(homeRoot, record)
		if err != nil {
			return result, err
		}
		if !owned {
			return result, fmt.Errorf("target %s changed while applying links", record.Target)
		}
	}

	var backup *backupSession
	for _, link := range plan.links {
		if link.BackupConflict {
			if backup == nil {
				var err error
				backup, err = beginBackup(homeRoot, plan.layout, transaction)
				if err != nil {
					return result, err
				}
			}
			if err := backup.store(homeRoot, plan.layout, transaction, link.linkRecord); err != nil {
				return result, err
			}
			if err := transaction.after(transactionStepConflictBackedUp); err != nil {
				return result, err
			}
		}
		if link.ReplaceLegacySkillDirectory {
			if err := plan.backupLegacySkillDirectory(homeRoot, transaction, link); err != nil {
				return result, err
			}
			result.Removed += link.LegacyLinks
		}
		if err := plan.makeTargetDirectories(homeRoot, transaction, link.Target); err != nil {
			return result, fmt.Errorf("create target directory for %s: %w", link.Target, err)
		}
		if err := plan.createTargetSymlink(homeRoot, transaction, link.linkRecord); err != nil {
			return result, fmt.Errorf("link %s to %s: %w", link.Target, link.Destination, err)
		}
		if err := transaction.after(transactionStepLinkCreated); err != nil {
			return result, err
		}
		if err := reportf(reporter, "Linked %s -> %s", link.Target, link.Destination); err != nil {
			return result, err
		}
		result.Created++
	}
	for _, record := range plan.stale {
		owned, err := plan.targetIsExactLink(homeRoot, record)
		if err != nil {
			return result, err
		}
		if !owned {
			continue
		}
		if err := plan.removeTargetSymlink(homeRoot, transaction, record); err != nil {
			return result, fmt.Errorf("remove stale managed link %s: %w", record.Target, err)
		}
		if err := reportf(reporter, "Removed stale link %s", record.Target); err != nil {
			return result, err
		}
		result.Removed++
	}
	if !slices.Equal(plan.previous.Links, plan.records) {
		if err := transaction.saveLedger(homeRoot, plan.layout, plan.records); err != nil {
			return result, err
		}
		if err := transaction.after(transactionStepLedgerSaved); err != nil {
			return result, err
		}
	}
	if backup != nil {
		if err := backup.finish(homeRoot); err != nil {
			return result, err
		}
		if err := transaction.after(transactionStepBackupManifestSaved); err != nil {
			return result, err
		}
		result.BackupID = backup.id
	}
	return result, nil
}

func (plan Plan) targetIsExactLink(homeRoot *os.Root, record linkRecord) (bool, error) {
	if !plan.rootedTargets {
		return exactSymlink(record.Target, record.Destination)
	}
	relative, err := plan.layout.homeRelative(record.Target)
	if err != nil {
		return false, err
	}
	return exactRootSymlink(homeRoot, relative, record.Destination)
}

func (plan Plan) backupLegacySkillDirectory(
	homeRoot *os.Root,
	transaction *linkTransaction,
	link linkPlan,
) error {
	if !plan.rootedTargets {
		return transaction.backupLegacySkillDirectoryAbsolute(link.Target, plan.previous)
	}
	relative, err := plan.layout.homeRelative(link.Target)
	if err != nil {
		return err
	}
	return transaction.backupLegacySkillDirectory(homeRoot, relative, link.Target, plan.previous)
}

func (plan Plan) makeTargetDirectories(
	homeRoot *os.Root,
	transaction *linkTransaction,
	target string,
) error {
	if !plan.rootedTargets {
		return transaction.makeDirectoriesAbsolute(filepath.Dir(target))
	}
	relative, err := plan.layout.homeRelative(target)
	if err != nil {
		return err
	}
	return transaction.makeDirectories(homeRoot, filepath.Dir(relative))
}

func (plan Plan) createTargetSymlink(
	homeRoot *os.Root,
	transaction *linkTransaction,
	record linkRecord,
) error {
	if !plan.rootedTargets {
		return transaction.createSymlinkAbsolute(record.Destination, record.Target)
	}
	relative, err := plan.layout.homeRelative(record.Target)
	if err != nil {
		return err
	}
	return transaction.createSymlink(homeRoot, record.Destination, relative, record.Target)
}

func (plan Plan) removeTargetSymlink(
	homeRoot *os.Root,
	transaction *linkTransaction,
	record linkRecord,
) error {
	if !plan.rootedTargets {
		return transaction.removeSymlinkAbsolute(record.Target, record.Destination)
	}
	relative, err := plan.layout.homeRelative(record.Target)
	if err != nil {
		return err
	}
	return transaction.removeSymlink(homeRoot, relative, record.Target, record.Destination)
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
	if pathutil.Contains(configRoot, resolvedTargetDestination) {
		ownedPrefix, err := targetUsesManagedPrefix(targetDestination, home, managedRoot)
		if err != nil {
			return "", err
		}
		if !ownedPrefix {
			return "", fmt.Errorf("refusing recursive managed symlink %s -> %s", target, targetDestination)
		}
		resolvedTargetDestination = targetDestination
	}
	if pathutil.Contains(resolvedTargetDestination, managedRoot) {
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

func targetUsesManagedPrefix(target, home, managedRoot string) (bool, error) {
	for current := filepath.Dir(target); current != home; current = filepath.Dir(current) {
		if !pathutil.Contains(home, current) {
			return false, nil
		}
		relative, err := filepath.Rel(home, current)
		if err != nil {
			return false, fmt.Errorf("map managed target prefix %s: %w", current, err)
		}
		owned, err := exactSymlink(current, filepath.Join(managedRoot, relative))
		if err != nil {
			return false, err
		}
		if owned {
			return true, nil
		}
	}
	return false, nil
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
