package selfupdate

import (
	"fmt"
	"strconv"
	"strings"
)

type releaseVersion struct {
	major uint64
	minor uint64
	patch uint64
}

func parseReleaseVersion(value string) (releaseVersion, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "v") {
		return releaseVersion{}, fmt.Errorf("release version %q must start with v", value)
	}
	parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
	if len(parts) != 3 {
		return releaseVersion{}, fmt.Errorf("release version %q must have three parts", value)
	}

	var values [3]uint64
	for index, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return releaseVersion{}, fmt.Errorf("release version %q is not canonical", value)
		}
		parsed, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return releaseVersion{}, fmt.Errorf("parse release version %q: %w", value, err)
		}
		values[index] = parsed
	}

	return releaseVersion{major: values[0], minor: values[1], patch: values[2]}, nil
}

func (version releaseVersion) String() string {
	return fmt.Sprintf("v%d.%d.%d", version.major, version.minor, version.patch)
}

func (version releaseVersion) compare(other releaseVersion) int {
	left := [...]uint64{version.major, version.minor, version.patch}
	right := [...]uint64{other.major, other.minor, other.patch}
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
}
