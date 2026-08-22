package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	reportstatus "github.com/xolan/xoldot/internal/status"
	"github.com/xolan/xoldot/internal/urlutil"
)

type Runner struct {
	Context    context.Context
	Dir        string
	Executable string
	Stdout     io.Writer
	Stderr     io.Writer
	Verbose    bool
	Reporter   reportstatus.Reporter
}

func (runner Runner) Root() (string, error) {
	root, err := runner.output("rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("find Git repository root: %w", err)
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("git repository root is empty")
	}
	return root, nil
}

func (runner Runner) CurrentBranch() (string, error) {
	branch, err := runner.output("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read current Git branch: %w", err)
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "", errors.New("current Git branch is empty")
	}
	return branch, nil
}

func (runner Runner) PullFastForward(remote, branch string) error {
	if strings.TrimSpace(remote) == "" || strings.TrimSpace(branch) == "" {
		return errors.New("git remote and branch cannot be empty")
	}
	return runner.run("pull", "--ff-only", remote, branch)
}

type LocalInspection struct {
	Repository bool
	Remote     bool
	Branch     bool
}

type CheckoutError struct {
	err error
}

func (err *CheckoutError) Error() string {
	return err.err.Error()
}

func (err *CheckoutError) Unwrap() error {
	return err.err
}

func (runner Runner) Configure(remote, branch string) error {
	if strings.TrimSpace(remote) == "" {
		return fmt.Errorf("git remote URL cannot be empty")
	}
	if strings.TrimSpace(branch) == "" {
		return fmt.Errorf("git branch cannot be empty")
	}

	if err := os.MkdirAll(runner.Dir, 0o755); err != nil {
		return fmt.Errorf("create git directory: %w", err)
	}
	if err := runner.run("init", "-b", branch); err != nil {
		return err
	}

	current, err := runner.output("remote", "get-url", "origin")
	if err == nil {
		if strings.TrimSpace(current) == remote {
			return nil
		}
		return runner.run("remote", "set-url", "origin", remote)
	}
	return runner.run("remote", "add", "origin", remote)
}

func (runner Runner) Sync(remote, branch string, dry bool) error {
	if strings.TrimSpace(remote) == "" || strings.TrimSpace(branch) == "" {
		return fmt.Errorf("git remote and branch must be configured")
	}
	if _, err := os.Stat(runner.Dir + "/.git"); err != nil {
		return fmt.Errorf("%s is not a git repository; run 'xoldot setup': %w", runner.Dir, err)
	}

	if dry {
		return runner.syncDry(remote, branch)
	}

	if err := runner.run("add", "-A"); err != nil {
		return err
	}
	hasChanges, err := runner.hasStagedChanges()
	if err != nil {
		return err
	}
	if hasChanges {
		if err := runner.reportf(reportstatus.Progress, "Committing local changes"); err != nil {
			return err
		}
		if err := runner.run("commit", "-m", "xoldot sync"); err != nil {
			return fmt.Errorf("commit local changes (is git user.name/user.email configured?): %w", err)
		}
	} else if err := runner.reportf(reportstatus.Progress, "No local changes to commit"); err != nil {
		return err
	}

	remoteBranch, err := runner.remoteBranchExists(remote, branch)
	if err != nil {
		return err
	}
	if remoteBranch {
		if err := runner.reportf(reportstatus.Progress, "Pulling %s/%s with rebase", remote, branch); err != nil {
			return err
		}
		if err := runner.run("pull", "--rebase", remote, branch); err != nil {
			return err
		}
	}
	if err := runner.reportf(reportstatus.Progress, "Pushing to %s/%s", remote, branch); err != nil {
		return err
	}
	if err := runner.run("push", "-u", remote, "HEAD:"+branch); err != nil {
		return err
	}
	return nil
}

func (runner Runner) syncDry(remote, branch string) error {
	status, err := runner.output("status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(status) == "" {
		if err := runner.reportf(reportstatus.Progress, "No local changes to commit"); err != nil {
			return err
		}
	} else if err := runner.reportf(
		reportstatus.Progress,
		"Would commit local changes\n%s",
		strings.TrimSuffix(status, "\n"),
	); err != nil {
		return err
	}
	remoteBranch, err := runner.remoteBranchExists(remote, branch)
	if err != nil {
		return err
	}
	if remoteBranch {
		if err := runner.reportf(reportstatus.Progress, "Would pull %s/%s with rebase", remote, branch); err != nil {
			return err
		}
	}
	return runner.reportf(reportstatus.Progress, "Would push to %s/%s", remote, branch)
}

