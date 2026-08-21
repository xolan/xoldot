package skills

import "testing"

func TestSourceNeedsGit(t *testing.T) {
	for _, test := range []struct {
		source string
		want   bool
	}{
		{source: "/local/skill"},
		{source: "git@github.com:owner/repo.git", want: true},
		{source: "ssh://git@example.com/owner/repo", want: true},
		{source: "git://example.com/owner/repo", want: true},
		{source: "git+ssh://git@example.com/owner/repo", want: true},
		{source: "git+https://example.com/owner/repo", want: true},
		{source: "https://example.com/owner/repo.git", want: true},
		{source: "https://GitHub.COM/owner/repo", want: true},
		{source: "https://gitlab.com/owner/repo", want: true},
		{source: "https://bitbucket.org/owner/repo", want: true},
		{source: "https://example.com/download/archive.tar.gz"},
		{source: "https://github.com/owner/repo/tree/main"},
	} {
		t.Run(test.source, func(t *testing.T) {
			if got := SourceNeedsGit(test.source); got != test.want {
				t.Errorf("SourceNeedsGit(%q) = %t, want %t", test.source, got, test.want)
			}
		})
	}
}
