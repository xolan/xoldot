package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/xolan/xoldot/internal/status"
)

type styler struct {
	enabled bool
}

const (
	styleBold   = "1"
	styleMuted  = "2"
	styleRed    = "31"
	styleGreen  = "32"
	styleYellow = "33"
	styleCyan   = "36"
)

func newStyler(output io.Writer) styler {
	return newStylerWithTerminal(output, term.IsTerminal)
}

func newStylerWithTerminal(output io.Writer, isTerminal func(int) bool) styler {
	file, ok := output.(*os.File)
	if !ok {
		return styler{}
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return styler{}
	}
	return styler{enabled: isTerminal(int(file.Fd()))}
}

func (style styler) paint(code, text string) string {
	if !style.enabled {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (style styler) heading(text string) string  { return style.paint(styleBold, text) }
func (style styler) bold(text string) string     { return style.heading(text) }
func (style styler) muted(text string) string    { return style.paint(styleMuted, text) }
func (style styler) failure(text string) string  { return style.paint(styleRed, text) }
func (style styler) success(text string) string  { return style.paint(styleGreen, text) }
func (style styler) warning(text string) string  { return style.paint(styleYellow, text) }
func (style styler) progress(text string) string { return style.paint(styleCyan, text) }

func (style styler) progressPlan(text string) string {
	return style.planDescription(text, styleCyan)
}

func (style styler) warningPlan(text string) string {
	return style.planDescription(text, styleYellow)
}

func (style styler) planDescription(text, color string) string {
	if !style.enabled || text == "" {
		return text
	}
	end := strings.IndexAny(text, " :")
	if end < 0 {
		end = len(text)
	}
	return style.paint(color, text[:end]) + text[end:]
}

func (style styler) unifiedDiff(text string) string {
	if !style.enabled {
		return text
	}
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		switch {
		case strings.HasPrefix(line, "+"):
			lines[index] = style.success(line)
		case strings.HasPrefix(line, "-"):
			lines[index] = style.failure(line)
		case strings.HasPrefix(line, "@@"):
			lines[index] = style.progress(line)
		}
	}
	return strings.Join(lines, "\n")
}

type terminalReporter struct {
	output      io.Writer
	errorOutput io.Writer
	outputStyle styler
	errorStyle  styler
}

type statusDecoration struct {
	prefix    string
	color     string
	colorLine bool
}

func decorationForStatus(kind status.Kind) (statusDecoration, error) {
	switch kind {
	case status.Progress:
		return statusDecoration{prefix: "›", color: styleCyan}, nil
	case status.Success:
		return statusDecoration{prefix: "✓", color: styleGreen, colorLine: true}, nil
	case status.Warning:
		return statusDecoration{prefix: "!", color: styleYellow, colorLine: true}, nil
	case status.Command:
		return statusDecoration{prefix: "+", color: styleMuted, colorLine: true}, nil
	case status.Error:
		return statusDecoration{prefix: "✗", color: styleRed, colorLine: true}, nil
	default:
		return statusDecoration{}, fmt.Errorf("unknown status kind %d", kind)
	}
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
	decoration, err := decorationForStatus(kind)
	if err != nil {
		return err
	}
	output := reporter.output
	style := reporter.outputStyle

	switch kind {
	case status.Command:
		output = reporter.errorOutput
		style = reporter.errorStyle
	case status.Error:
		output = reporter.errorOutput
		style = reporter.errorStyle
	}

	message := formatStatus(style, decoration.color, decoration.prefix, text, decoration.colorLine)
	if _, err := io.WriteString(output, message); err != nil {
		return fmt.Errorf("write status output: %w", err)
	}
	return nil
}

func WriteError(output io.Writer, err error) error {
	reporter := newTerminalReporter(io.Discard, output, styler{})
	return reporter.Report(status.Error, "xoldot: "+err.Error())
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
