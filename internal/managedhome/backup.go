package managedhome

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/xolan/xoldot/internal/pathutil"
)

const backupManifestName = "manifest.json"

type backupKind string

const (
	backupRegular backupKind = "file"
	backupSymlink backupKind = "symlink"
)

type backupManifest struct {
	Version  int            `json:"version"`
	ID       string         `json:"id"`
	Complete bool           `json:"complete"`
	Entries  []backupRecord `json:"entries"`
}

type backupRecord struct {
	Original    string     `json:"original"`
	Stored      string     `json:"stored"`
	Type        backupKind `json:"type"`
	Mode        uint32     `json:"mode"`
	Digest      string     `json:"digest"`
	Destination string     `json:"destination"`
}

type backupSession struct {
	id      string
	records []backupRecord
}

type BackupState string

const (
	BackupReady      BackupState = "ready"
	BackupIncomplete BackupState = "incomplete"
	BackupInvalid    BackupState = "invalid"
)

type BackupInspection struct {
	ID      string
	State   BackupState
	Problem string
}

var errIncompleteBackup = errors.New("backup is incomplete")

func backupDirectoryRelative(id string) string {
	return filepath.Join(filepath.FromSlash(backupsRelativePath), id)
}

func backupDirectory(layout managedHomeLayout, id string) string {
	return filepath.Join(layout.Home, backupDirectoryRelative(id))
}

func validateBackupID(id string) error {
	if len(id) != 24 {
		return fmt.Errorf("invalid backup ID %q", id)
	}
	decoded, err := hex.DecodeString(id)
	if err != nil || hex.EncodeToString(decoded) != id {
		return fmt.Errorf("invalid backup ID %q", id)
	}
	return nil
}

func eligibleBackupConflict(root *os.Root, layout managedHomeLayout, conflict plannedConflict) bool {
	relative, err := layout.homeRelative(conflict.entry.Target)
	if err != nil {
		return false
	}
	info, err := root.Lstat(relative)
	if err != nil {
		return false
	}
	_, err = backupKindFor(info.Mode())
	return err == nil
}

func beginBackup(root *os.Root, layout managedHomeLayout, transaction *linkTransaction) (*backupSession, error) {
	backupsRoot := filepath.FromSlash(backupsRelativePath)
	if err := transaction.makeDirectories(root, backupsRoot); err != nil {
		return nil, fmt.Errorf("create backup state directory: %w", err)
	}
	for range 100 {
		id, err := randomTransactionName("")
		if err != nil {
			return nil, err
		}
		directory := backupDirectoryRelative(id)
		if err := transaction.createPersistentDirectory(root, directory, backupDirectory(layout, id), 0o700); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("create backup directory %s: %w", backupDirectory(layout, id), err)
		}
		files := filepath.Join(directory, "files")
		if err := transaction.createPersistentDirectory(root, files, filepath.Join(backupDirectory(layout, id), "files"), 0o700); err != nil {
			return nil, fmt.Errorf("create backup files directory: %w", err)
		}
		session := &backupSession{id: id}
		manifestRelative := filepath.Join(directory, backupManifestName)
		data, err := encodeBackupManifest(backupManifest{Version: 1, ID: id})
		if err != nil {
			return nil, err
		}
		manifest, _, err := transaction.createFile(
			root,
			manifestRelative,
			filepath.Join(backupDirectory(layout, id), backupManifestName),
			0o600,
		)
		if err != nil {
			return nil, fmt.Errorf("create incomplete backup manifest: %w", err)
		}
		if _, err := manifest.Write(data); err != nil {
			_ = manifest.Close()
			return nil, fmt.Errorf("write incomplete backup manifest: %w", err)
		}
		if err := manifest.Sync(); err != nil {
			_ = manifest.Close()
			return nil, fmt.Errorf("sync incomplete backup manifest: %w", err)
		}
		if err := manifest.Close(); err != nil {
			return nil, fmt.Errorf("close incomplete backup manifest: %w", err)
		}
		return session, nil
	}
	return nil, fmt.Errorf("create unique backup directory in %s", filepath.Join(layout.Home, filepath.FromSlash(backupsRelativePath)))
}

