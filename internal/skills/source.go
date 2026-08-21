package skills

import (
	"net/url"
	"strings"
)

func SourceNeedsGit(source string) bool {
	if strings.HasPrefix(strings.ToLower(source), "git@") {
		return true
	}
	parsed, err := url.Parse(source)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if strings.HasPrefix(scheme, "git+") || scheme == "ssh" || scheme == "git" {
		return true
	}
	if scheme != "http" && scheme != "https" {
		return false
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	if strings.HasSuffix(strings.ToLower(path), ".git") {
		return true
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "github.com", "gitlab.com", "bitbucket.org":
	default:
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}
