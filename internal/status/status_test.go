package status

import "testing"

func TestReportfFormatsMessage(t *testing.T) {
	var gotKind Kind
	var gotText string
	reporter := ReporterFunc(func(kind Kind, text string) error {
		gotKind = kind
		gotText = text
		return nil
	})

	if err := Reportf(reporter, Progress, "Pulling %s/%s", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	if gotKind != Progress || gotText != "Pulling origin/main" {
		t.Errorf("reported (%d, %q)", gotKind, gotText)
	}
}

func TestNilReporterIsDiscarded(t *testing.T) {
	if err := Reportf(nil, Progress, "ignored"); err != nil {
		t.Fatal(err)
	}
	var reporter Reporter = ReporterFunc(nil)
	if err := reporter.Report(Progress, "ignored"); err != nil {
		t.Fatal(err)
	}
}