func (session *backupSession) store(
	root *os.Root,
	layout managedHomeLayout,
	transaction *linkTransaction,
	link linkRecord,
) error {
	relative, err := layout.homeRelative(link.Target)
	if err != nil {
		return err
	}
	info, err := root.Lstat(relative)
	if err != nil {
		return fmt.Errorf("inspect backup source %s: %w", link.Target, err)
	}
	kind, err := backupKindFor(info.Mode())
	if err != nil {
		return fmt.Errorf("backup source %s changed after preparation: %w", link.Target, err)
	}
	digest, err := backupDigest(root, relative, kind, info)
	if err != nil {
		return fmt.Errorf("digest backup source %s: %w", link.Target, err)
	}
	storedRelative := filepath.Join(backupDirectoryRelative(session.id), "files", fmt.Sprintf("%06d", len(session.records)))
	stored := filepath.Join(layout.Home, storedRelative)
	if err := root.Rename(relative, storedRelative); err != nil {
		return fmt.Errorf("back up %s: %w", link.Target, err)
	}
	transaction.append(func() error {
		if _, err := root.Lstat(relative); err == nil {
			return fmt.Errorf(
				"refusing to restore %s because its path was replaced; original remains at %s",
				link.Target,
				stored,
			)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect backup restore target %s: %w", link.Target, err)
		}
		current, err := root.Lstat(storedRelative)
		if err != nil {
			return fmt.Errorf("inspect backup for %s: %w", link.Target, err)
		}
		if !os.SameFile(info, current) {
			return fmt.Errorf("refusing to restore %s because its backup was replaced", link.Target)
		}
		if err := root.Rename(storedRelative, relative); err != nil {
			return fmt.Errorf("restore %s: %w", link.Target, err)
		}
		return nil
	}, nil)
	storedInfo, err := root.Lstat(storedRelative)
	if err != nil {
		return fmt.Errorf("inspect stored backup for %s: %w", link.Target, err)
	}
	if !os.SameFile(info, storedInfo) {
		return fmt.Errorf("backup source %s changed while it was being moved", link.Target)
	}
	if uint32(storedInfo.Mode().Perm()) != uint32(info.Mode().Perm()) {
		return fmt.Errorf("backup source %s changed mode while it was being moved", link.Target)
	}
	storedDigest, err := backupDigest(root, storedRelative, kind, storedInfo)
	if err != nil {
		return fmt.Errorf("verify stored backup for %s: %w", link.Target, err)
	}
	if storedDigest != digest {
		return fmt.Errorf("backup source %s changed while it was being moved", link.Target)
	}
	session.records = append(session.records, backupRecord{
		Original:    link.Target,
		Stored:      stored,
		Type:        kind,
		Mode:        uint32(info.Mode().Perm()),
		Digest:      digest,
		Destination: link.Destination,
	})
	return nil
}

func (session *backupSession) finish(root *os.Root) error {
	data, err := encodeBackupManifest(backupManifest{
		Version:  1,
		ID:       session.id,
		Complete: true,
		Entries:  session.records,
	})
	if err != nil {
		return err
	}
	manifestRelative := filepath.Join(backupDirectoryRelative(session.id), backupManifestName)
	manifest, err := root.OpenFile(manifestRelative, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open backup manifest: %w", err)
	}
	if _, err := manifest.Write(data); err != nil {
		_ = manifest.Close()
		return fmt.Errorf("write backup manifest: %w", err)
	}
	if err := manifest.Chmod(0o600); err != nil {
		_ = manifest.Close()
		return fmt.Errorf("set backup manifest permissions: %w", err)
	}
	if err := manifest.Sync(); err != nil {
		_ = manifest.Close()
		return fmt.Errorf("sync backup manifest: %w", err)
	}
	if err := manifest.Close(); err != nil {
		return fmt.Errorf("close backup manifest: %w", err)
	}
	return nil
}

func backupKindFor(mode os.FileMode) (backupKind, error) {
	switch {
	case mode.IsRegular():
		return backupRegular, nil
	case mode&os.ModeSymlink != 0:
		return backupSymlink, nil
	default:
		return "", fmt.Errorf("directories and special files are not supported")
	}
}

