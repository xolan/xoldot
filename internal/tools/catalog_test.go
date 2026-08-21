package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/status"
)

func bufferReporter(output *bytes.Buffer) status.Reporter {
	return status.ReporterFunc(func(_ status.Kind, text string) error {
		_, err := output.WriteString(text + "\n")
		return err
	})
}

func TestInstallCommandUsesDistributionOverrideThenDefault(t *testing.T) {
	tool := Tool{
		Name: "ripgrep",
		Install: Install{Linux: map[string]string{
			"default": "apt install ripgrep",
			"arch":    "yay -S ripgrep",
		}},
	}

	got, err := tool.InstallCommand(Platform{OS: "linux", LinuxIDs: []string{"arch"}})
	if err != nil {
		t.Fatalf("InstallCommand() error = %v", err)
	}
	if got != "yay -S ripgrep" {
		t.Errorf("arch command = %q", got)
	}

	got, err = tool.InstallCommand(Platform{OS: "linux", LinuxIDs: []string{"debian"}})
	if err != nil {
		t.Fatalf("InstallCommand() error = %v", err)
	}
	if got != "apt install ripgrep" {
		t.Errorf("default command = %q", got)
	}
}

func TestCatalogRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.toml")
	var catalog Catalog
	if err := Add(&catalog, "jq"); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := Save(path, catalog); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Tools) != 1 || loaded.Tools[0].Name != "jq" {
		t.Fatalf("loaded tools = %#v", loaded.Tools)
	}
	if got := loaded.Tools[0].Check; got != "command -v 'jq'" {
		t.Errorf("check = %q", got)
	}
}

func TestApplyInstallsMissingToolAndRechecks(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "installed")
	catalog := Catalog{Tools: []Tool{{
		Name:  "example",
		Check: "test -f " + shellWord(marker),
		Install: Install{Linux: map[string]string{
			"default": "touch " + shellWord(marker),
		}},
	}}}
	var output bytes.Buffer

	err := Apply(catalog, Platform{OS: "linux", LinuxIDs: []string{"debian"}, Shell: "/bin/sh"}, strings.NewReader(""), &output, &output, bufferReporter(&output), false)
	if err != nil {
		t.Fatalf("Apply() error = %v\n%s", err, output.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("install command did not create marker: %v", err)
	}
	if !strings.Contains(output.String(), "Installing tool example") {
		t.Errorf("output = %q", output.String())
	}
}

func TestPrepareBuildsInstallPlanWithoutMutating(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "installed")
	checked := filepath.Join(t.TempDir(), "checked")
	catalog := Catalog{Tools: []Tool{{
		Name:  "example",
		Check: "touch " + shellWord(checked) + "; test -f " + shellWord(marker),
		Install: Install{Linux: map[string]string{
			"default": "touch " + shellWord(marker),
		}},
	}}}
	var output bytes.Buffer
	plan, err := Prepare(
		catalog,
		Platform{OS: "linux", LinuxIDs: []string{"debian"}, Shell: "/bin/sh"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Prepare installed the Tool: %v", err)
	}
	if _, err := os.Stat(checked); !os.IsNotExist(err) {
		t.Fatalf("Prepare checked the Tool: %v", err)
	}
	if err := plan.Apply(strings.NewReader(""), &output, &output, bufferReporter(&output)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("Plan.Apply did not install the Tool: %v", err)
	}
}

func TestApplyKeepsInstallerOutputSeparateFromStatus(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "installed")
	catalog := Catalog{Tools: []Tool{{
		Name:  "example",
		Check: "test -f " + shellWord(marker),
		Install: Install{Linux: map[string]string{
			"default": "printf 'child output\\n'; touch " + shellWord(marker),
		}},
	}}}
	var childOutput bytes.Buffer
	var gotKind status.Kind
	var gotText string
	reporter := status.ReporterFunc(func(kind status.Kind, text string) error {
		gotKind = kind
		gotText = text
		return nil
	})

	err := Apply(
		catalog,
		Platform{OS: "linux", LinuxIDs: []string{"debian"}, Shell: "/bin/sh"},
		strings.NewReader(""),
		&childOutput,
		&childOutput,
		reporter,
		false,
	)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got, want := childOutput.String(), "child output\n"; got != want {
		t.Errorf("child output = %q, want %q", got, want)
	}
	if gotKind != status.Progress || gotText != "Installing tool example" {
		t.Errorf("reported (%d, %q)", gotKind, gotText)
	}
}

