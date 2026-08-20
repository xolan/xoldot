package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	err := Apply(catalog, Platform{OS: "linux", LinuxIDs: []string{"debian"}, Shell: "/bin/sh"}, strings.NewReader(""), &output, &output, false)
	if err != nil {
		t.Fatalf("Apply() error = %v\n%s", err, output.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("install command did not create marker: %v", err)
	}
	if !strings.Contains(output.String(), "running install command") {
		t.Errorf("output = %q", output.String())
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

	err := Apply(catalog, Platform{OS: "linux", LinuxIDs: []string{"debian"}, Shell: "/bin/sh"}, strings.NewReader(""), &output, &output, true)
	if err != nil {
		t.Fatalf("Apply() error = %v\n%s", err, output.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("dry run created the install marker")
	}
	if !strings.Contains(output.String(), "would run: touch") {
		t.Errorf("output = %q", output.String())
	}
}