func backupDigest(root *os.Root, relative string, kind backupKind, identity os.FileInfo) (string, error) {
	hash := sha256.New()
	switch kind {
	case backupRegular:
		file, err := root.Open(relative)
		if err != nil {
			return "", err
		}
		info, err := file.Stat()
		if err == nil && !os.SameFile(identity, info) {
			err = fmt.Errorf("file was replaced")
		}
		if err == nil {
			_, err = io.Copy(hash, file)
		}
		return hex.EncodeToString(hash.Sum(nil)), errors.Join(err, file.Close())
	case backupSymlink:
		destination, err := root.Readlink(relative)
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(hash, destination)
		return hex.EncodeToString(hash.Sum(nil)), nil
	default:
		return "", fmt.Errorf("unsupported backup type %q", kind)
	}
}

func encodeBackupManifest(manifest backupManifest) ([]byte, error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode backup manifest: %w", err)
	}
	return append(data, '\n'), nil
}

func loadBackupManifest(root *os.Root, layout managedHomeLayout, id string) (backupManifest, []os.FileInfo, error) {
	manifestPath := filepath.Join(backupDirectoryRelative(id), backupManifestName)
	data, err := root.ReadFile(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return backupManifest{}, nil, errIncompleteBackup
	}
	if err != nil {
		return backupManifest{}, nil, fmt.Errorf("read backup manifest %s: %w", filepath.Join(backupDirectory(layout, id), backupManifestName), err)
	}
	var manifest backupManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return backupManifest{}, nil, fmt.Errorf("parse backup manifest %s: %w", filepath.Join(backupDirectory(layout, id), backupManifestName), err)
	}
	if !manifest.Complete {
		return backupManifest{}, nil, errIncompleteBackup
	}
	if err := validateBackupManifest(layout, id, manifest); err != nil {
		return backupManifest{}, nil, err
	}
	identities, err := validateBackupFiles(root, layout, manifest)
	if err != nil {
		return backupManifest{}, nil, err
	}
	return manifest, identities, nil
}

func validateBackupManifest(layout managedHomeLayout, id string, manifest backupManifest) error {
	if manifest.Version != 1 {
		return fmt.Errorf("unsupported backup manifest version %d", manifest.Version)
	}
	if manifest.ID != id {
		return fmt.Errorf("backup manifest ID %q does not match directory %q", manifest.ID, id)
	}
	if len(manifest.Entries) == 0 {
		return fmt.Errorf("backup manifest %s has no entries", id)
	}
	directory := backupDirectory(layout, id)
	originals := make(map[string]struct{}, len(manifest.Entries))
	storedPaths := make(map[string]struct{}, len(manifest.Entries))
	links := make([]linkRecord, 0, len(manifest.Entries))
	for _, record := range manifest.Entries {
		if filepath.Clean(record.Original) != record.Original || !filepath.IsAbs(record.Original) || record.Original == layout.Home || !pathutil.Contains(layout.Home, record.Original) {
			return fmt.Errorf("backup %s original path %q is outside the Target home", id, record.Original)
		}
		if filepath.Clean(record.Stored) != record.Stored || !filepath.IsAbs(record.Stored) || record.Stored == directory || !pathutil.Contains(directory, record.Stored) {
			return fmt.Errorf("backup %s stored path %q is outside its backup directory", id, record.Stored)
		}
		if _, exists := originals[record.Original]; exists {
			return fmt.Errorf("backup %s repeats original path %q", id, record.Original)
		}
		if _, exists := storedPaths[record.Stored]; exists {
			return fmt.Errorf("backup %s repeats stored path %q", id, record.Stored)
		}
		originals[record.Original] = struct{}{}
		storedPaths[record.Stored] = struct{}{}
		if record.Type != backupRegular && record.Type != backupSymlink {
			return fmt.Errorf("backup %s has unsupported type %q", id, record.Type)
		}
		if record.Mode&^uint32(os.ModePerm) != 0 {
			return fmt.Errorf("backup %s has invalid mode %o for %s", id, record.Mode, record.Original)
		}
		decoded, err := hex.DecodeString(record.Digest)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != record.Digest {
			return fmt.Errorf("backup %s has invalid digest for %s", id, record.Original)
		}
		if record.Destination == "" {
			return fmt.Errorf("backup %s has an empty managed destination for %s", id, record.Original)
		}
		links = append(links, linkRecord{
			Target:      record.Original,
			Destination: record.Destination,
		})
	}
	if err := validateLedger(linkLedger{Version: 1, Links: links}, layout.Home, layout.ManagedRoot); err != nil {
		return fmt.Errorf("backup %s has invalid managed links: %w", id, err)
	}
	return nil
}

