package doctor

import (
	"os/exec"
	"strconv"
	"strings"

	"github.com/xolan/xoldot/internal/gitops"
)

type runtime struct {
	lookPath   func(string) (string, error)
	output     func(string, string, ...string) (string, error)
	inspectGit func(string, string, string, string) (gitops.LocalInspection, error)
}

func defaultRuntime() runtime {
	return runtime{
		lookPath: exec.LookPath,
		output: func(directory, executable string, arguments ...string) (string, error) {
			command := exec.Command(executable, arguments...)
			command.Dir = directory
			output, err := command.Output()
			return string(output), err
		},
		inspectGit: func(root, remote, branch, executable string) (gitops.LocalInspection, error) {
			return (gitops.Runner{Dir: root, Executable: executable}).InspectLocal(remote, branch)
		},
	}
}

func nodeVersionAtLeast(actual, minimum string) bool {
	actualParts := strings.Split(strings.TrimPrefix(strings.TrimSpace(actual), "v"), ".")
	minimumParts := strings.Split(minimum, ".")
	if len(actualParts) < 2 || len(minimumParts) < 2 {
		return false
	}
	actualMajor, majorErr := strconv.Atoi(actualParts[0])
	actualMinor, minorErr := strconv.Atoi(actualParts[1])
	minimumMajor, minimumMajorErr := strconv.Atoi(minimumParts[0])
	minimumMinor, minimumMinorErr := strconv.Atoi(minimumParts[1])
	if majorErr != nil || minorErr != nil || minimumMajorErr != nil || minimumMinorErr != nil {
		return false
	}
	return actualMajor > minimumMajor || actualMajor == minimumMajor && actualMinor >= minimumMinor
}
