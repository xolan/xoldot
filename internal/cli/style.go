package cli

import (
	"io"
	"os"
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

func (style styler) success(text string) string { return style.paint("32", "✓ "+text) }
func (style styler) warn(text string) string    { return style.paint("33", text) }
func (style styler) bold(text string) string    { return style.paint("1", text) }
