package config_test

import (
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"

	"github.com/xolan/xoldot/internal/aliases"
	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/lifecyclescripts"
	"github.com/xolan/xoldot/internal/profiles"
	"github.com/xolan/xoldot/internal/skills"
	"github.com/xolan/xoldot/internal/tools"
)

func TestExampleConfigurationMatchesCurrentSchemas(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate example test source")
	}
	paths := config.NewPaths(filepath.Join(filepath.Dir(source), "..", "..", "examples", "configuration"))

	configuration, err := config.Load(paths.Config)
	if err != nil {
		t.Fatalf("load example xoldot.toml: %v", err)
	}
	if !reflect.DeepEqual(configuration, config.Default()) {
		t.Errorf("example settings = %#v, want defaults %#v", configuration, config.Default())
	}
	if _, err := tools.Load(paths.Tools); err != nil {
		t.Fatalf("load example tools.toml: %v", err)
	}
	if _, err := aliases.Load(paths.Aliases); err != nil {
		t.Fatalf("load example files/aliases.toml: %v", err)
	}
	if _, err := skills.Load(paths.Skills); err != nil {
		t.Fatalf("load example skills.toml: %v", err)
	}
	if err := profiles.Validate(paths); err != nil {
		t.Fatalf("validate example Profiles: %v", err)
	}
	if _, err := lifecyclescripts.Load(paths.Root, paths.Scripts); err != nil {
		t.Fatalf("load example lifecycle scripts: %v", err)
	}

	work, err := profiles.Describe(paths, "work")
	if err != nil {
		t.Fatalf("describe example work Profile: %v", err)
	}
	for _, implicit := range []string{".agents/skills/focus", ".claude/skills/focus"} {
		if !slices.Contains(work.ManagedHome, implicit) {
			t.Errorf("work managed home does not include Skill path %q: %v", implicit, work.ManagedHome)
		}
	}
}