func validateBackupFiles(root *os.Root, layout managedHomeLayout, manifest backupManifest) ([]os.FileInfo, error) {
	allowed := map[string]struct{}{}
	directory := backupDirectoryRelative(manifest.ID)
	allowPathAndParents(allowed, directory, filepath.Join(directory, backupManifestName))
	identities := make([]os.FileInfo, len(manifest.Entries))
	for index, record := range manifest.Entries {
		relative, err := layout.homeRelative(record.Stored)
		if err != nil {
			return nil, err
		}
		allowPathAndParents(allowed, directory, relative)
		info, err := root.Lstat(relative)
		if err != nil {
			return nil, fmt.Errorf("inspect stored backup %s: %w", record.Stored, err)
		}
		kind, err := backupKindFor(info.Mode())
		if err != nil || kind != record.Type || uint32(info.Mode().Perm()) != record.Mode {
			return nil, fmt.Errorf("stored backup %s does not match its manifest type or mode", record.Stored)
		}
		digest, err := backupDigest(root, relative, kind, info)
		if err != nil {
			return nil, fmt.Errorf("digest stored backup %s: %w", record.Stored, err)
		}
		if digest != record.Digest {
			return nil, fmt.Errorf("stored backup %s does not match its manifest digest", record.Stored)
		}
		identities[index] = info
	}
	err := fs.WalkDir(root.FS(), directory, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if _, ok := allowed[path]; !ok {
			return fmt.Errorf("backup %s contains untracked path %s", manifest.ID, filepath.Join(layout.Home, path))
		}
		return nil
	})
	return identities, err
}

func allowPathAndParents(allowed map[string]struct{}, root, path string) {
	allowed[root] = struct{}{}
	for current := path; current != root && pathutil.Contains(root, current); current = filepath.Dir(current) {
		allowed[current] = struct{}{}
	}
}

func InspectBackups(managedRoot, home, configRoot string) ([]BackupInspection, error) {
	layout, err := newManagedHomeLayout(managedRoot, home, configRoot)
	if err != nil {
		return nil, err
	}
	if layout.homeIdentity == nil {
		return nil, nil
	}
	root, err := layout.openHomeRoot()
	if err != nil {
		return nil, err
	}
	directory := filepath.FromSlash(backupsRelativePath)
	entries, err := fs.ReadDir(root.FS(), directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, root.Close()
	}
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("inspect backup directory %s: %w", filepath.Join(layout.Home, directory), err),
			root.Close(),
		)
	}
	inspections := make([]BackupInspection, 0, len(entries))
	for _, entry := range entries {
		inspection := BackupInspection{ID: entry.Name()}
		if !entry.IsDir() {
			inspection.State = BackupInvalid
			inspection.Problem = "backup entry is not a directory"
			inspections = append(inspections, inspection)
			continue
		}
		if err := validateBackupID(entry.Name()); err != nil {
			inspection.State = BackupInvalid
			inspection.Problem = err.Error()
			inspections = append(inspections, inspection)
			continue
		}
		_, _, err := loadBackupManifest(root, layout, entry.Name())
		switch {
		case err == nil:
			inspection.State = BackupReady
		case errors.Is(err, errIncompleteBackup):
			inspection.State = BackupIncomplete
			inspection.Problem = err.Error()
		default:
			inspection.State = BackupInvalid
			inspection.Problem = err.Error()
		}
		inspections = append(inspections, inspection)
	}
	return inspections, root.Close()
}
