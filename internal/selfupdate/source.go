package selfupdate

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xolan/xoldot/internal/gitops"
	"github.com/xolan/xoldot/internal/status"
)

func (updater Updater) updateSource(ctx context.Context) error {
	runner := gitops.Runner{
		Context:    ctx,
		Dir:        updater.workingDirectory,
		Executable: updater.gitExecutable,
		Stdout:     updater.Stdout,
		Stderr:     updater.Stderr,
		Verbose:    updater.Verbose,
		Reporter:   updater.Reporter,
	}
	root, err := runner.Root()
	if err != nil {
		return fmt.Errorf("find the xoldot source checkout from %s: %w", updater.workingDirectory, err)
	}
	if err := verifyModule(root, updater.modulePath); err != nil {
		return err
	}

	runner.Dir = root
	branch, err := runner.CurrentBranch()
	if err != nil {
		return fmt.Errorf("read the checked-out source branch: %w", err)
	}

	if err := updater.reportf(status.Progress, "Pulling origin/%s in %s", branch, root); err != nil {
		return err
	}
	if err := runner.PullFastForward("origin", branch); err != nil {
		return fmt.Errorf("pull origin/%s: %w", branch, err)
	}
	return updater.reportf(status.Success, "Updated the source checkout on %s", branch)
}

func verifyModule(root, want string) error {
	moduleFile := filepath.Join(root, "go.mod")
	file, err := os.Open(moduleFile)
	if err != nil {
		return fmt.Errorf("open %s: %w", moduleFile, err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "module" {
			if fields[1] != want {
				return fmt.Errorf("%s belongs to module %q, not %q", root, fields[1], want)
			}
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", moduleFile, err)
	}
	return fmt.Errorf("%s does not declare a Go module", moduleFile)
}
