package selfupdate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/xolan/xoldot/internal/status"
)

const (
	defaultAPIURL     = "https://api.github.com/repos/xolan/xoldot/releases/latest"
	defaultModulePath = "github.com/xolan/xoldot"
)

type Updater struct {
	Version  string
	Stdout   io.Writer
	Stderr   io.Writer
	Reporter status.Reporter
	Verbose  bool

	apiURL           string
	client           *http.Client
	executable       func() (string, error)
	gitExecutable    string
	modulePath       string
	runtimeOS        string
	runtimeArch      string
	workingDirectory string
}

func (updater Updater) Update(ctx context.Context) error {
	updater.setDefaults()
	current, err := parseReleaseVersion(updater.Version)
	if err != nil {
		return updater.updateSource(ctx)
	}
	return updater.updateRelease(ctx, current)
}

func (updater *Updater) setDefaults() {
	if updater.Stdout == nil {
		updater.Stdout = io.Discard
	}
	if updater.Stderr == nil {
		updater.Stderr = io.Discard
	}
	if updater.apiURL == "" {
		updater.apiURL = defaultAPIURL
	}
	if updater.client == nil {
		updater.client = &http.Client{Timeout: 2 * time.Minute}
	}
	if updater.executable == nil {
		updater.executable = os.Executable
	}
	if updater.gitExecutable == "" {
		updater.gitExecutable = "git"
	}
	if updater.modulePath == "" {
		updater.modulePath = defaultModulePath
	}
	if updater.runtimeOS == "" {
		updater.runtimeOS = runtime.GOOS
	}
	if updater.runtimeArch == "" {
		updater.runtimeArch = runtime.GOARCH
	}
	if updater.workingDirectory == "" {
		updater.workingDirectory = "."
	}
}

func (updater Updater) reportf(kind status.Kind, format string, arguments ...any) error {
	if err := status.Reportf(updater.Reporter, kind, format, arguments...); err != nil {
		return fmt.Errorf("write self-update status: %w", err)
	}
	return nil
}
