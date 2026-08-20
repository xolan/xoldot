package urlutil

import (
	"strings"
	"testing"
)

func TestRedactForDisplay(t *testing.T) {
	got := RedactForDisplay("https://token@github.com/owner/repo?access_token=secret")
	if strings.Contains(got, "token") || strings.Contains(got, "secret") {
		t.Errorf("RedactForDisplay() leaked credentials: %q", got)
	}
}