func (runner Runner) reportf(kind reportstatus.Kind, format string, arguments ...any) error {
	if err := reportstatus.Reportf(runner.Reporter, kind, format, arguments...); err != nil {
		return fmt.Errorf("write Git status: %w", err)
	}
	return nil
}

func (runner Runner) CheckoutRemote(remote, branch string) (bool, error) {
	exists, err := runner.remoteBranchExists(remote, branch)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if err := runner.run("fetch", remote, branch); err != nil {
		return false, err
	}
	if err := runner.run("checkout", "-B", branch, "--track", remote+"/"+branch); err != nil {
		return false, &CheckoutError{err: err}
	}
	return true, nil
}

func (runner Runner) HasLocalHistory() (bool, error) {
	command := runner.command("rev-parse", "--verify", "HEAD")
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	err := command.Run()
	if err == nil {
		return true, nil
	}
	if hasExitCode(err, 128) {
		return false, nil
	}
	return false, fmt.Errorf("inspect local git history: %w", err)
}

func (runner Runner) InspectLocal(remote, branch string) (LocalInspection, error) {
	if strings.TrimSpace(remote) == "" || strings.TrimSpace(branch) == "" {
		return LocalInspection{}, fmt.Errorf("git remote and branch must be configured")
	}
	if _, err := os.Stat(runner.Dir + "/.git"); errors.Is(err, os.ErrNotExist) {
		return LocalInspection{}, nil
	} else if err != nil {
		return LocalInspection{}, fmt.Errorf("inspect local Git repository: %w", err)
	}

	inspection := LocalInspection{Repository: true}
	remoteURL, err := runner.output("config", "--local", "--get", "remote."+remote+".url")
	if err == nil {
		inspection.Remote = strings.TrimSpace(remoteURL) != ""
	} else if !hasExitCode(err, 1) {
		return LocalInspection{}, fmt.Errorf("inspect local Git remote %q: %w", remote, err)
	}

	current, currentErr := runner.output("symbolic-ref", "--short", "HEAD")
	if currentErr == nil && strings.TrimSpace(current) == branch {
		inspection.Branch = true
		return inspection, nil
	}
	if currentErr != nil && !hasExitCode(currentErr, 1) {
		return LocalInspection{}, fmt.Errorf("inspect current local Git branch: %w", currentErr)
	}
	command := runner.command("show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err := command.Run(); err == nil {
		inspection.Branch = true
	} else if !hasExitCode(err, 1) {
		return LocalInspection{}, fmt.Errorf("inspect local Git branch %q: %w", branch, err)
	}
	return inspection, nil
}

func (runner Runner) hasStagedChanges() (bool, error) {
	command := runner.command("diff", "--cached", "--quiet", "--exit-code")
	err := command.Run()
	if err == nil {
		return false, nil
	}
	if hasExitCode(err, 1) {
		return true, nil
	}
	return false, fmt.Errorf("git diff --cached: %w", err)
}

func (runner Runner) remoteBranchExists(remote, branch string) (bool, error) {
	command := runner.command("ls-remote", "--exit-code", "--heads", remote, "refs/heads/"+branch)
	err := command.Run()
	if err == nil {
		return true, nil
	}
	if hasExitCode(err, 2) {
		return false, nil
	}
	return false, fmt.Errorf("inspect remote branch: %w", err)
}

func (runner Runner) run(arguments ...string) error {
	command := runner.command(arguments...)
	command.Stdout = runner.Stdout
	command.Stderr = runner.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("git %s: %w", formatCommand(arguments), err)
	}
	return nil
}

func (runner Runner) output(arguments ...string) (string, error) {
	var output bytes.Buffer
	command := runner.command(arguments...)
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return "", err
	}
	return output.String(), nil
}

func (runner Runner) command(arguments ...string) *exec.Cmd {
	if runner.Verbose {
		_ = runner.reportf(reportstatus.Command, "git %s", formatCommand(arguments))
	}
	// Stdin stays nil: a non-*os.File stdin makes exec.Cmd copy in a
	// goroutine that Wait blocks on until the next terminal read returns,
	// hanging every git command. Git prompts via /dev/tty regardless.
	executable := runner.Executable
	if executable == "" {
		executable = "git"
	}
	var command *exec.Cmd
	if runner.Context == nil {
		command = exec.Command(executable, arguments...)
	} else {
		command = exec.CommandContext(runner.Context, executable, arguments...)
	}
	command.Dir = runner.Dir
	return command
}

func formatCommand(arguments []string) string {
	formatted := append([]string(nil), arguments...)
	if len(formatted) == 4 && formatted[0] == "remote" && (formatted[1] == "add" || formatted[1] == "set-url") {
		formatted[3] = urlutil.RedactForDisplay(formatted[3])
	}
	return strings.Join(formatted, " ")
}

func hasExitCode(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}
