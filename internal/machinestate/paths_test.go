package machinestate

import (
	"path/filepath"
	"testing"
)

func TestIsReserved(t *testing.T) {
	home := t.TempDir()
	for _, relative := range []string{
		LinkLedgerRelativePath,
		LinkLockRelativePath,
		ScriptsStateRelativePath,
		ScriptsLockRelativePath,
		BackupsRelativePath,
		BackupsRelativePath + "/20260822T120000Z/manifest.json",
	} {
		if target := Path(home, relative); !IsReserved(home, target) {
			t.Errorf("IsReserved(%s) = false", target)
		}
	}

	for _, target := range []string{
		Path(home, RootRelativePath+"/other-state"),
		filepath.Join(home, ".config", "xoldot"),
	} {
		if IsReserved(home, target) {
			t.Errorf("IsReserved(%s) = true", target)
		}
	}
}
