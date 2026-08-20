package urlutil

import "net/url"

func RedactForDisplay(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return value
	}
	if parsed.User != nil {
		parsed.User = url.User("<redacted>")
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = "redacted"
	}
	return parsed.String()
}
