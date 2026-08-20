package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/status"
)

func TestTerminalReporterRoutesAndFormatsMessages(t *testing.T) {
	var output bytes.Buffer
	var errorOutput bytes.Buffer
	reporter := terminalReporter{
		output:      &output,
		errorOutput: &errorOutput,
		outputStyle: styler{enabled: true},
		errorStyle:  styler{enabled: true},
	}

	for _, message := range []struct {
		kind status.Kind
		text string
	}{
		{kind: status.Progress, text: "Would commit local changes\nM tools.toml"},
		{kind: status.Success, text: "Sync complete"},
		{kind: status.Warning, text: "Git remains disabled"},
		{kind: status.Command, text: "git pull --rebase origin main"},
	} {
		if err := reporter.Report(message.kind, message.text); err != nil {
			t.Fatalf("Report(%d) error = %v", message.kind, err)
		}
	}

	wantOutput := "\x1b[36m›\x1b[0m Would commit local changes\n" +
		"  M tools.toml\n" +
		"\x1b[32m✓ Sync complete\x1b[0m\n" +
		"\x1b[33m! Git remains disabled\x1b[0m\n"
	if got := output.String(); got != wantOutput {
		t.Errorf("stdout = %q, want %q", got, wantOutput)
	}
	wantErrorOutput := "\x1b[2m+ git pull --rebase origin main\x1b[0m\n"
	if got := errorOutput.String(); got != wantErrorOutput {
		t.Errorf("stderr = %q, want %q", got, wantErrorOutput)
	}
}

func TestTerminalReporterKeepsPrefixesWithoutColor(t *testing.T) {
	var output bytes.Buffer
	var errorOutput bytes.Buffer
	reporter := newTerminalReporter(&output, &errorOutput, styler{})

	if err := reporter.Report(status.Progress, "Pulling origin/main with rebase"); err != nil {
		t.Fatal(err)
	}
	if err := reporter.Report(status.Success, "Sync complete"); err != nil {
		t.Fatal(err)
	}
	if err := reporter.Report(status.Command, "git pull --rebase origin main"); err != nil {
		t.Fatal(err)
	}

	if got, want := output.String(), "› Pulling origin/main with rebase\n✓ Sync complete\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got, want := errorOutput.String(), "+ git pull --rebase origin main\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestTerminalReporterRejectsUnknownKind(t *testing.T) {
	reporter := newTerminalReporter(&bytes.Buffer{}, &bytes.Buffer{}, styler{})
	if err := reporter.Report(status.Kind(99), "unknown"); err == nil {
		t.Fatal("Report() error = nil, want unknown kind error")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("simulated write failure")
}

func TestTerminalReporterReturnsWriteError(t *testing.T) {
	reporter := newTerminalReporter(failingWriter{}, &bytes.Buffer{}, styler{})
	err := reporter.Report(status.Progress, "Working")
	if err == nil || !strings.Contains(err.Error(), "simulated write failure") {
		t.Fatalf("Report() error = %v, want write failure", err)
	}
}