func TestApplyDryReportsInsteadOfInstalling(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "installed")
	catalog := Catalog{Tools: []Tool{{
		Name:  "example",
		Check: "test -f " + shellWord(marker),
		Install: Install{Linux: map[string]string{
			"default": "touch " + shellWord(marker),
		}},
	}}}
	var output bytes.Buffer

	err := Apply(catalog, Platform{OS: "linux", LinuxIDs: []string{"debian"}, Shell: "/bin/sh"}, strings.NewReader(""), &output, &output, bufferReporter(&output), true)
	if err != nil {
		t.Fatalf("Apply() error = %v\n%s", err, output.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("dry run created the install marker")
	}
	if !strings.Contains(output.String(), "If tool example is missing, would run: touch") {
		t.Errorf("output = %q", output.String())
	}
}

func TestApplyDryDoesNotRunCheck(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "checked")
	catalog := Catalog{Tools: []Tool{{
		Name:  "example",
		Check: "touch " + shellWord(marker) + "; false",
		Install: Install{Linux: map[string]string{
			"default": "true",
		}},
	}}}
	var output bytes.Buffer

	if err := Apply(catalog, Platform{OS: "linux", Shell: "/bin/sh"}, strings.NewReader(""), &output, &output, bufferReporter(&output), true); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("dry run executed check, Stat() error = %v", err)
	}
	if !strings.Contains(output.String(), "Would check") {
		t.Errorf("output = %q", output.String())
	}
}

func TestLoadRejectsDuplicateTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.toml")
	data := `[[tool]]
name = "example"
check = "true"

[[tool]]
name = "example"
check = "true"
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("Load() error = %v, want duplicate error", err)
	}
}

func TestApplyValidatesEveryInstallBeforeRunningOne(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "installed")
	catalog := Catalog{Tools: []Tool{
		{
			Name:  "first",
			Check: "false",
			Install: Install{Linux: map[string]string{
				"default": "touch " + shellWord(marker),
			}},
		},
		{Name: "second", Check: "false"},
	}}
	var output bytes.Buffer
	err := Apply(catalog, Platform{OS: "linux", Shell: "/bin/sh"}, strings.NewReader(""), &output, &output, bufferReporter(&output), false)
	if err == nil || !strings.Contains(err.Error(), "second") {
		t.Fatalf("Apply() error = %v, want second tool validation error", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("first install ran before full validation: %v", err)
	}
}

func TestApplyChecksEveryToolBeforeResolvingInstallers(t *testing.T) {
	checked := filepath.Join(t.TempDir(), "checked")
	catalog := Catalog{Tools: []Tool{
		{Name: "missing", Check: "false"},
		{
			Name:  "installed",
			Check: "touch " + shellWord(checked),
		},
	}}
	var output bytes.Buffer
	err := Apply(catalog, Platform{OS: "linux", Shell: "/bin/sh"}, strings.NewReader(""), &output, &output, bufferReporter(&output), false)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("Apply() error = %v, want missing installer error", err)
	}
	if _, err := os.Stat(checked); err != nil {
		t.Errorf("later tool check did not run before installer validation: %v", err)
	}
}

func TestApplyDoesNotRequireInstallerForInstalledTool(t *testing.T) {
	catalog := Catalog{Tools: []Tool{{Name: "installed", Check: "true"}}}
	var output bytes.Buffer
	if err := Apply(catalog, Platform{OS: "linux", Shell: "/bin/sh"}, strings.NewReader(""), &output, &output, bufferReporter(&output), false); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !strings.Contains(output.String(), "already installed") {
		t.Errorf("output = %q", output.String())
	}
}
