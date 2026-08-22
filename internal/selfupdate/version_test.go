package selfupdate

import "testing"

func TestParseReleaseVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		want  releaseVersion
		valid bool
	}{
		{value: "v0.0.0", want: releaseVersion{}, valid: true},
		{value: "v1.23.456", want: releaseVersion{major: 1, minor: 23, patch: 456}, valid: true},
		{value: "dev"},
		{value: "1.2.3"},
		{value: "v1.2"},
		{value: "v1.2.3.4"},
		{value: "v01.2.3"},
		{value: "v1.02.3"},
		{value: "v1.2.03"},
		{value: "v1.2.3-rc.1"},
		{value: "v18446744073709551616.0.0"},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			got, err := parseReleaseVersion(test.value)
			if test.valid && err != nil {
				t.Fatalf("parseReleaseVersion(%q) error = %v", test.value, err)
			}
			if !test.valid && err == nil {
				t.Fatalf("parseReleaseVersion(%q) = %+v, want error", test.value, got)
			}
			if got != test.want {
				t.Errorf("parseReleaseVersion(%q) = %+v, want %+v", test.value, got, test.want)
			}
		})
	}
}

func TestReleaseVersionCompare(t *testing.T) {
	t.Parallel()
	tests := []struct {
		left  releaseVersion
		right releaseVersion
		want  int
	}{
		{left: releaseVersion{1, 2, 3}, right: releaseVersion{1, 2, 3}, want: 0},
		{left: releaseVersion{2, 0, 0}, right: releaseVersion{1, 99, 99}, want: 1},
		{left: releaseVersion{1, 3, 0}, right: releaseVersion{1, 4, 0}, want: -1},
		{left: releaseVersion{1, 2, 4}, right: releaseVersion{1, 2, 3}, want: 1},
	}
	for _, test := range tests {
		if got := test.left.compare(test.right); got != test.want {
			t.Errorf("%s.compare(%s) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}
