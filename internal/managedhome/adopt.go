package managedhome

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/xolan/xoldot/internal/pathutil"
	reportstatus "github.com/xolan/xoldot/internal/status"
)

type AdoptionPlan struct {
	Source      string
	Destination string
	linkPlan    Plan

	sourceRelative      string
	destinationRelative string
	sourceIdentity      os.FileInfo
}

func Adopt(source, managedRoot, home, configRoot string, reporter reportstatus.Reporter, dry bool) error {
	plan, err := PrepareAdoption(source, managedRoot, home, configRoot)
	if err != nil {
		return err
	}
	return plan.Apply(reporter, dry)
}

func PrepareAdoption(source, managedRoot, home, configRoot string) (AdoptionPlan, error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return AdoptionPlan{}, fmt.Errorf("resolve adoption source: %w", err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		return AdoptionPlan{}, fmt.Errorf("inspect adoption source %s: %w", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return AdoptionPlan{}, fmt.Errorf("adoption source %s is a symlink; only ordinary files can be adopted", source)
	}
	if !info.Mode().IsRegular() {
		return AdoptionPlan{}, fmt.Errorf("adoption source %s is not an ordinary file", source)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return AdoptionPlan{}, fmt.Errorf("resolve adoption source %s: %w", source, err)
	}
	sourceIdentity, err := os.Lstat(source)
	if err != nil {
		return AdoptionPlan{}, fmt.Errorf("inspect resolved adoption source %s: %w", source, err)
	}
	if !os.SameFile(info, sourceIdentity) {
		return AdoptionPlan{}, fmt.Errorf("adoption source %s changed while it was being prepared", source)
	}

	layout, err := newManagedHomeLayout(managedRoot, home, configRoot)
	if err != nil {
		return AdoptionPlan{}, err
	}
	if source == layout.Home || !pathutil.Contains(layout.Home, source) {
		return AdoptionPlan{}, fmt.Errorf("adoption source %s is outside the target home %s", source, layout.Home)
	}
	if pathutil.Contains(layout.ConfigRoot, source) {
		return AdoptionPlan{}, fmt.Errorf("refusing recursive adoption of %s from the configuration directory", source)
	}

	sourceRelative, err := layout.homeRelative(source)
	if err != nil {
		return AdoptionPlan{}, fmt.Errorf("map adoption source %s: %w", source, err)
	}
	destination := filepath.Join(layout.ManagedRoot, sourceRelative)
	if destination == source {
		return AdoptionPlan{}, fmt.Errorf("refusing recursive adoption of %s into itself", source)
	}
	if _, err := os.Lstat(destination); err == nil {
		return AdoptionPlan{}, fmt.Errorf("managed destination %s already exists", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return AdoptionPlan{}, fmt.Errorf("inspect managed destination %s: %w", destination, err)
	}
	destinationParent := filepath.Dir(destination)
	resolvedParent, err := pathutil.ResolveExistingPrefix(destinationParent)
	if err != nil {
		return AdoptionPlan{}, fmt.Errorf("resolve managed destination for %s: %w", source, err)
	}
	if resolvedParent != destinationParent {
		return AdoptionPlan{}, fmt.Errorf("managed destination parent %s contains a symlink", destinationParent)
	}
	if !pathutil.Contains(layout.ManagedRoot, destination) {
		return AdoptionPlan{}, fmt.Errorf("managed destination %s resolves outside the managed home %s", destination, layout.ManagedRoot)
	}
	if layout.reservedTarget(source) {
		return AdoptionPlan{}, fmt.Errorf("target %s is reserved for xoldot state", source)
	}

	previous, err := layout.loadLedger()
	if err != nil {
		return AdoptionPlan{}, err
	}
	if err := layout.validateLedger(previous); err != nil {
		return AdoptionPlan{}, err
	}
	record := linkRecord{Target: source, Destination: destination}
	destinationRelative, err := layout.managedRelative(destination)
	if err != nil {
		return AdoptionPlan{}, err
	}

	return AdoptionPlan{
		Source:              source,
		Destination:         destination,
		sourceRelative:      sourceRelative,
		destinationRelative: destinationRelative,
		sourceIdentity:      sourceIdentity,
		linkPlan: Plan{
			layout:        layout,
			previous:      previous,
			links:         []linkPlan{{linkRecord: record}},
			records:       layout.recordsWith(record, previous),
			rootedTargets: true,
		},
	}, nil
}

func (plan AdoptionPlan) Apply(reporter reportstatus.Reporter, dry bool) error {
	return plan.apply(reporter, dry, nil)
}

func (plan AdoptionPlan) apply(
	reporter reportstatus.Reporter,
	dry bool,
	hook func(transactionStep) error,
) error {
	if dry {
		if err := reportf(reporter, reportstatus.Progress, "Would move %s -> %s", plan.Source, plan.Destination); err != nil {
			return err
		}
		_, err := plan.linkPlan.apply(reporter, true, nil, nil)
		return err
	}

	managedRoot, err := plan.linkPlan.layout.openManagedRoot()
	if err != nil {
		return err
	}
	home, err := plan.linkPlan.layout.openLockedHome(plan.linkPlan.previous)
	if err != nil {
		return errors.Join(err, managedRoot.Close())
	}
	closeRoots := func() error {
		return errors.Join(managedRoot.Close(), home.close())
	}

	sourceParentRelative := filepath.Dir(plan.sourceRelative)
	sourceParent, err := home.root.OpenRoot(sourceParentRelative)
	if err != nil {
		return errors.Join(
			fmt.Errorf("open adoption source parent %s: %w", filepath.Dir(plan.Source), err),
			closeRoots(),
		)
	}
	sourceName := filepath.Base(plan.sourceRelative)
	input, err := openPreparedSource(sourceParent, sourceName, plan.Source, plan.sourceIdentity)
	if err != nil {
		return errors.Join(err, sourceParent.Close(), closeRoots())
	}

	transaction := linkTransaction{hook: hook}
	fail := func(applyErr error) error {
		return errors.Join(
			applyErr,
			input.Close(),
			transaction.rollback(),
			sourceParent.Close(),
			closeRoots(),
		)
	}
	if _, err := managedRoot.Lstat(plan.destinationRelative); err == nil {
		return fail(fmt.Errorf("managed destination %s appeared while adopting", plan.Destination))
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(fmt.Errorf("inspect managed destination %s: %w", plan.Destination, err))
	}
	if err := transaction.makeDirectories(
		managedRoot,
		filepath.Dir(plan.destinationRelative),
	); err != nil {
		return fail(fmt.Errorf("create managed destination directory: %w", err))
	}
	if err := stageAdoptionFile(
		input,
		plan.Source,
		managedRoot,
		plan.destinationRelative,
		plan.Destination,
		&transaction,
	); err != nil {
		return fail(err)
	}
	if err := transaction.after(transactionStepDestinationStaged); err != nil {
		return fail(err)
	}

	currentSource, err := sourceParent.Lstat(sourceName)
	if err != nil {
		return fail(fmt.Errorf("inspect adoption source %s before backup: %w", plan.Source, err))
	}
	if !os.SameFile(plan.sourceIdentity, currentSource) {
		return fail(fmt.Errorf("adoption source %s changed before adoption", plan.Source))
	}
	backup, err := transaction.backupPath(
		sourceParent,
		".",
		sourceName,
		plan.Source,
		plan.sourceIdentity,
		false,
	)
	if err != nil {
		return fail(fmt.Errorf("back up adoption source %s: %w", plan.Source, err))
	}
	if err := transaction.after(transactionStepSourceBackedUp); err != nil {
		return fail(err)
	}
	if err := verifyAdoptionFile(
		sourceParent,
		backup,
		plan.Source,
		managedRoot,
		plan.destinationRelative,
		plan.Destination,
	); err != nil {
		return fail(fmt.Errorf("verify staged adoption: %w", err))
	}
	if err := reportf(reporter, reportstatus.Progress, "Moving %s -> %s", plan.Source, plan.Destination); err != nil {
		return fail(err)
	}
	if _, err := plan.linkPlan.apply(reporter, false, &transaction, home.root); err != nil {
		return fail(err)
	}
	closeInputErr := input.Close()
	commitErr := transaction.commit()
	return errors.Join(closeInputErr, commitErr, sourceParent.Close(), closeRoots())
}

func openPreparedSource(
	parent *os.Root,
	name string,
	display string,
	prepared os.FileInfo,
) (*os.File, error) {
	current, err := parent.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("inspect adoption source %s: %w", display, err)
	}
	if !current.Mode().IsRegular() || !os.SameFile(prepared, current) {
		return nil, fmt.Errorf("adoption source %s changed before adoption", display)
	}
	input, err := parent.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open adoption source %s: %w", display, err)
	}
	opened, err := input.Stat()
	if err != nil {
		_ = input.Close()
		return nil, fmt.Errorf("inspect open adoption source %s: %w", display, err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(prepared, opened) {
		_ = input.Close()
		return nil, fmt.Errorf("adoption source %s changed before adoption", display)
	}
	return input, nil
}

func stageAdoptionFile(
	input *os.File,
	sourceDisplay string,
	destinationRoot *os.Root,
	destinationRelative string,
	destinationDisplay string,
	transaction *linkTransaction,
) error {
	info, err := input.Stat()
	if err != nil {
		return fmt.Errorf("inspect open adoption source %s: %w", sourceDisplay, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("adoption source %s is no longer an ordinary file", sourceDisplay)
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind adoption source %s: %w", sourceDisplay, err)
	}

	output, _, err := transaction.createFile(
		destinationRoot,
		destinationRelative,
		destinationDisplay,
		info.Mode().Perm(),
	)
	if err != nil {
		return fmt.Errorf("create managed destination %s: %w", destinationDisplay, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("stage %s at %s: %w", sourceDisplay, destinationDisplay, err)
	}
	if err := output.Chmod(info.Mode().Perm()); err != nil {
		_ = output.Close()
		return fmt.Errorf("preserve permissions on %s: %w", destinationDisplay, err)
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return fmt.Errorf("sync staged file %s: %w", destinationDisplay, err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close staged file %s: %w", destinationDisplay, err)
	}
	if err := verifyOpenSourceAgainstDestination(
		input,
		sourceDisplay,
		destinationRoot,
		destinationRelative,
		destinationDisplay,
	); err != nil {
		return fmt.Errorf("verify staged adoption: %w", err)
	}
	return nil
}

func verifyAdoptionFile(
	sourceRoot *os.Root,
	sourceRelative string,
	sourceDisplay string,
	destinationRoot *os.Root,
	destinationRelative string,
	destinationDisplay string,
) error {
	source, err := sourceRoot.Open(sourceRelative)
	if err != nil {
		return fmt.Errorf("open source %s: %w", sourceDisplay, err)
	}
	defer func() { _ = source.Close() }()
	return verifyOpenSourceAgainstDestination(
		source,
		sourceDisplay,
		destinationRoot,
		destinationRelative,
		destinationDisplay,
	)
}

func verifyOpenSourceAgainstDestination(
	source *os.File,
	sourceDisplay string,
	destinationRoot *os.Root,
	destinationRelative string,
	destinationDisplay string,
) error {
	sourceInfo, err := source.Stat()
	if err != nil {
		return fmt.Errorf("inspect source %s: %w", sourceDisplay, err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("source %s is no longer an ordinary file", sourceDisplay)
	}
	destination, err := destinationRoot.Open(destinationRelative)
	if err != nil {
		return fmt.Errorf("open destination %s: %w", destinationDisplay, err)
	}
	defer func() { _ = destination.Close() }()
	destinationInfo, err := destination.Stat()
	if err != nil {
		return fmt.Errorf("inspect destination %s: %w", destinationDisplay, err)
	}
	if !destinationInfo.Mode().IsRegular() {
		return fmt.Errorf("destination %s is no longer an ordinary file", destinationDisplay)
	}
	if sourceInfo.Mode().Perm() != destinationInfo.Mode().Perm() {
		return fmt.Errorf(
			"permission mismatch: source has %04o, destination has %04o",
			sourceInfo.Mode().Perm(),
			destinationInfo.Mode().Perm(),
		)
	}
	equal, err := openFilesEqual(source, destination)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("byte mismatch between %s and %s", sourceDisplay, destinationDisplay)
	}
	return nil
}

func openFilesEqual(left, right *os.File) (bool, error) {
	if _, err := left.Seek(0, io.SeekStart); err != nil {
		return false, fmt.Errorf("rewind %s for verification: %w", left.Name(), err)
	}
	if _, err := right.Seek(0, io.SeekStart); err != nil {
		return false, fmt.Errorf("rewind %s for verification: %w", right.Name(), err)
	}
	leftBuffer := make([]byte, 32*1024)
	rightBuffer := make([]byte, len(leftBuffer))
	for {
		leftCount, leftErr := io.ReadFull(left, leftBuffer)
		rightCount, rightErr := io.ReadFull(right, rightBuffer)
		if leftCount != rightCount || !bytes.Equal(leftBuffer[:leftCount], rightBuffer[:rightCount]) {
			return false, nil
		}
		if leftErr == io.EOF && rightErr == io.EOF {
			return true, nil
		}
		if leftErr == io.ErrUnexpectedEOF && rightErr == io.ErrUnexpectedEOF {
			return true, nil
		}
		if leftErr != nil && leftErr != io.EOF && leftErr != io.ErrUnexpectedEOF {
			return false, fmt.Errorf("read %s for verification: %w", left.Name(), leftErr)
		}
		if rightErr != nil && rightErr != io.EOF && rightErr != io.ErrUnexpectedEOF {
			return false, fmt.Errorf("read %s for verification: %w", right.Name(), rightErr)
		}
		if leftErr != nil || rightErr != nil {
			return false, nil
		}
	}
}
