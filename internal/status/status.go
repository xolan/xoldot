package status

import "fmt"

type Kind uint8

const (
	Progress Kind = iota
	Success
	Warning
	Command
)

type Reporter interface {
	Report(kind Kind, text string) error
}

type ReporterFunc func(kind Kind, text string) error

func (report ReporterFunc) Report(kind Kind, text string) error {
	if report == nil {
		return nil
	}
	return report(kind, text)
}

func Reportf(reporter Reporter, kind Kind, format string, arguments ...any) error {
	if reporter == nil {
		return nil
	}
	return reporter.Report(kind, fmt.Sprintf(format, arguments...))
}
