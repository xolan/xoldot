package gitops

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type Runner struct {
	Stdin  io.Reader
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
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
		if err := runner.logf("sync: committing local changes\n"); err != nil {
			return err
		}
		if err := runner.run("commit", "-m", "xoldot sync"); err != nil {
			return fmt.Errorf("commit local changes (is git user.name/user.email configured?): %w", err)
		}
	} else if err := runner.logf("sync: no local changes to commit\n"); err != nil {
		return err
	}

	remoteBranch, err := runner.remoteBranchExists(remote, branch)
	if err != nil {
		return err
	}
	if remoteBranch {
		if err := runner.logf("sync: pulling %s %s with rebase\n", remote, branch); err != nil {
			return err
		}
		if err := runner.run("pull", "--rebase", remote, branch); err != nil {
			return err
		}
	}
	if err := runner.logf("sync: pushing to %s/%s\n", remote, branch); err != nil {
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
		if err := runner.logf("sync: no local changes to commit\n"); err != nil {
			return err
		}
	} else if err := runner.logf("sync: would commit:\n%s", status); err != nil {
		return err
	}
	remoteBranch, err := runner.remoteBranchExists(remote, branch)
	if err != nil {
		return err
	}
	if remoteBranch {
		if err := runner.logf("sync: would pull %s %s with rebase\n", remote, branch); err != nil {
			return err
		}
	}
	return runner.logf("sync: would push to %s/%s\n", remote, branch)
}

func (runner Runner) logf(format string, arguments ...any) error {
	if _, err := fmt.Fprintf(runner.Stdout, format, arguments...); err != nil {
		return fmt.Errorf("write sync status: %w", err)
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
		return fmt.Errorf("git %s: %w", strings.Join(arguments, " "), err)
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
	command := exec.Command("git", arguments...)
	command.Dir = runner.Dir
	command.Stdin = runner.Stdin
	return command
}

func hasExitCode(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}
