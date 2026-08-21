package doctor

import (
	"errors"
	"fmt"
	"os"

	"github.com/xolan/xoldot/internal/config"
	"github.com/xolan/xoldot/internal/pathutil"
)

type resolvedPaths struct {
	root   string
	home   string
	rootOK bool
	homeOK bool
}

func inspectPaths(paths config.Paths) (resolvedPaths, []string) {
	var resolved resolvedPaths
	var failures []string
	root, err := pathutil.ResolveExistingPrefix(paths.Root)
	if err != nil {
		failures = append(failures, fmt.Sprintf("resolve Configuration directory %s: %v", paths.Root, err))
	} else {
		resolved.root = root
		resolved.rootOK = true
	}

	home, err := config.TargetHome()
	if err != nil {
		failures = append(failures, err.Error())
	} else if home, err = pathutil.ResolveExistingPrefix(home); err != nil {
		failures = append(failures, fmt.Sprintf("resolve Target home: %v", err))
	} else {
		info, statErr := os.Stat(home)
		if statErr == nil && !info.IsDir() {
			failures = append(failures, fmt.Sprintf("Target home %s is not a directory", home))
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			failures = append(failures, fmt.Sprintf("inspect Target home %s: %v", home, statErr))
		} else {
			resolved.home = home
			resolved.homeOK = true
		}
	}

	if resolved.rootOK {
		configurationPaths := []string{paths.Config, paths.Tools, paths.Aliases, paths.Skills, paths.Profiles, paths.ManagedHome}
		for _, path := range configurationPaths {
			candidate, pathErr := pathutil.ResolveExistingPrefix(path)
			if pathErr != nil {
				failures = append(failures, fmt.Sprintf("resolve Configuration path %s: %v", path, pathErr))
				continue
			}
			if candidate == resolved.root || !pathutil.Contains(resolved.root, candidate) {
				failures = append(failures, fmt.Sprintf("Configuration path %s does not resolve beneath %s", path, resolved.root))
			}
		}
	}
	if resolved.rootOK && resolved.homeOK && pathutil.Contains(resolved.root, resolved.home) {
		failures = append(failures, fmt.Sprintf("Target home %s resolves inside the Configuration directory %s", resolved.home, resolved.root))
	}
	return resolved, failures
}
