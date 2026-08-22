package cli

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/status"
)

func TestNewStylerRejectsCharacterDevicesThatAreNotTerminals(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	output, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.Close() }()

	if style := newStyler(output); style.enabled {
		t.Fatal("newStyler(os.DevNull) enabled ANSI styling")
	}
}

func TestNewStylerTerminalPolicy(t *testing.T) {
	output, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.Close() }()

	for _, test := range []struct {
		name       string
		noColor    string
		term       string
		isTerminal bool
		want       bool
	}{
		{name: "terminal", term: "xterm-256color", isTerminal: true, want: true},
		{name: "not terminal", term: "xterm-256color"},
		{name: "NO_COLOR", noColor: "1", term: "xterm-256color", isTerminal: true},
		{name: "dumb terminal", term: "dumb", isTerminal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", test.noColor)
			t.Setenv("TERM", test.term)
			style := newStylerWithTerminal(output, func(int) bool { return test.isTerminal })
			if style.enabled != test.want {
				t.Errorf("styling enabled = %t, want %t", style.enabled, test.want)
			}
		})
	}
}

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
		{kind: status.Error, text: "xoldot: apply failed"},
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
	wantErrorOutput := "\x1b[2m+ git pull --rebase origin main\x1b[0m\n" +
		"\x1b[31m✗ xoldot: apply failed\x1b[0m\n"
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
	if err := reporter.Report(status.Warning, "Git remains disabled"); err != nil {
		t.Fatal(err)
	}
	if err := reporter.Report(status.Error, "xoldot: apply failed"); err != nil {
		t.Fatal(err)
	}

	if got, want := output.String(), "› Pulling origin/main with rebase\n✓ Sync complete\n! Git remains disabled\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got, want := errorOutput.String(), "+ git pull --rebase origin main\n✗ xoldot: apply failed\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestWriteErrorKeepsPrefixWithoutColor(t *testing.T) {
	var output bytes.Buffer
	if err := WriteError(&output, errors.New("apply failed")); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "✗ xoldot: apply failed\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestStylerColorsReportPrefixes(t *testing.T) {
	style := styler{enabled: true}
	for _, test := range []struct {
		name string
		got  string
		want string
	}{
		{name: "heading", got: style.heading("Managed home:"), want: "\x1b[1mManaged home:\x1b[0m"},
		{name: "current", got: style.success("current"), want: "\x1b[32mcurrent\x1b[0m"},
		{name: "pending", got: style.progress("missing"), want: "\x1b[36mmissing\x1b[0m"},
		{name: "warning", got: style.warning("conflict"), want: "\x1b[33mconflict\x1b[0m"},
		{name: "error", got: style.failure("error:"), want: "\x1b[31merror:\x1b[0m"},
		{name: "muted", got: style.muted("unchecked"), want: "\x1b[2munchecked\x1b[0m"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Errorf("styled text = %q, want %q", test.got, test.want)
			}
		})
	}
}

func TestStylerColorsPlansAndUnifiedDiff(t *testing.T) {
	style := styler{enabled: true}
	if got, want := style.progressPlan("Would link source -> target"), "\x1b[36mWould\x1b[0m link source -> target"; got != want {
		t.Errorf("plan = %q, want %q", got, want)
	}
	if got, want := style.warningPlan("Conflict: local changes"), "\x1b[33mConflict\x1b[0m: local changes"; got != want {
		t.Errorf("conflict = %q, want %q", got, want)
	}

	diff := "--- old\n+++ new\n@@ -1 +1 @@\n-before\n+after\n unchanged\n"
	want := "\x1b[31m--- old\x1b[0m\n" +
		"\x1b[32m+++ new\x1b[0m\n" +
		"\x1b[36m@@ -1 +1 @@\x1b[0m\n" +
		"\x1b[31m-before\x1b[0m\n" +
		"\x1b[32m+after\x1b[0m\n" +
		" unchanged\n"
	if got := style.unifiedDiff(diff); got != want {
		t.Errorf("diff = %q, want %q", got, want)
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
