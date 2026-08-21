package machinestate

import (
	"path/filepath"

	"github.com/xolan/xoldot/internal/pathutil"
)

const (
	RootRelativePath         = ".local/state/xoldot"
	LinkLedgerRelativePath   = RootRelativePath + "/links.json"
	LinkLockRelativePath     = RootRelativePath + "/links.lock"
	BackupsRelativePath      = RootRelativePath + "/backups"
	ScriptsStateRelativePath = RootRelativePath + "/scripts.json"
	ScriptsLockRelativePath  = RootRelativePath + "/scripts.lock"
)

func Path(home, relative string) string {
	return filepath.Join(home, filepath.FromSlash(relative))
}

func IsReserved(home, target string) bool {
	backups := Path(home, BackupsRelativePath)
	return target == Path(home, LinkLedgerRelativePath) ||
		target == Path(home, LinkLockRelativePath) ||
		target == Path(home, ScriptsStateRelativePath) ||
		target == Path(home, ScriptsLockRelativePath) ||
		target == backups || pathutil.Contains(backups, target)
}
