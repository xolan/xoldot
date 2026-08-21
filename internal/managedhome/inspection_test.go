package managedhome

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectReportsCurrentLink(t *testing.T) {
	fixture := newInspectionFixture(t, true)
	source := fixture.writeManaged(t, ".current")
	if err := os.Symlink(source, filepath.Join(fixture.home, ".current")); err != nil {
		t.Fatal(err)
	}

	inspection, err := Inspect(fixture.managed, fixture.home, fixture.configRoot)
	if err != nil {
		t.Fatal(err)
	}
	assertSingleInspectionState(t, inspection, StateCurrent)
}

func TestInspectReportsMissingLinkWithoutCreatingHome(t *testing.T) {
	fixture := newInspectionFixture(t, false)
	fixture.writeManaged(t, ".missing")

	inspection, err := Inspect(fixture.managed, fixture.home, fixture.configRoot)
	if err != nil {
		t.Fatal(err)
	}
	assertSingleInspectionState(t, inspection, StateMissing)
	if _, err := os.Stat(fixture.home); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("inspection created Target home: %v", err)
	}
}

func TestInspectReportsConflictingLink(t *testing.T) {
	fixture := newInspectionFixture(t, true)
	fixture.writeManaged(t, ".conflict")
	if err := os.WriteFile(filepath.Join(fixture.home, ".conflict"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}

	inspection, err := Inspect(fixture.managed, fixture.home, fixture.configRoot)
	if err != nil {
		t.Fatal(err)
	}
	assertSingleInspectionState(t, inspection, StateConflict)
}

func TestInspectReportsStaleOwnedLink(t *testing.T) {
	fixture := newInspectionFixture(t, true)
	source := fixture.writeManaged(t, ".stale")
	if _, err := Link(fixture.managed, fixture.home, fixture.configRoot, discardReporter, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}

	inspection, err := Inspect(fixture.managed, fixture.home, fixture.configRoot)
	if err != nil {
		t.Fatal(err)
	}
	assertSingleInspectionState(t, inspection, StateStale)
}

type inspectionFixture struct {
	managed    string
	home       string
	configRoot string
}

func newInspectionFixture(t *testing.T, createHome bool) inspectionFixture {
	t.Helper()
	root := t.TempDir()
	fixture := inspectionFixture{
		managed:    filepath.Join(root, "managed"),
		home:       filepath.Join(root, "home"),
		configRoot: filepath.Join(root, "config"),
	}
	directories := []string{fixture.managed, fixture.configRoot}
	if createHome {
		directories = append(directories, fixture.home)
	}
	for _, directory := range directories {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (fixture inspectionFixture) writeManaged(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(fixture.managed, name)
	if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertSingleInspectionState(t *testing.T, inspection Inspection, want State) {
	t.Helper()
	entries := inspection.Entries()
	if len(entries) != 1 || entries[0].State != want {
		t.Fatalf("inspection = %+v, want one %q item", entries, want)
	}
}
