package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/xolan/xoldot/internal/status"
)

type styler struct {
	enabled bool
}

func newStyler(output io.Writer) styler {
	file, ok := output.(*os.File)
	if !ok {
		return styler{}
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return styler{}
	}
	info, err := file.Stat()
	return styler{enabled: err == nil && info.Mode()&os.ModeCharDevice != 0}
}

func (style styler) paint(code, text string) string {
	if !style.enabled {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (style styler) bold(text string) string { return style.paint("1", text) }

type terminalReporter struct {
	output      io.Writer
	errorOutput io.Writer
	outputStyle styler
	errorStyle  styler
}

func newTerminalReporter(output, errorOutput io.Writer, outputStyle styler) terminalReporter {
	return terminalReporter{
		output:      output,
		errorOutput: errorOutput,
		outputStyle: outputStyle,
		errorStyle:  newStyler(errorOutput),
	}
}

func (reporter terminalReporter) Report(kind status.Kind, text string) error {
	output := reporter.output
	style := reporter.outputStyle
	prefix := ""
	color := ""
	colorLine := false

	switch kind {
	case status.Progress:
		prefix = "›"
		color = "36"
	case status.Success:
		prefix = "✓"
		color = "32"
		colorLine = true
	case status.Warning:
		prefix = "!"
		color = "33"
		colorLine = true
	case status.Command:
		output = reporter.errorOutput
		style = reporter.errorStyle
		prefix = "+"
		color = "2"
		colorLine = true
	default:
		return fmt.Errorf("unknown status kind %d", kind)
	}

	message := formatStatus(style, color, prefix, text, colorLine)
	if _, err := io.WriteString(output, message); err != nil {
		return fmt.Errorf("write status output: %w", err)
	}
	return nil
}

func formatStatus(style styler, color, prefix, text string, colorLine bool) string {
	text = strings.TrimSuffix(text, "\n")
	lines := strings.Split(text, "\n")
	first := prefix + " " + lines[0]
	if colorLine {
		first = style.paint(color, first)
	} else {
		first = style.paint(color, prefix) + " " + lines[0]
	}

	var output strings.Builder
	output.WriteString(first)
	output.WriteByte('\n')
	for _, line := range lines[1:] {
		continuation := "  " + line
		if colorLine {
			continuation = style.paint(color, continuation)
		}
		output.WriteString(continuation)
		output.WriteByte('\n')
	}
	return output.String()
}
